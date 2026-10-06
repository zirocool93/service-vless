#!/usr/bin/env python3
"""Root acceptance: все мутации внутри unshare -n, после независимого armed ACK."""
import hashlib
import json
import os
import pathlib
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import shutil


def identity(pid):
    stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
    return {"pid": pid, "start": stat[19], "boot": pathlib.Path("/proc/sys/kernel/random/boot_id").read_text().strip()}


def server(directory):
    def tcp(port, prefix):
        listener = socket.socket()
        listener.setsockopt(socket.SOL_IP, 19, 1)
        listener.bind(("127.0.0.1", port))
        listener.listen()
        def accept():
            while True:
                conn, _ = listener.accept()
                with conn:
                    conn.sendall(prefix + conn.recv(100))
        threading.Thread(target=accept, daemon=True).start()

    def udp(port, prefix):
        listener = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        listener.setsockopt(socket.SOL_IP, 19, 1)
        listener.setsockopt(socket.SOL_IP, 20, 1)
        listener.bind(("127.0.0.1", port))
        def receive():
            while True:
                data, ancillary, _, peer = listener.recvmsg(100, 100)
                raw = next(raw for level, kind, raw in ancillary if level == socket.SOL_IP and kind == 20)
                destination = (socket.inet_ntoa(raw[4:8]), struct.unpack("!H", raw[2:4])[0])
                with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as reply:
                    reply.setsockopt(socket.SOL_IP, 19, 1)
                    reply.setsockopt(socket.SOL_SOCKET, 36, 512)
                    reply.bind(destination)
                    reply.sendto(prefix + data, peer)
        threading.Thread(target=receive, daemon=True).start()

    for port, prefix in [(12345, b"TPROXY:"), (12346, b"DNS:")]:
        tcp(port, prefix)
        udp(port, prefix)
    pathlib.Path(directory, "server-ready").touch()
    while True:
        time.sleep(1)


def management_listener(port):
    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind(("10.200.0.1", port))
    listener.listen()
    def accept():
        while True:
            conn, _ = listener.accept()
            def serve(connection):
                with connection:
                    while True:
                        data = connection.recv(100)
                        if not data: break
                        connection.sendall(data)
            threading.Thread(target=serve, args=(conn,), daemon=True).start()
    threading.Thread(target=accept, daemon=True).start()
    return listener


