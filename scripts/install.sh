#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

BUNDLE_DIR=""
RELEASE_VERSION=""
REPOSITORY="zirocool93/service-vless"
APP_ROOT=/usr/local/lib/ubuntu-vpn-gateway
APP_BIN=/usr/local/bin/ubuntu-vpn-gateway
XRAY_BIN=$APP_ROOT/xray
DATA_DIR=/var/lib/ubuntu-vpn-gateway
SECRETS_DIR=/etc/ubuntu-vpn-gateway/secrets
UNIT_PATH=/etc/systemd/system/ubuntu-vpn-gateway.service
SERVICE=ubuntu-vpn-gateway.service
BACKUP_DIR=""
HAD_INSTALL=false
MUTATED=false
COMMITTED=false
STOPPED_OLD=false
ACTIVE_BEFORE=false
HAD_DB=false
HAD_UNIT=false
ENABLED_BEFORE=false
HAD_APP_ROOT=false
HAD_DOC_ROOT=false
HAD_UPDATER=false

usage() {
  echo "Использование: install.sh --bundle-dir DIR --version vX.Y.Z [--repository OWNER/REPO]"
}

while (($#)); do
  case "$1" in
    --bundle-dir) BUNDLE_DIR=${2:?}; shift 2 ;;
    --version) RELEASE_VERSION=${2:?}; shift 2 ;;
    --repository) REPOSITORY=${2:?}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Неизвестный параметр: $1" >&2; exit 2 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "Установку необходимо запускать от root." >&2; exit 1; }
