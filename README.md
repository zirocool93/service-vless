# Ubuntu VPN Gateway

Панель управления исходящими VPN-соединениями Ubuntu Server. Реализованы HTTPS, вход администратора, SQLite с шифрованием секретов, импорт VLESS и подписок, локальные SOCKS5/HTTP-прокси и host Full Tunnel для IPv4 TCP/UDP через Xray. Full Tunnel использует Safe Apply, DNS через VLESS, блокировку внешнего IPv6 и отдельный постоянный fail-open watchdog.

## Установка на Ubuntu

Выполните на сервере с systemd от пользователя с sudo:

```bash
curl -fsSL https://raw.githubusercontent.com/zirocool93/service-vless/main/scripts/bootstrap.sh | sudo bash
```

Скрипт загружает последний опубликованный релиз для amd64 или arm64, проверяет SHA-256 и устанавливает готовые gateway/Xray и службу systemd. Go и Node.js на сервере не нужны. При первой установке сохраните пароль администратора, выведенный в терминал. Панель доступна по `https://IP-СЕРВЕРА:8443`; используется локальный сертификат, fingerprint которого следует сверить через SSH. Подробности: [установка и обновление](docs/installation.md).

## Последующее обновление

```bash
sudo ubuntu-vpn-gateway-update
```

Перед обновлением сохраняются прежние бинарники, база, ключи и unit. Учетная запись и узлы сохраняются. Если новая версия не запускается или HTTPS health не проходит, установщик восстанавливает предыдущую установку. Версию можно проверить командой `ubuntu-vpn-gateway version`.

Обычный режим обслуживает приложения через SOCKS5 `127.0.0.1:1080` или HTTP `127.0.0.1:8080` без изменения системного маршрута. Отдельный раздел Full Tunnel применяет план с 120-секундным подтверждением той же web-сессии и запускает точечный rollback при сбое; неполная очистка остаётся `rollback_failed`. Staged-кандидат прошёл namespace-проверку полного 4-tuple и нового SSH между capture/publish, а также VM-проверки сохранения исходной Apply-ответной сессии и 12 новых SSH-сессий во время Apply. Подтверждены VLESS exit IPv4 `89.125.93.116`, DNS A/AAAA UDP/TCP через LAN и публичный resolver без WAN DNS, UDP NTP, rollback при гибели активных Xray/watchdog/backend и восстановление direct-доступа после restart.

Повторный staged lease 120 секунд откатился с пустыми table 200, доступными SSH/API и direct-сетью. Перезагрузка Ubuntu LXC подтвердила смену boot ID, запуск backend, API `disabled`, отсутствие policy rule `10000`, table 200 и владельческой nft table; вернулся исходный IP `194.186.91.130`, hashes DNS/master/TLS не изменились. Штатный локальный installer также обновил систему с активным Full Tunnel: recovery завершился до замены файлов, после установки backend был healthy, туннель выключен, direct-доступ и hashes сохранены. Проверка публичного GitHub release/update для этого кандидата ещё ожидает публикации и проверки. Docker routing, kill switch и подключение AmneziaWG не поддерживаются; AWG-конфигурацию можно только импортировать. Подробнее: [Full Tunnel](docs/full-tunnel.md), [тестовая среда](docs/test-environment.md), [серверная часть](docs/core-web.md), [интерфейс](docs/ui.md).

Начало работы: [разработка](docs/development.md), [установка и текущее состояние](docs/install.md), [безопасность](docs/security.md), [сеть](docs/networking.md), [план](docs/implementation-plan.md), [архитектура](docs/architecture.md), [карта репозитория](docs/repository-map.md), [журнал](docs/work-log.md).

Лицензия: [MIT](LICENSE).