def main(helper):
    with tempfile.TemporaryDirectory(prefix="uvg-ns-") as root:
        transaction = "d" * 32
        directory = pathlib.Path(root, transaction)
        directory.mkdir(mode=0o700)
        child = subprocess.Popen([sys.executable, __file__, "server", str(directory)])
        watchdog = None
        try:
            for _ in range(50):
                if (directory / "server-ready").exists(): break
                time.sleep(.05)
            else: raise RuntimeError("Transparent listener не готов")
            raw = b"{}"
            digest = hashlib.sha256(raw).hexdigest()
            for name in ["snapshot.json", "xray.json"]:
                (directory / name).write_bytes(raw)
            manifest = {"plan": {"transaction_id": transaction, "endpoint_ip": "203.0.113.50", "endpoint_port": 443,
                                 "management_peers": ["198.51.100.2"], "ssh_port": 22, "ui_port": 8443,
                                 "lan4": ["10.200.0.0/24"], "lan6": []},
                        "owner": identity(os.getpid()), "xray": identity(child.pid), "snapshot_hash": digest,
                        "config_hash": digest, "device": "lo", "gateway": "", "host_route": False}
            payload = json.dumps(manifest).encode()
            (directory / "manifest.json").write_bytes(payload)
            (directory / "manifest.sha256").write_text(hashlib.sha256(payload).hexdigest())
            watchdog = subprocess.Popen([helper, "monitor", transaction, "--root", root])
            for _ in range(50):
                if (directory / "armed.json").exists(): break
                time.sleep(.05)
            else: raise RuntimeError("Нет armed ACK, мутации запрещены")
            armed = json.loads((directory / "armed.json").read_text())
            assert armed["transaction_id"] == transaction and armed["manifest_hash"] == hashlib.sha256(payload).hexdigest()

            def run(*args, input=None):
                result = subprocess.run(args, input=input, text=True, capture_output=True)
                if result.returncode: raise RuntimeError(f"{args}: {result.stderr}")
            # Первая и все последующие сетевые мутации разрешены только после ACK выше.
            run("ip", "link", "set", "lo", "up")
            run("ip", "link", "add", "nettest", "type", "dummy")
            run("ip", "addr", "add", "10.200.0.10/32", "dev", "nettest")
            run("ip", "link", "set", "nettest", "up")
            run("ip", "link", "add", "mgmt-host", "type", "veth", "peer", "name", "mgmt-client")
            run("ip", "addr", "add", "10.200.0.1/32", "dev", "mgmt-host")
            run("ip", "addr", "add", "198.51.100.2/32", "dev", "mgmt-client")
            run("ip", "link", "set", "mgmt-host", "up")
            run("ip", "link", "set", "mgmt-client", "up")
            run("ip", "route", "add", "10.200.0.1/32", "dev", "mgmt-client", "src", "198.51.100.2")
            run("ip", "route", "add", "198.51.100.2/32", "dev", "mgmt-host", "src", "10.200.0.1")
            listeners = [management_listener(22), management_listener(8443)]
            time.sleep(.1)
            old = socket.socket()
            old.settimeout(3)
            old.bind(("198.51.100.2", 0))
            old.connect(("10.200.0.1", 8443))
            old_client_port = old.getsockname()[1]

            # Воспроизводим sysctl целевой VM внутри одноразового namespace.
            # Значения фиксируются для диагностики; namespace уничтожается после теста.
            sysctl_keys = [
                f"net.ipv4.conf.{interface}.{setting}"
                for interface in ["all", "lo", "nettest"]
                for setting in ["rp_filter", "accept_local"]
            ]
            original_sysctls = {
                key: subprocess.check_output(["sysctl", "-n", key], text=True).strip()
                for key in sysctl_keys
            }
            for interface in ["all", "lo", "nettest"]:
                run("sysctl", "-q", "-w", f"net.ipv4.conf.{interface}.rp_filter=2")
                run("sysctl", "-q", "-w", f"net.ipv4.conf.{interface}.accept_local=0")
            applied_sysctls = {
                key: subprocess.check_output(["sysctl", "-n", key], text=True).strip()
                for key in sysctl_keys
            }
            assert all(applied_sysctls[key] == ("2" if key.endswith("rp_filter") else "0") for key in sysctl_keys), \
                (original_sysctls, applied_sysctls)
            for prefix in ["203.0.113.0/24", "198.51.100.0/24", "10.200.0.0/24"]:
                run("ip", "route", "add", prefix, "dev", "lo", "src", "10.200.0.10")
            run("ip", "-6", "addr", "add", "2001:db8:1::1/128", "dev", "lo")
            run("ip", "-6", "route", "add", "2001:db8:2::/64", "dev", "lo", "src", "2001:db8:1::1")
            run("nft", "-f", "-", input="table inet uvg_foreign {\n chain preserved {\n type filter hook output priority 0; policy accept;\n }\n}\n")
            foreign = subprocess.check_output(["nft", "-j", "list", "table", "inet", "uvg_foreign"])
            real_nft = shutil.which("nft")
            wrappers = pathlib.Path(root, "nft-wrapper")
            wrappers.mkdir()
            wrapper = wrappers / "nft"
            wrapper.write_text("""#!/usr/bin/env python3
import os, pathlib, subprocess, sys, time
root = pathlib.Path(%r)
real = %r
count = root / "count"
if sys.argv[1:] == ["-f", "-"]:
    n = int(count.read_text()) + 1 if count.exists() else 1
    count.write_text(str(n))
    payload = sys.stdin.buffer.read()
    if n == 2:
        (root / "final-started").touch()
        while not (root / "release").exists(): time.sleep(.01)
    proc = subprocess.run([real, *sys.argv[1:]], input=payload)
    sys.exit(proc.returncode)
os.execv(real, [real, *sys.argv[1:]])
""" % (str(wrappers), real_nft))
            wrapper.chmod(0o755)
            helper_env = os.environ.copy()
            helper_env["PATH"] = str(wrappers) + os.pathsep + helper_env["PATH"]
            apply = subprocess.Popen([helper, "namespace-apply", transaction, "--root", root], env=helper_env,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            for _ in range(500):
                if (wrappers / "final-started").exists(): break
                if apply.poll() is not None:
                    out, err = apply.communicate()
                    raise RuntimeError(f"namespace-apply завершился до final publish: {err or out}")
                time.sleep(.01)
            else: raise RuntimeError("Не достигнут staged barrier перед final nft publish")
            new = socket.socket()
            new.settimeout(3)
            new.bind(("198.51.100.2", 0))
            new.connect(("10.200.0.1", 8443))
            new_client_port = new.getsockname()[1]
            flow_data = json.loads((directory / "flows.json").read_text())
            assert any(flow["local_ip"] == "10.200.0.1" and flow["remote_ip"] == "198.51.100.2" and
                       flow["local_server_port"] == 8443 and flow["remote_client_port"] == old_client_port for flow in flow_data), flow_data
            assert not any(flow.get("remote_client_port") == new_client_port for flow in flow_data), flow_data
            (wrappers / "release").touch()
            out, err = apply.communicate(timeout=20)
            if apply.returncode: raise RuntimeError(f"namespace-apply: {err or out}")
            old.sendall(b"old-management")
            assert old.recv(100) == b"old-management"
            new.sendall(b"new-management")
            assert new.recv(100) == b"new-management"
            old.close(); new.close()
            seal = json.loads((directory / "flowseal.json").read_text())
            ack = json.loads((directory / "flow-ack.json").read_text())
            state = json.loads((directory / "state.json").read_text())
            assert seal["transaction_id"] == transaction and seal["manifest_hash"] == hashlib.sha256(payload).hexdigest()
            assert ack["transaction_id"] == transaction and ack["manifest_hash"] == seal["manifest_hash"] and ack["flow_hash"] == seal["flow_hash"]
            assert state["state"] == "pending" and state["flow_hash"] == seal["flow_hash"]
            run("nft", "-f", "-", input="insert rule inet uvg_tproxy uvg_prerouting meta mark & 0xff == 7 counter\n")

            for address, port, prefix in [("203.0.113.7", 18080, b"TPROXY:"), ("10.200.0.53", 53, b"DNS:")]:
                with socket.socket() as client:
                    client.settimeout(2)
                    client.setsockopt(socket.SOL_SOCKET, 36, 7)
                    client.bind(("10.200.0.10", 0))
                    client.connect((address, port))
                    client.sendall(b"tcp")
                    assert client.recv(100) == prefix + b"tcp"
            for address, port, prefix in [("203.0.113.8", 18081, b"TPROXY:"), ("198.51.100.53", 53, b"DNS:")]:
                with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
                    client.settimeout(2)
                    client.bind(("10.200.0.10", 0))
                    client.connect((address, port))
                    client.send(b"udp")
                    assert client.recv(100) == prefix + b"udp"
            with socket.socket() as bypass:
                bypass.settimeout(.25)
                bypass.setsockopt(socket.SOL_SOCKET, 36, 512)
                bypass.bind(("10.200.0.10", 0))
                try:
                    bypass.connect(("203.0.113.9", 18089))
                except OSError: pass
                else: raise AssertionError("SO_MARK bypass перехвачен")
            for mark in [0, 512]:
                with socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as client:
                    client.setsockopt(socket.SOL_SOCKET, 36, mark)
                    try: client.sendto(b"blocked", ("2001:db8:2::7", 443))
                    except OSError as error: assert error.errno in [1, 13], error
                    else: raise AssertionError("Внешний IPv6 не заблокирован")
            counters = json.loads(subprocess.check_output(["nft", "-j", "list", "chain", "inet", "uvg_tproxy", "uvg_prerouting"]))
            assert any(expression.get("counter", {}).get("packets", 0) > 0 for item in counters["nftables"] for expression in item.get("rule", {}).get("expr", []))
            run(helper, "rollback", transaction, "--root", root)
            assert subprocess.check_output(["nft", "-j", "list", "table", "inet", "uvg_foreign"]) == foreign
            rules = json.loads(subprocess.check_output(["ip", "-j", "-4", "rule"]))
            assert not any(item.get("priority") == 10000 for item in rules)
            routes = json.loads(subprocess.check_output(["ip", "-N", "-j", "-4", "route", "show", "table", "200"]))
            assert not routes, f"Rollback оставил маршруты table200: {routes}"
            endpoints = json.loads(subprocess.check_output(["ip", "-N", "-j", "-4", "route", "show", "proto", "186"]))
            assert not endpoints, f"Rollback оставил endpoint route: {endpoints}"
            run(helper, "rollback", transaction, "--root", root)
            print("PASS: armed до мутаций; staged flow ACK с сохранением старого TCP tuple; новый TCP tuple между capture и publish; IPv4 TCP/UDP TPROXY; DNS до LAN; SO_MARK bypass; IPv6 block; rollback")
        finally:
            if 'wrappers' in locals() and wrappers.exists(): (wrappers / "release").touch()
            if 'apply' in locals() and apply.poll() is None: apply.wait(timeout=10)
            if 'old' in locals(): old.close()
            if 'new' in locals(): new.close()
            if watchdog is not None:
                watchdog.terminate()
                try: watchdog.wait(timeout=10)
                except subprocess.TimeoutExpired: watchdog.kill(); watchdog.wait()
            child.terminate()
            child.wait()


if __name__ == "__main__":
    if sys.argv[1] == "server": server(sys.argv[2])
    else: main(sys.argv[1])
