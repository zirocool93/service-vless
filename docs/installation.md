# Установка и обновление

Поддерживаются Ubuntu Server с systemd на `amd64` и `arm64`. Release уже содержит gateway, Xray, unit-файл и installer; Go, Node.js, npm и unzip на сервере не требуются. Установка не меняет маршруты, nftables, DNS или сетевые интерфейсы.

## Установка одной командой

Перед выполнением просмотрите публичный `bootstrap.sh`. Для latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/zirocool93/service-vless/main/scripts/bootstrap.sh | sudo bash
```

Для закреплённой версии:

```bash
curl -fsSL https://raw.githubusercontent.com/zirocool93/service-vless/main/scripts/bootstrap.sh | sudo bash -s -- --version v0.1.0
```

Bootstrap обращается только к HTTPS GitHub API и GitHub Releases, выбирает asset `ubuntu-vpn-gateway_<tag>_linux_<arch>.tar.gz`, требует единственную точную запись asset в `SHA256SUMS`, проверяет SHA-256 и безопасные пути архива. Ссылки, device nodes и другие специальные файлы отклоняются. После проверки вызывается вложенный installer.

Для самой первой команды на минимальном образе нужны `curl`, CA certificates, `tar`, `sha256sum` и `awk`. Installer на Ubuntu ставит отсутствующие `curl`, `ca-certificates` и `util-linux` через apt. Production runtime не загружает Xray отдельно: проверенный бинарник находится внутри release bundle.

## Результат

Устанавливаются:

- gateway: `/usr/local/bin/ubuntu-vpn-gateway`;
- Xray: `/usr/local/lib/ubuntu-vpn-gateway/xray`;
- updater: `/usr/local/bin/ubuntu-vpn-gateway-update`;
- unit: `/etc/systemd/system/ubuntu-vpn-gateway.service`;
- SQLite и журнал версии: `/var/lib/ubuntu-vpn-gateway`;
- master key и TLS: `/etc/ubuntu-vpn-gateway/secrets`;
- лицензии и документация: `/usr/share/doc/ubuntu-vpn-gateway`.

Каталоги данных и секретов имеют режим `0700`, секретные файлы — `0600`. При первой инициализации имя администратора и случайный пароль выводятся только в stdout текущего installer. Сохраните пароль сразу. Повторная установка не печатает и не меняет существующие credentials.

После запуска installer проверяет unit и HTTPS endpoint с `/etc/ubuntu-vpn-gateway/secrets/tls.crt`:

```bash
sudo systemctl status ubuntu-vpn-gateway.service
curl --cacert /etc/ubuntu-vpn-gateway/secrets/tls.crt https://localhost:8443/api/v1/health
```

Панель доступна по `https://SERVER-IP:8443`. При доступе с другого компьютера сверяйте fingerprint self-signed сертификата через доверенный канал.

## Обновление

До latest release:

```bash
sudo ubuntu-vpn-gateway-update
```

До конкретной версии:

```bash
sudo ubuntu-vpn-gateway-update --version v0.1.0
```

Одновременно может выполняться только один install/update. До остановки службы проверяются версии бинарников нового bundle. После остановки создаётся уникальная резервная копия прежних бинарников, SQLite вместе с WAL/SHM, секретов, unit-файла, installer/updater и manifest версии. Файлы заменяются через временное имя и atomic rename.

Новая версия должна запуститься и пройти HTTPS health check. При ошибке, отмене команды или сбое миграции ERR trap останавливает новый процесс, восстанавливает предыдущие бинарники, БД, sidecar-файлы, ключи, unit и updater, затем запускает прежнюю службу. Обновление никогда не очищает каталоги данных. Первичная неудачная установка также сохраняет созданные БД/ключи для безопасного повторного запуска.

Перезапуск службы во время update завершает текущее активное локальное proxy-соединение. После успешного обновления откройте панель и нажмите «Подключить» для нужного узла снова; автоматическое восстановление runtime-соединения пока не реализовано.

Резервные копии находятся в `/var/lib/ubuntu-vpn-gateway/backups`. Они содержат секреты и доступны только root. Автоматическое удаление backups не выполняется.

## Формат release bundle

Архив для каждой архитектуры содержит обычные файлы:

```text
ubuntu-vpn-gateway
xray
VERSION
LICENSE
scripts/bootstrap.sh
scripts/install.sh
scripts/update.sh
scripts/uninstall.sh
packaging/systemd/ubuntu-vpn-gateway.service
docs/installation.md
third-party/Xray-LICENSE
third-party/Xray-SOURCE.md
```

`VERSION` и команда `ubuntu-vpn-gateway version` обязаны точно совпадать с release tag вида `vX.Y.Z`. `Xray-SOURCE.md` фиксирует официальный tag и исходный URL Xray, соответствующий бинарнику под MPL-2.0.

## Удаление

```bash
sudo /usr/local/lib/ubuntu-vpn-gateway/uninstall.sh
```

По умолчанию бинарники и unit удаляются, а SQLite, секреты и backups сохраняются. Необратимое удаление данных выполняется только явным `--purge-data`; перед ним создайте внешнюю резервную копию SQLite и `master.key`.

## Ограничения

Installer не устанавливает AmneziaWG и не активирует Full Tunnel. Проверка TPROXY в namespace тестового LXC не является разрешением менять сеть. Такие изменения требуют отдельного одобренного дизайна, независимого watchdog и VM-проверки отката.
