# Ядро web-приложения и безопасность

## Запуск

Production-каталоги по умолчанию:

- данные и SQLite: `/var/lib/ubuntu-vpn-gateway`;
- ключи и TLS: `/etc/ubuntu-vpn-gateway/secrets`;
- Xray: `/usr/local/bin/xray`;
- web-интерфейс: `https://SERVER-IP:8443`.

Первичная инициализация выполняется отдельно от systemd-службы:

```bash
sudo ubuntu-vpn-gateway init
```

Команда создаёт администратора `admin` и один раз печатает сгенерированный пароль в stdout с русскими метками. Повторный `init` не меняет пароль. `serve` не создаёт администратора и отказывается запускаться до `init`, поэтому пароль не попадает в journald.

```bash
sudo ubuntu-vpn-gateway serve
```

Параметры `--data-dir`, `--secrets-dir`, `--xray-bin`, `--socks-port`, `--http-port`, `--listen` и `--max-concurrent` задаются только при запуске процесса и не доступны через пользовательский API. `--dev-http` разрешён лишь вместе с loopback-адресом, например `--listen 127.0.0.1:8443`; этот режим предназначен для локальной разработки.

## TLS и секреты

При первом production-запуске создаётся self-signed ECDSA-сертификат с SAN для localhost и IP-адресов сетевых интерфейсов. Закрытый ключ и сертификат имеют права `0600`, каталог секретов — `0700`; TLS ниже 1.2 отключён. Если существует только один файл TLS-пары, запуск прекращается с ошибкой вместо неявной замены.

Master key AES-256-GCM хранится отдельно от SQLite с правами `0600`. Секретная часть каждой записи шифруется с AAD `kind + NUL + id`, поэтому ciphertext нельзя незаметно перенести в другую строку. Если SQLite существует, а `master.key` утрачен, приложение требует восстановить ключ из резервной копии и не создаёт новый. Резервная копия должна включать SQLite и соответствующие `master.key`, `tls.crt`, `tls.key`.

Текущая миграция `user_version=1` использует таблицы `users`, `sessions` и универсальные зашифрованные `records`. Это сознательное упрощение текущей фазы; отдельные нормализованные таблицы из ранней архитектурной схемы пока не реализованы.

## Аутентификация и API

Пароли хешируются Argon2id (`64 MiB`, три прохода, четыре потока). Сессионный cookie `gateway_session` содержит случайный токен; в SQLite хранится только SHA-256 токена. Cookie имеет `HttpOnly`, `Secure` в production, `SameSite=Strict` и область `/api/v1`. Сессия действует 12 часов.

- `POST /api/v1/auth/login` — JSON `{username,password}`;
- `GET /api/v1/auth/session` — `{authenticated,user,csrfToken}`;
- `POST /api/v1/auth/logout` — требует заголовок `X-CSRF-Token`.

Login принимает ровно один JSON-объект без неизвестных полей и trailing-данных; пароль ограничен 1024 байтами. Origin проверяется до Argon2. Ограничитель попыток общий для IP независимо от имени пользователя, резервирует попытку до дорогой проверки и допускает не более четырёх одновременных Argon2. Для неизвестного имени также выполняется Argon2 с фиктивным хешем.

Публичен только `GET /api/v1/health` и auth bootstrap API. Остальные `/api/` требуют действующую сессию; изменяющие запросы также требуют `X-CSRF-Token` и same-origin. Аутентифицированный SSE доступен по `GET /api/v1/events`, отправляет heartbeat и события приложения. Число одновременных SSE-подключений ограничено 64.

## VLESS и Full Tunnel

Локальные SOCKS5/HTTP proxy привязаны к loopback. Host Full Tunnel для IPv4 TCP/UDP управляется через `/api/v1/tunnel/status`, `prepare`, `apply`, `confirm` и `disable`. Все маршруты требуют сессию, а мутации — CSRF. Одноразовый token связан с transaction ID, hash плана, cookie-сессией и management peer IP; pending deadline составляет 120 секунд.

Отдельный Full Tunnel Xray обслуживает transparent и DNS inbound, а обычный proxy Xray продолжает работать независимо. Backend и дочерний Xray получают `CAP_NET_ADMIN`, `CAP_NET_RAW` и необходимую для transparent bind `CAP_NET_BIND_SERVICE`, но не `CAP_SYS_PTRACE`. Применение после durable armed ACK проходит состояния `tracking → sealing → sealed → pending`: сначала публикуются только постоянные conntrack hooks, затем `ss -Hnt state all` выбирает установленные и closing SSH/UI flows, write-once `flows.json` и seal получают независимый watchdog ACK, и только после этого ставятся routes/rules и атомарно включается intercept. Seal контролируется и после commit.

Watchdog остаётся жив после commit и запускает rollback при завершении backend, Full Tunnel Xray или самого watchdog. Early recovery unit выполняется до backend на boot; основной systemd unit также вызывает recovery в `ExecStartPre` перед каждым стартом/restart. Reboot test staged-кандидата подтвердил работу этого пути. Rollback снимает владельческую nft table; при неудаче очищает обе intercept chains и удаляет route/rule и Xray только после подтверждения выключенного intercept. При неподтверждённой очистке сохраняет зависимости и записывает `rollback_failed`.

Если Manager запущен без in-memory plan, status делает read-only scan durable-журналов и возвращает последнюю незавершённую транзакцию вместо ложного `disabled`. Невалидный manifest оставляет `plan` пустым, но transaction ID и `rollback_failed` видимы; status выставляет `available=false`, и UI блокирует prepare. `disable` в production Linux от root повторяет recovery и возвращает ошибку, если очистку подтвердить не удалось. Внешний IPv6 блокируется; Docker routing, kill switch и AWG runtime не реализованы.

Staged acceptance: namespace-проверка сохранила старый management 4-tuple и новый TCP flow между capture/publish, DNS-before-LAN, mark bypass, IPv6 drop и rollback. На VM исходный Apply HTTP-ответ вернулся; pending перешёл в active после confirm. Старый SSH сохранял heartbeat, 12 новых SSH подключений во время Apply работали. Подтверждены VPN exit `89.125.93.116`, DNS A/AAAA UDP/TCP через LAN и public resolver, 0 WAN-пакетов port 53, UDP NTP reply 48 bytes, rollback при гибели активных Xray/watchdog/backend и успешный backend restart с recovery/health и direct-доступом. Повторный lease 120 секунд завершил rollback с SSH/API и direct-доступом. Ubuntu LXC reboot из active-состояния сменил boot ID, вернул API `disabled`, не оставил policy rule `10000`, table 200 или владельческую nft table, восстановил direct IPv4 `194.186.91.130` и сохранил DNS/master/TLS hashes. Локальный installer обновил active Full Tunnel после recovery до замены файлов; затем backend был healthy, туннель выключен, direct и hashes сохранены. GitHub CI commit `eafa592` прошёл на Go 1.26/1.27; release workflow `37427945500` опубликовал `v0.2.0` с amd64/arm64 архивами и `SHA256SUMS`. Публичный updater без аргументов обновил active Full Tunnel VM до `v0.2.0`, сохранив DNS/master/TLS hashes; backend active, API `disabled`. Повторный Apply/Confirm на публичном бинарнике завершился `active` со всеми checks успешными. Детали: `docs/full-tunnel.md` и `docs/networking-design.md`.
