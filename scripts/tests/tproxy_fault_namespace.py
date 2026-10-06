#!/usr/bin/env python3
"""Проверка отката watchdog при гибели owner/Xray в изолированном netns."""
import fcntl
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import time


ID_BASE = "e" * 31


def require_namespace():
    if os.geteuid() != 0:
        raise RuntimeError("Тест требует root внутри disposable network namespace")
    if os.getpid() == 1:
        raise RuntimeError("Запуск запрещён: тестовый процесс является PID 1")
    current = os.readlink("/proc/self/ns/net")
    init = os.readlink("/proc/1/ns/net")
    if current == init:
        raise RuntimeError("Запуск запрещён: текущий network namespace совпадает с namespace PID 1; запустите через unshare -n")


def run(*args, input=None, check=True):
    result = subprocess.run(args, input=input, text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(f"Команда {args} завершилась с ошибкой: {result.stderr or result.stdout}")
    return result


def identity(pid):
    raw = pathlib.Path(f"/proc/{pid}/stat").read_text()
    fields = raw.rsplit(") ", 1)[1].split()
    return {"pid": pid, "start": fields[19],
            "boot": pathlib.Path("/proc/sys/kernel/random/boot_id").read_text().strip()}


def write_json(path, value):
    temporary = path.with_suffix(path.suffix + ".tmp")
    with temporary.open("w") as stream:
        json.dump(value, stream, indent=2, separators=(",", ": "))
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def locked(directory):
    class Lock:
        def __enter__(self):
            self.file = (directory / "network.lock").open("a+")
            fcntl.flock(self.file, fcntl.LOCK_EX)
        def __exit__(self, *_):
            fcntl.flock(self.file, fcntl.LOCK_UN)
            self.file.close()
    return Lock()


def identity_server():
    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.setsockopt(socket.SOL_IP, 19, 1)
    listener.bind(("127.0.0.1", 12345))
    listener.listen()
    while True:
        connection, _ = listener.accept()
        connection.close()


def wait_for(predicate, description, seconds=15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.05)
    raise RuntimeError(f"Ожидание истекло: {description}")


def tracked_table(transaction):
    return f'''table inet uvg_tproxy {{
 comment "uvg:{transaction}";
 chain uvg_output {{ type route hook output priority mangle; policy accept;
  ct state new,established,related counter accept
 }}
 chain uvg_prerouting {{ type filter hook prerouting priority mangle; policy accept;
  ct state new,established,related counter accept
 }}
}}
'''


def make_fixture(root, transaction, owner, xray):
    directory = root / transaction
    directory.mkdir(mode=0o700)
    snapshot = b"{}"
    config = b"{}"
    (directory / "snapshot.json").write_bytes(snapshot)
    (directory / "xray.json").write_bytes(config)
    manifest = {
        "plan": {
            "transaction_id": transaction,
            "endpoint_ip": "203.0.113.50",
            "endpoint_port": 443,
            "management_peers": ["198.51.100.2"],
            "lan4": [], "lan6": [], "ssh_port": 22, "ui_port": 8443,
            "deadline_seconds": 120,
        },
        "owner": owner, "xray": xray,
        "snapshot_hash": hashlib.sha256(snapshot).hexdigest(),
        "config_hash": hashlib.sha256(config).hexdigest(),
        "device": "lo", "gateway": "", "host_route": False,
    }
    raw = json.dumps(manifest, separators=(",", ": ")).encode()
    (directory / "manifest.json").write_bytes(raw)
    (directory / "manifest.sha256").write_text(hashlib.sha256(raw).hexdigest())
    return directory, hashlib.sha256(raw).hexdigest()


def arm(helper, root, directory, transaction):
    monitor = subprocess.Popen([helper, "monitor", transaction, "--root", str(root)],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    armed_path = directory / "armed.json"
    wait_for(lambda: armed_path.exists() or monitor.poll() is not None, "durable armed ACK")
    if monitor.poll() is not None:
        out, err = monitor.communicate()
        raise RuntimeError(f"Watchdog не вооружился до сетевых изменений: {err or out}")
    armed = json.loads(armed_path.read_text())
    assert armed["transaction_id"] == transaction
    return monitor, armed


def update_state(directory, transaction, manifest_hash, previous, state, flow_hash=None):
    record = {
        "transaction_id": transaction, "manifest_hash": manifest_hash,
        "state": state, "deadline": previous["deadline"],
        "message": "Изолированный fault injection",
    }
    if flow_hash:
        record["flow_hash"] = flow_hash
    write_json(directory / "state.json", record)


def setup_foreign_state():
    run("nft", "-f", "-", input="""table inet uvg_fault_foreign {
 chain preserved { type filter hook output priority 10; policy accept; }
}
""")
    run("ip", "-4", "route", "add", "local", "192.0.2.0/24", "dev", "lo", "table", "123")
    run("ip", "-4", "rule", "add", "priority", "12345", "lookup", "123")


def table200_routes():
    result = run("ip", "-N", "-j", "-4", "route", "show", "table", "200", check=False)
    if result.returncode:
        if "fib table does not exist" in result.stderr.lower():
            return []
        raise RuntimeError(f"Не удалось проверить routing table 200: {result.stderr or result.stdout}")
    return json.loads(result.stdout)


def foreign_snapshot():
    nft = json.loads(run("nft", "-j", "list", "table", "inet", "uvg_fault_foreign").stdout)
    rules = json.loads(run("ip", "-j", "-4", "rule", "show").stdout)
    routes = json.loads(run("ip", "-j", "-4", "route", "show", "table", "123").stdout)
    foreign_rules = [rule for rule in rules if rule.get("priority") == 12345 and str(rule.get("table")) == "123"]
    return nft, foreign_rules, routes


def assert_rollback(transaction, baseline):
    tables = json.loads(run("nft", "-j", "list", "tables").stdout)
    assert not any(item.get("table", {}).get("family") == "inet" and
                   item.get("table", {}).get("name") == "uvg_tproxy" for item in tables["nftables"]), tables
    routes = table200_routes()
    assert routes == [], f"rollback оставил маршруты в table 200: {routes}"
    nft, rules, foreign_routes = foreign_snapshot()
    assert nft == baseline[0], f"изменена чужая nft table: {nft}"
    assert rules == baseline[1], f"изменены чужие policy rules: {rules}"
    assert foreign_routes == baseline[2], f"изменён чужой routing table: {foreign_routes}"


def prepare_phase(phase, helper, root, directory, transaction, manifest_hash, armed):
    if phase == "armed":
        tables = json.loads(run("nft", "-j", "list", "tables").stdout)
        assert not any(item.get("table", {}).get("name") == "uvg_tproxy" for item in tables["nftables"])
        assert table200_routes() == []
        return
    if phase == "pending":
        result = run(helper, "namespace-apply", transaction, "--root", str(root), check=False)
        if result.returncode:
            raise RuntimeError(f"NamespaceApply не прошёл: {result.stderr or result.stdout}")
        pending = json.loads((directory / "state.json").read_text())
        assert pending["state"] == "pending" and pending["flow_hash"]
        return

    with locked(directory):
        update_state(directory, transaction, manifest_hash, armed, "tracking")
        run("nft", "-f", "-", input=tracked_table(transaction))
        if phase == "tracking":
            return
        update_state(directory, transaction, manifest_hash, armed, "sealing")
        flows_path = directory / "flows.json"
        if phase == "sealing":
            flows_path.write_text("[]")
            return
        flow_raw = json.dumps([{
            "local_ip": "127.0.0.1", "remote_ip": "198.51.100.2",
            "local_server_port": 8443, "remote_client_port": 53211,
        }], indent=2, separators=(",", ": ")).encode()
        flows_path.write_bytes(flow_raw)
        flow_hash = hashlib.sha256(flow_raw).hexdigest()
        seal = {"transaction_id": transaction, "manifest_hash": manifest_hash, "flow_hash": flow_hash}
        write_json(directory / "flowseal.json", seal)
        update_state(directory, transaction, manifest_hash, armed, "sealed", flow_hash)

    if phase == "sealed":
        wait_for(lambda: (directory / "flow-ack.json").exists(), "watchdog ACK sealed flow snapshot")
        ack = json.loads((directory / "flow-ack.json").read_text())
        assert ack["transaction_id"] == transaction and ack["manifest_hash"] == manifest_hash
        assert ack["flow_hash"] == flow_hash and ack["state"] == "sealed"
        return

    raise ValueError(phase)


def one_case(helper, root, phase, victim, index, baseline=None):
    transaction = ID_BASE + f"{index:01x}"
    owner = subprocess.Popen(["sleep", "300"])
    xray = subprocess.Popen([sys.executable, __file__, "server"])
    monitor = None
    try:
        time.sleep(0.1)
        directory, manifest_hash = make_fixture(root, transaction, identity(owner.pid), identity(xray.pid))
        monitor, armed = arm(helper, root, directory, transaction)
        if baseline is None:
            setup_foreign_state()
            baseline = foreign_snapshot()
        prepare_phase(phase, helper, root, directory, transaction, manifest_hash, armed)
        (owner if victim == "owner" else xray).kill()
        (owner if victim == "owner" else xray).wait(timeout=5)
        wait_for(lambda: json.loads((directory / "state.json").read_text()).get("state") in
                 ("rolled_back", "rollback_failed"), f"rollback после гибели {victim} на стадии {phase}")
        state = json.loads((directory / "state.json").read_text())
        assert state["state"] == "rolled_back", f"watchdog rollback failed: {state}"
        monitor.wait(timeout=5)
        if monitor.returncode:
            out, err = monitor.communicate()
            raise RuntimeError(f"Watchdog завершился с ошибкой после rollback: {err or out}")
        assert_rollback(transaction, baseline)
        print(f"PASS {index + 1:02d}/10: {phase}, процесс {victim} завершён, состояние rolled_back")
        return baseline
    finally:
        for process in (owner, xray):
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
        if monitor is not None and monitor.poll() is None:
            monitor.terminate()
            try:
                monitor.wait(timeout=5)
            except subprocess.TimeoutExpired:
                monitor.kill()
                monitor.wait()


def main(helper):
    require_namespace()
    if not os.path.isabs(helper) or not os.access(helper, os.X_OK):
        raise RuntimeError("Укажите абсолютный путь к исполняемому uvg-watchdog")
    with tempfile.TemporaryDirectory(prefix="uvg-fault-ns-") as temp:
        root = pathlib.Path(temp)
        baseline = None
        cases = [(phase, victim) for phase in ("armed", "tracking", "sealing", "sealed", "pending")
                 for victim in ("owner", "xray")]
        for index, (phase, victim) in enumerate(cases):
            baseline = one_case(helper, root, phase, victim, index, baseline)
        print("PASS: десять изолированных owner/Xray fault cases; чужие правила и маршруты сохранены")


if __name__ == "__main__":
    if len(sys.argv) == 2 and sys.argv[1] == "server":
        identity_server()
    elif len(sys.argv) == 2:
        try:
            main(sys.argv[1])
        except Exception as error:
            print(f"ОШИБКА: {error}", file=sys.stderr)
            sys.exit(1)
    else:
        print("Использование: tproxy_fault_namespace.py /absolute/path/uvg-watchdog", file=sys.stderr)
        sys.exit(2)
