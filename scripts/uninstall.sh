#!/usr/bin/env bash
set -Eeuo pipefail
PURGE=false
if [[ ${1:-} == "--purge-data" && $# -eq 1 ]]; then PURGE=true
elif (($#)); then echo "Использование: sudo ./scripts/uninstall.sh [--purge-data]" >&2; exit 2
fi
[[ $EUID -eq 0 ]] || { echo "Запустите удаление через sudo." >&2; exit 1; }
systemctl disable --now ubuntu-vpn-gateway.service 2>/dev/null || true
rm -f -- /etc/systemd/system/ubuntu-vpn-gateway.service
systemctl daemon-reload
rm -f -- /usr/local/bin/ubuntu-vpn-gateway /usr/local/lib/ubuntu-vpn-gateway/xray /usr/share/doc/ubuntu-vpn-gateway/installation.md
rmdir /usr/local/lib/ubuntu-vpn-gateway /usr/share/doc/ubuntu-vpn-gateway 2>/dev/null || true
if $PURGE; then
  rm -rf -- /var/lib/ubuntu-vpn-gateway /etc/ubuntu-vpn-gateway
  echo "Приложение и данные удалены без возможности восстановления."
else
  echo "Приложение удалено. Данные сохранены в /var/lib/ubuntu-vpn-gateway и /etc/ubuntu-vpn-gateway."
fi