[[ $RELEASE_VERSION =~ ^v[0-9][0-9A-Za-z._-]*$ ]] || { echo "Некорректная версия release." >&2; exit 1; }
[[ $REPOSITORY =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Некорректное имя репозитория." >&2; exit 1; }
BUNDLE_DIR=$(cd -- "${BUNDLE_DIR:?Не указан --bundle-dir}" && pwd -P)

required=(ubuntu-vpn-gateway xray uvg-watchdog scripts/bootstrap.sh scripts/install.sh scripts/update.sh scripts/uninstall.sh packaging/systemd/ubuntu-vpn-gateway.service packaging/systemd/uvg-watchdog@.service packaging/systemd/uvg-network-recovery.service docs/installation.md LICENSE VERSION third-party/Xray-LICENSE third-party/Xray-SOURCE.md)
for item in "${required[@]}"; do
  [[ -f "$BUNDLE_DIR/$item" && ! -L "$BUNDLE_DIR/$item" ]] || { echo "Bundle повреждён: отсутствует $item." >&2; exit 1; }
done
[[ $(<"$BUNDLE_DIR/VERSION") == "$RELEASE_VERSION" ]] || { echo "VERSION внутри bundle не совпадает с release." >&2; exit 1; }

if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  source /etc/os-release
else
  echo "Не удалось определить операционную систему." >&2
  exit 1
fi
[[ ${ID:-} == ubuntu ]] || { echo "Автоматическая установка поддерживает Ubuntu Server." >&2; exit 1; }
command -v apt-get >/dev/null || { echo "apt-get не найден." >&2; exit 1; }
missing=()
command -v curl >/dev/null || missing+=(curl)
command -v flock >/dev/null || missing+=(util-linux)
command -v nft >/dev/null || missing+=(nftables)
command -v ip >/dev/null || missing+=(iproute2)
[[ -r /etc/ssl/certs/ca-certificates.crt ]] || missing+=(ca-certificates)
if ((${#missing[@]})); then
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${missing[@]}"
fi
command -v systemctl >/dev/null || { echo "systemd не найден." >&2; exit 1; }
command -v curl >/dev/null || { echo "curl не установлен." >&2; exit 1; }

"$BUNDLE_DIR/ubuntu-vpn-gateway" version | grep -Fx -- "$RELEASE_VERSION" >/dev/null || { echo "Версия gateway внутри bundle не совпадает." >&2; exit 1; }
"$BUNDLE_DIR/uvg-watchdog" version | grep -Fx -- "$RELEASE_VERSION" >/dev/null || { echo "Версия watchdog внутри bundle не совпадает." >&2; exit 1; }
"$BUNDLE_DIR/xray" version >/dev/null || { echo "Бинарник Xray внутри bundle не запускается." >&2; exit 1; }

[[ -d $APP_ROOT ]] && HAD_APP_ROOT=true
[[ -d /usr/share/doc/ubuntu-vpn-gateway ]] && HAD_DOC_ROOT=true
[[ -f /usr/local/bin/ubuntu-vpn-gateway-update ]] && HAD_UPDATER=true
[[ -f $UNIT_PATH ]] && HAD_UNIT=true
if [[ ! -f $APP_BIN ]] && { $HAD_APP_ROOT || $HAD_DOC_ROOT || $HAD_UPDATER || $HAD_UNIT; }; then
  echo "Обнаружена неполная прежняя установка без основного бинарника; существующие файлы оставлены без изменения." >&2
  exit 1
fi

install -d -m 0700 "$DATA_DIR" /etc/ubuntu-vpn-gateway "$SECRETS_DIR"
install -d -m 0755 "$APP_ROOT" /usr/share/doc/ubuntu-vpn-gateway
exec 9>/run/lock/ubuntu-vpn-gateway-install.lock
flock -n 9 || { echo "Установка или обновление уже выполняется." >&2; exit 1; }

rollback() {
  local exit_code=${1:-1}
  local failures=()
  trap - ERR EXIT INT TERM
  set +e
  if { $MUTATED || $STOPPED_OLD; } && ! $COMMITTED; then
    echo "Обновление не прошло проверку; восстанавливается предыдущая версия." >&2
    systemctl stop "$SERVICE" >/dev/null 2>&1 || failures+=("не удалось остановить новую службу")
    if $MUTATED && $HAD_INSTALL && [[ -n $BACKUP_DIR ]]; then
      install -m 0755 "$BACKUP_DIR/ubuntu-vpn-gateway" "$APP_BIN" || failures+=("не восстановлен gateway")
      if [[ -f $BACKUP_DIR/xray ]]; then install -m 0755 "$BACKUP_DIR/xray" "$XRAY_BIN" || failures+=("не восстановлен Xray"); fi
      if [[ -f $BACKUP_DIR/ubuntu-vpn-gateway.service ]]; then install -m 0644 "$BACKUP_DIR/ubuntu-vpn-gateway.service" "$UNIT_PATH" || failures+=("не восстановлен unit"); fi
      if [[ -f $BACKUP_DIR/gateway.sqlite ]]; then install -m 0600 "$BACKUP_DIR/gateway.sqlite" "$DATA_DIR/gateway.sqlite" || failures+=("не восстановлена SQLite"); fi
      if [[ -f $BACKUP_DIR/gateway.sqlite-wal ]]; then install -m 0600 "$BACKUP_DIR/gateway.sqlite-wal" "$DATA_DIR/gateway.sqlite-wal" || failures+=("не восстановлен SQLite WAL"); else rm -f -- "$DATA_DIR/gateway.sqlite-wal" || failures+=("не удалён новый WAL"); fi
      if [[ -f $BACKUP_DIR/gateway.sqlite-shm ]]; then install -m 0600 "$BACKUP_DIR/gateway.sqlite-shm" "$DATA_DIR/gateway.sqlite-shm" || failures+=("не восстановлен SQLite SHM"); else rm -f -- "$DATA_DIR/gateway.sqlite-shm" || failures+=("не удалён новый SHM"); fi
      if ! $HAD_DB; then rm -f -- "$DATA_DIR/gateway.sqlite" "$DATA_DIR/gateway.sqlite-wal" "$DATA_DIR/gateway.sqlite-shm" || failures+=("не удалена новая БД"); fi
      if [[ -d $BACKUP_DIR/secrets ]]; then
        rm -rf -- "$SECRETS_DIR" || failures+=("не очищены новые secrets")
        install -d -m 0700 "$SECRETS_DIR" || failures+=("не создан каталог secrets")
        cp -a -- "$BACKUP_DIR/secrets/." "$SECRETS_DIR/" || failures+=("не восстановлены secrets")
      fi
      if [[ -f $BACKUP_DIR/installed-version ]]; then install -m 0600 "$BACKUP_DIR/installed-version" "$DATA_DIR/installed-version" || failures+=("не восстановлен manifest версии"); fi
      if [[ -d $BACKUP_DIR/app-root ]]; then
        rm -rf -- "$APP_ROOT" || failures+=("не очищен новый app-root")
        cp -a -- "$BACKUP_DIR/app-root" "$APP_ROOT" || failures+=("не восстановлены installer-файлы")
      fi
      if [[ -d $BACKUP_DIR/doc ]]; then
        rm -rf -- /usr/share/doc/ubuntu-vpn-gateway || failures+=("не очищена новая документация")
        cp -a -- "$BACKUP_DIR/doc" /usr/share/doc/ubuntu-vpn-gateway || failures+=("не восстановлена документация")
      fi
      if [[ -f $BACKUP_DIR/ubuntu-vpn-gateway-update ]]; then
        install -m 0755 "$BACKUP_DIR/ubuntu-vpn-gateway-update" /usr/local/bin/ubuntu-vpn-gateway-update || failures+=("не восстановлен updater")
      else
        rm -f -- /usr/local/bin/ubuntu-vpn-gateway-update || failures+=("не удалён новый updater")
      fi
      if ! $HAD_UNIT; then rm -f -- "$UNIT_PATH" || failures+=("не удалён новый unit"); fi
      for unit in uvg-watchdog@.service uvg-network-recovery.service; do
        if [[ -f $BACKUP_DIR/$unit ]]; then install -m 0644 "$BACKUP_DIR/$unit" "/etc/systemd/system/$unit" || failures+=("не восстановлен $unit")
        else rm -f -- "/etc/systemd/system/$unit"; fi
      done
      systemctl stop uvg-network-recovery.service >/dev/null 2>&1 || true
      systemctl daemon-reload || failures+=("daemon-reload завершился ошибкой")
      if $ENABLED_BEFORE; then systemctl enable "$SERVICE" >/dev/null 2>&1 || failures+=("не восстановлен enabled-state"); else systemctl disable "$SERVICE" >/dev/null 2>&1 || failures+=("не восстановлен disabled-state"); fi
      if $ACTIVE_BEFORE; then systemctl start "$SERVICE" || failures+=("прежняя служба не запустилась"); fi
    elif $STOPPED_OLD && $ACTIVE_BEFORE; then
      systemctl start "$SERVICE" || failures+=("прежняя служба не запустилась после прерванного backup")
    elif $MUTATED && ! $HAD_INSTALL; then
      systemctl disable "$SERVICE" >/dev/null 2>&1 || true
      rm -f -- "$UNIT_PATH" "$UNIT_PATH.new" "$APP_BIN" "$APP_BIN.new" /usr/local/bin/ubuntu-vpn-gateway-update /usr/local/bin/ubuntu-vpn-gateway-update.new || failures+=("не удалены файлы неудачной первичной установки")
      systemctl stop uvg-network-recovery.service >/dev/null 2>&1 || true
      rm -f -- /etc/systemd/system/uvg-watchdog@.service /etc/systemd/system/uvg-network-recovery.service
      rm -rf -- "$APP_ROOT" /usr/share/doc/ubuntu-vpn-gateway || failures+=("не удалены каталоги неудачной первичной установки")
      systemctl daemon-reload || failures+=("daemon-reload после очистки завершился ошибкой")
      echo "Созданные данные и secrets сохранены для безопасного повторного запуска." >&2
    fi
    if ((${#failures[@]})); then
      printf 'Rollback завершён с ошибками: %s\n' "$(IFS='; '; echo "${failures[*]}")" >&2
    else
      echo "Rollback завершён успешно." >&2
    fi
  fi
  exit "$exit_code"
}
trap 'rollback $?' ERR
trap 'rollback $?' EXIT
trap 'rollback 130' INT
trap 'rollback 143' TERM

if [[ -f $APP_BIN ]]; then
  HAD_INSTALL=true
  # До замены helper и остановки backend необходимо снять Full Tunnel.
  if [[ -x $APP_ROOT/uvg-watchdog ]]; then "$APP_ROOT/uvg-watchdog" recover; fi
  install -d -m 0700 "$DATA_DIR/backups"
  BACKUP_DIR=$(mktemp -d "$DATA_DIR/backups/$(date -u +%Y%m%dT%H%M%SZ)-${RELEASE_VERSION}.XXXXXXXX")
  if systemctl is-enabled --quiet "$SERVICE"; then ENABLED_BEFORE=true; fi
  if systemctl is-active --quiet "$SERVICE"; then
    ACTIVE_BEFORE=true
    STOPPED_OLD=true
    systemctl stop "$SERVICE"
  fi
  cp -a -- "$APP_BIN" "$BACKUP_DIR/ubuntu-vpn-gateway"
  for unit in uvg-watchdog@.service uvg-network-recovery.service; do
    [[ ! -f /etc/systemd/system/$unit ]] || cp -a -- "/etc/systemd/system/$unit" "$BACKUP_DIR/$unit"
  done
  systemctl stop uvg-network-recovery.service >/dev/null 2>&1 || true
  [[ -f $XRAY_BIN ]] && cp -a -- "$XRAY_BIN" "$BACKUP_DIR/xray"
  [[ -f $UNIT_PATH ]] && cp -a -- "$UNIT_PATH" "$BACKUP_DIR/ubuntu-vpn-gateway.service"
  for db_file in gateway.sqlite gateway.sqlite-wal gateway.sqlite-shm; do
    [[ -f $DATA_DIR/$db_file ]] && cp -a -- "$DATA_DIR/$db_file" "$BACKUP_DIR/$db_file"
  done
  [[ -f $DATA_DIR/gateway.sqlite ]] && HAD_DB=true
  [[ -f $UNIT_PATH ]] && HAD_UNIT=true
  [[ -d $SECRETS_DIR ]] && cp -a -- "$SECRETS_DIR" "$BACKUP_DIR/secrets"
  [[ -f $DATA_DIR/installed-version ]] && cp -a -- "$DATA_DIR/installed-version" "$BACKUP_DIR/installed-version"
  cp -a -- "$APP_ROOT" "$BACKUP_DIR/app-root"
  [[ -d /usr/share/doc/ubuntu-vpn-gateway ]] && cp -a -- /usr/share/doc/ubuntu-vpn-gateway "$BACKUP_DIR/doc"
  [[ -f /usr/local/bin/ubuntu-vpn-gateway-update ]] && cp -a -- /usr/local/bin/ubuntu-vpn-gateway-update "$BACKUP_DIR/ubuntu-vpn-gateway-update"
else
  systemctl stop "$SERVICE" 2>/dev/null || true
fi

MUTATED=true
install -m 0755 "$BUNDLE_DIR/ubuntu-vpn-gateway" "$APP_BIN.new"
mv -f -- "$APP_BIN.new" "$APP_BIN"
install -m 0755 "$BUNDLE_DIR/xray" "$XRAY_BIN.new"
mv -f -- "$XRAY_BIN.new" "$XRAY_BIN"
install -m 0755 "$BUNDLE_DIR/uvg-watchdog" "$APP_ROOT/uvg-watchdog.new"
mv -f -- "$APP_ROOT/uvg-watchdog.new" "$APP_ROOT/uvg-watchdog"
for unit in uvg-watchdog@.service uvg-network-recovery.service; do
  install -m 0644 "$BUNDLE_DIR/packaging/systemd/$unit" "/etc/systemd/system/$unit.new"
  mv -f -- "/etc/systemd/system/$unit.new" "/etc/systemd/system/$unit"
done
install -m 0644 "$BUNDLE_DIR/packaging/systemd/ubuntu-vpn-gateway.service" "$UNIT_PATH.new"
mv -f -- "$UNIT_PATH.new" "$UNIT_PATH"
install -m 0644 "$BUNDLE_DIR/docs/installation.md" /usr/share/doc/ubuntu-vpn-gateway/installation.md
install -m 0644 "$BUNDLE_DIR/LICENSE" /usr/share/doc/ubuntu-vpn-gateway/LICENSE
if [[ -d $BUNDLE_DIR/third-party ]]; then
  install -d -m 0755 /usr/share/doc/ubuntu-vpn-gateway/third-party
  find "$BUNDLE_DIR/third-party" -maxdepth 1 -type f -exec install -m 0644 {} /usr/share/doc/ubuntu-vpn-gateway/third-party/ \;
fi
for script in bootstrap.sh install.sh update.sh uninstall.sh; do
  install -m 0755 "$BUNDLE_DIR/scripts/$script" "$APP_ROOT/$script"
done
cat > /usr/local/bin/ubuntu-vpn-gateway-update.new <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail
exec "$APP_ROOT/update.sh" --repository "$REPOSITORY" "\$@"
EOF
chmod 0755 /usr/local/bin/ubuntu-vpn-gateway-update.new
mv -f -- /usr/local/bin/ubuntu-vpn-gateway-update.new /usr/local/bin/ubuntu-vpn-gateway-update

"$APP_BIN" version | grep -Fx -- "$RELEASE_VERSION" >/dev/null
"$XRAY_BIN" version >/dev/null
systemctl daemon-reload

# Повторный запуск init безопасно различает уже созданного администратора и
# незавершённую первичную установку. Пароль выводится только в текущий stdout.
if init_output=$("$APP_BIN" init --xray-bin "$XRAY_BIN" 2>&1); then
  printf '%s\n' "$init_output"
elif [[ $init_output != *"администратор уже инициализирован"* ]]; then
  echo "Не удалось инициализировать администратора." >&2
  false
fi
unset init_output

systemctl enable "$SERVICE" >/dev/null
systemctl start "$SERVICE"
health_ok=false
for _ in $(seq 1 30); do
  if systemctl is-active --quiet "$SERVICE" && [[ -f $SECRETS_DIR/tls.crt ]] && \
    curl --fail --silent --cacert "$SECRETS_DIR/tls.crt" --connect-timeout 2 --max-time 3 https://localhost:8443/api/v1/health >/dev/null; then
    health_ok=true
    break
  fi
  sleep 1
done
$health_ok || { echo "Новая версия не прошла HTTPS health check." >&2; false; }

printf '%s\n' "$RELEASE_VERSION" > "$DATA_DIR/installed-version.new"
chmod 0600 "$DATA_DIR/installed-version.new"
mv -f -- "$DATA_DIR/installed-version.new" "$DATA_DIR/installed-version"
COMMITTED=true
trap - ERR EXIT INT TERM
echo "Ubuntu VPN Gateway $RELEASE_VERSION установлен. Web UI: https://SERVER-IP:8443"
