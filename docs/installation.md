# Установка и обновление

Подготовлен installer для Ubuntu с systemd, устанавливающий нативные бинарники без Docker. Проверка bash-синтаксиса прошла; установка, повторная установка и аварийное восстановление после неудачного обновления ещё не прошли интеграционную приёмку. Он не изменяет маршруты, nftables, DNS, модули ядра или конфигурацию AmneziaWG.

## Подготовка

Соберите или получите локальный release-бинарник `ubuntu-vpn-gateway-linux-amd64` либо `arm64`. Проект пока не публикует release URL, поэтому installer намеренно не скачивает gateway из вымышленного источника.

Для Xray укажите существующий тег официального Xray-core и SHA-256 конкретного архива для архитектуры сервера. Контрольную сумму следует получить по доверенному каналу и сверить с официальной публикацией release. Installer загружает архив только с `https://github.com/XTLS/Xray-core/releases/download/…`, проверяет SHA-256 до распаковки и запускает `xray version` до установки.

На сервере нужны `curl`, `unzip`, `sha256sum` и systemd.

## Установка

```bash
sudo ./scripts/install.sh \
  --gateway-binary ./ubuntu-vpn-gateway-linux-amd64 \
  --xray-version vX.Y.Z \
  --xray-sha256 64_HEX_СИМВОЛА
```

При первой установке команда `gateway init` выполняется непосредственно в терминале до запуска systemd. Сгенерированный пароль администратора появляется только в stdout installer и не попадает в journald. Сохраните его сразу: повторная инициализация пароль не показывает и не меняет.

Устанавливаются:

- `/usr/local/bin/ubuntu-vpn-gateway`;
- `/usr/local/lib/ubuntu-vpn-gateway/xray`;
- `/etc/systemd/system/ubuntu-vpn-gateway.service`;
- данные в `/var/lib/ubuntu-vpn-gateway` с режимом `0700`;
- master key и TLS-пара в `/etc/ubuntu-vpn-gateway/secrets` с каталогом `0700` и файлами `0600`.

После старта интерфейс доступен на `https://SERVER-IP:8443`. Сертификат self-signed, поэтому при первом подключении проверьте его fingerprint через доверенный SSH-сеанс. Локальные SOCKS5 и HTTP proxy слушают только loopback.

## Обновление и повторный запуск

`scripts/update.sh` принимает те же параметры и вызывает idempotent installer. Перед заменой существующих бинарников создаётся каталог `/var/lib/ubuntu-vpn-gateway/backups/UTC_TIMESTAMP` с предыдущими бинарниками, SQLite и каталогом секретов. Служба останавливается до копирования и запускается после атомарной замены файлов. Существующая учётная запись администратора сохраняется; `init` повторно не вызывается.

Перед обновлением убедитесь, что на файловой системе достаточно места для полной копии SQLite и секретов. Резервная копия SQLite пригодна только вместе с соответствующим `master.key`.

## Проверка

```bash
sudo systemctl status ubuntu-vpn-gateway.service
curl --cacert /etc/ubuntu-vpn-gateway/secrets/tls.crt https://SERVER-IP:8443/api/v1/health
sudo journalctl -u ubuntu-vpn-gateway.service
```

Пароль администратора, VLESS URI, subscription URL, UUID, ключи и полные конфигурации не должны передаваться в команды журналирования или публиковаться в диагностике.

## Удаление

```bash
sudo ./scripts/uninstall.sh
```

По умолчанию удаляются служба и бинарники, а данные и секреты сохраняются для повторной установки. Полное удаление выполняется только явной командой:

```bash
sudo ./scripts/uninstall.sh --purge-data
```

`--purge-data` необратимо удаляет SQLite, резервные копии, master key и TLS-ключи.

## Ограничение AmneziaWG

Installer не устанавливает и не загружает модули ядра AmneziaWG. На предоставленном LXC нет AWG-модуля и TUN, поэтому AWG runtime здесь не проверен. VLESS Full Tunnel через TPROXY не требует TUN: его применимость зависит от capabilities и сетевого review; изолированная проверка синтаксиса TPROXY уже прошла. Системное туннелирование требует независимого rollback watchdog и испытаний сохранности SSH и web-доступа.
