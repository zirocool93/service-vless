#!/usr/bin/env bash
set -Eeuo pipefail

APP_BINARY=""
XRAY_VERSION=""
XRAY_SHA256=""
UNIT_SOURCE=""
DOC_SOURCE=""

usage() {
  cat <<'EOF'
Использование: sudo ./scripts/install.sh \
  --gateway-binary ./ubuntu-vpn-gateway-linux-amd64 \
  --xray-version VERSION --xray-sha256 SHA256 \
  [--unit ./packaging/systemd/ubuntu-vpn-gateway.service] \
  [--documentation ./docs/installation.md]

Бинарник gateway должен быть локальным release-артефактом. Xray загружается
с официальной страницы GitHub Releases и обязательно проверяется по SHA-256.
EOF
}

while (($#)); do
  case "$1" in
    --gateway-binary) APP_BINARY=${2:?}; shift 2 ;;
    --xray-version) XRAY_VERSION=${2:?}; shift 2 ;;
    --xray-sha256) XRAY_SHA256=${2:?}; shift 2 ;;
    --unit) UNIT_SOURCE=${2:?}; shift 2 ;;
    --documentation) DOC_SOURCE=${2:?}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Неизвестный параметр: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "Запустите installer через sudo." >&2; exit 1; }
[[ -n $APP_BINARY && -f $APP_BINARY ]] || { echo "Укажите существующий --gateway-binary." >&2; exit 1; }
[[ -n $XRAY_VERSION && $XRAY_VERSION =~ ^v?[0-9][0-9A-Za-z._-]*$ ]] || { echo "Некорректный --xray-version." >&2; exit 1; }
[[ $XRAY_SHA256 =~ ^[[:xdigit:]]{64}$ ]] || { echo "--xray-sha256 должен содержать 64 hex-символа." >&2; exit 1; }
command -v systemctl >/dev/null || { echo "systemd не найден." >&2; exit 1; }
command -v curl >/dev/null || { echo "curl не найден." >&2; exit 1; }
command -v unzip >/dev/null || { echo "unzip не найден; установите пакет unzip." >&2; exit 1; }
command -v sha256sum >/dev/null || { echo "sha256sum не найден." >&2; exit 1; }

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)
[[ -n $UNIT_SOURCE ]] || UNIT_SOURCE="$PROJECT_DIR/packaging/systemd/ubuntu-vpn-gateway.service"
[[ -n $DOC_SOURCE ]] || DOC_SOURCE="$PROJECT_DIR/docs/installation.md"
[[ -f $UNIT_SOURCE && -f $DOC_SOURCE ]] || { echo "Не найдены unit или документация." >&2; exit 1; }

case "$(uname -m)" in
  x86_64|amd64) XRAY_ASSET="Xray-linux-64.zip" ;;
  aarch64|arm64) XRAY_ASSET="Xray-linux-arm64-v8a.zip" ;;
  *) echo "Архитектура $(uname -m) пока не поддерживается installer." >&2; exit 1 ;;
esac
XRAY_TAG=$XRAY_VERSION
[[ $XRAY_TAG == v* ]] || XRAY_TAG="v$XRAY_TAG"
XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_TAG}/${XRAY_ASSET}"

TMP_DIR=$(mktemp -d)
cleanup() { rm -rf -- "$TMP_DIR"; }
trap cleanup EXIT
curl --fail --location --proto '=https' --tlsv1.2 --output "$TMP_DIR/xray.zip" "$XRAY_URL"
printf '%s  %s\n' "${XRAY_SHA256,,}" "$TMP_DIR/xray.zip" | sha256sum --check --status || { echo "SHA-256 архива Xray не совпал." >&2; exit 1; }
unzip -q "$TMP_DIR/xray.zip" xray -d "$TMP_DIR/xray"
chmod 0755 "$TMP_DIR/xray/xray"
"$TMP_DIR/xray/xray" version >/dev/null
chmod 0755 "$APP_BINARY"
"$APP_BINARY" --help >/dev/null 2>&1 || true

install -d -m 0700 /var/lib/ubuntu-vpn-gateway /etc/ubuntu-vpn-gateway /etc/ubuntu-vpn-gateway/secrets
install -d -m 0755 /usr/local/lib/ubuntu-vpn-gateway /usr/share/doc/ubuntu-vpn-gateway

if systemctl is-active --quiet ubuntu-vpn-gateway.service; then systemctl stop ubuntu-vpn-gateway.service; fi
if [[ -e /usr/local/bin/ubuntu-vpn-gateway || -e /usr/local/lib/ubuntu-vpn-gateway/xray ]]; then
  BACKUP_DIR="/var/lib/ubuntu-vpn-gateway/backups/$(date -u +%Y%m%dT%H%M%SZ)"
  install -d -m 0700 "$BACKUP_DIR"
  [[ -f /usr/local/bin/ubuntu-vpn-gateway ]] && cp -a /usr/local/bin/ubuntu-vpn-gateway "$BACKUP_DIR/"
  [[ -f /usr/local/lib/ubuntu-vpn-gateway/xray ]] && cp -a /usr/local/lib/ubuntu-vpn-gateway/xray "$BACKUP_DIR/"
  [[ -f /var/lib/ubuntu-vpn-gateway/gateway.sqlite ]] && cp -a /var/lib/ubuntu-vpn-gateway/gateway.sqlite "$BACKUP_DIR/"
  [[ -d /etc/ubuntu-vpn-gateway/secrets ]] && cp -a /etc/ubuntu-vpn-gateway/secrets "$BACKUP_DIR/"
fi

install -m 0755 "$APP_BINARY" /usr/local/bin/ubuntu-vpn-gateway.new
mv -f /usr/local/bin/ubuntu-vpn-gateway.new /usr/local/bin/ubuntu-vpn-gateway
install -m 0755 "$TMP_DIR/xray/xray" /usr/local/lib/ubuntu-vpn-gateway/xray.new
mv -f /usr/local/lib/ubuntu-vpn-gateway/xray.new /usr/local/lib/ubuntu-vpn-gateway/xray
install -m 0644 "$UNIT_SOURCE" /etc/systemd/system/ubuntu-vpn-gateway.service
install -m 0644 "$DOC_SOURCE" /usr/share/doc/ubuntu-vpn-gateway/installation.md
chmod 0700 /var/lib/ubuntu-vpn-gateway /etc/ubuntu-vpn-gateway /etc/ubuntu-vpn-gateway/secrets
find /etc/ubuntu-vpn-gateway/secrets -maxdepth 1 -type f -exec chmod 0600 {} +

systemctl daemon-reload
if [[ ! -f /var/lib/ubuntu-vpn-gateway/gateway.sqlite ]]; then
  echo "Создаётся учётная запись администратора. Сохраните пароль сейчас:"
  /usr/local/bin/ubuntu-vpn-gateway init --xray-bin /usr/local/lib/ubuntu-vpn-gateway/xray
fi
systemctl enable --now ubuntu-vpn-gateway.service
echo "Ubuntu VPN Gateway установлен: https://SERVER-IP:8443"
