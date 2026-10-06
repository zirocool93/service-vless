# Установка и обновление

Опубликованный release `v0.2.0` рассчитан на Ubuntu Server с systemd на `amd64` и `arm64` и включает gateway, Xray, watchdog, unit-файлы, installer и все `docs/*.md`; Go, Node.js, npm и unzip на сервере не требуются. Свежая установка не активирует Full Tunnel и не перенастраивает маршруты, nftables, DNS или сетевые интерфейсы. Update и uninstall сначала вызывают recovery существующего Full Tunnel, поэтому могут удалять только ранее созданные владельческие сетевые объекты.

## Установка одной командой

Перед выполнением просмотрите публичный `bootstrap.sh`. Для latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/zirocool93/service-vless/main/scripts/bootstrap.sh | sudo bash
```

Для закреплённой версии:

```bash
curl -fsSL https://raw.githubusercontent.com/zirocool93/service-vless/main/scripts/bootstrap.sh | sudo bash -s -- --version v0.2.0
```

Bootstrap обращается только к HTTPS GitHub API и GitHub Releases, выбирает asset `ubuntu-vpn-gateway_<tag>_linux_<arch>.tar.gz`, требует единственную точную запись asset в `SHA256SUMS`, проверяет SHA-256 и безопасные пути архива. Ссылки, device nodes и другие специальные файлы отклоняются. После проверки вызывается вложенный installer.

Для самой первой команды на минимальном образе нужны `curl`, CA certificates, `tar`, `sha256sum` и `awk`. Installer на Ubuntu ставит отсутствующие `curl`, `ca-certificates`, `util-linux`, `nftables` и `iproute2` через apt. Production runtime не загружает Xray отдельно: проверенный бинарник находится внутри release bundle.

## Результат

Устанавливаются:

- gateway: `/usr/local/bin/ubuntu-vpn-gateway`;
- Xray: `/usr/local/lib/ubuntu-vpn-gateway/xray`;
- watchdog helper: `/usr/local/lib/ubuntu-vpn-gateway/uvg-watchdog`;
- updater: `/usr/local/bin/ubuntu-vpn-gateway-update`;
- unit: `/etc/systemd/system/ubuntu-vpn-gateway.service`;
- watchdog template: `/etc/systemd/system/uvg-watchdog@.service`;
- early recovery: `/etc/systemd/system/uvg-network-recovery.service`;
- SQLite и журнал версии: `/var/lib/ubuntu-vpn-gateway`;
- master key и TLS: `/etc/ubuntu-vpn-gateway/secrets`;
- лицензии и документация: `/usr/share/doc/ubuntu-vpn-gateway`.

Основной unit требует завершения early recovery и содержит `ExecStartPre` с повторным `uvg-watchdog recover`, поэтому recovery вызывается до каждого старта backend, включая restart. Backend получает `CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_NET_BIND_SERVICE`; `CAP_SYS_PTRACE` не выдаётся. Recovery до sandbox backend сохраняет root-owned proof host network namespace и boot ID в журнале транзакций. В unit предусмотрен fail-open recovery при запуске; автоматического повторного подключения нет. Перезагрузка отдельно не проходила acceptance.

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
sudo ubuntu-vpn-gateway-update --version v0.2.0
```

Одновременно может выполняться только один install/update. До остановки службы проверяются версии бинарников нового bundle. После остановки создаётся уникальная резервная копия прежних бинарников, SQLite вместе с WAL/SHM, секретов, unit-файла, installer/updater и manifest версии. Файлы заменяются через временное имя и atomic rename.

Новая версия должна запуститься и пройти HTTPS health check. При ошибке, отмене команды или сбое миграции обработчики ошибок и завершения останавливают новый процесс, восстанавливает предыдущие бинарники, БД, sidecar-файлы, ключи, unit и updater, затем запускает прежнюю службу. Обновление никогда не очищает каталоги данных. Первичная неудачная установка также сохраняет созданные БД/ключи для безопасного повторного запуска.

Перезапуск службы во время update завершает текущее активное локальное proxy-соединение. После успешного обновления откройте панель и нажмите «Подключить» для нужного узла снова; автоматическое восстановление runtime-соединения пока не реализовано.

Резервные копии находятся в `/var/lib/ubuntu-vpn-gateway/backups`. Они содержат секреты и доступны только root. Автоматическое удаление backups не выполняется.

VM acceptance локального и публичного update подтвердила обновление при активном Full Tunnel. Recovery прошёл до замены файлов. Публичная команда `ubuntu-vpn-gateway-update` без аргументов установила `v0.2.0`; `version` и `installed-version` совпали, backend был active, API сообщал `disabled`, hashes DNS/master/TLS сохранились. Повторный Apply/Confirm на публичном бинарнике завершился `active` со всеми checks успешными. GitHub CI commit `eafa592` прошёл на Go 1.26/1.27; release workflow `37427945500` опубликовал latest `v0.2.0` с ровно двумя архитектурными архивами и `SHA256SUMS`.

## Формат release bundle

Архив для каждой архитектуры содержит обычные файлы:

```text
ubuntu-vpn-gateway
xray
uvg-watchdog
VERSION
LICENSE
scripts/bootstrap.sh
scripts/install.sh
scripts/update.sh
scripts/uninstall.sh
packaging/systemd/ubuntu-vpn-gateway.service
packaging/systemd/uvg-watchdog@.service
packaging/systemd/uvg-network-recovery.service
docs/*.md
third-party/Xray-LICENSE
third-party/Xray-SOURCE.md
```

В bundle входят все Markdown-документы из `docs/`, не только инструкция по установке.

`VERSION` и команда `ubuntu-vpn-gateway version` обязаны точно совпадать с release tag вида `vX.Y.Z`. `Xray-SOURCE.md` фиксирует официальный tag и исходный URL Xray, соответствующий бинарнику под MPL-2.0.

## Удаление

```bash
sudo /usr/local/lib/ubuntu-vpn-gateway/uninstall.sh
```

По умолчанию бинарники и unit удаляются, а SQLite, секреты и backups сохраняются. Необратимое удаление данных выполняется только явным `--purge-data`; перед ним создайте внешнюю резервную копию SQLite и `master.key`.

## Ограничения

Installer не устанавливает AmneziaWG и сам не активирует Full Tunnel. Он устанавливает независимый watchdog и ранний recovery unit. Перед update/uninstall recovery снимает Full Tunnel до замены helper и остановки backend; основной service unit повторяет recovery перед каждым backend start. VM reboot acceptance подтвердила смену boot ID, запуск backend, API `disabled`, отсутствие policy rule `10000`, table 200 и владельческой nft table, восстановление исходного IP `194.186.91.130` и сохранение DNS/master/TLS hashes. Публичный update из active Full Tunnel и последующий Apply/Confirm на `v0.2.0` также прошли. Автоматического reconnect нет. Активация выполняется только отдельным prepare/apply/confirm из аутентифицированного UI. Подробный контракт находится в `docs/full-tunnel.md`; VM результаты приведены там же.
