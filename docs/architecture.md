# Архитектура Ubuntu VPN Gateway

## Назначение и границы

Приложение управляет исходящими соединениями самого Ubuntu Server через VLESS-клиент Xray. Это не VPN-сервер и не система выдачи доступа пользователям. Веб-интерфейс и API обслуживает один Go-бинарник; React + TypeScript собирается в статические файлы, встраиваемые через `embed`. Node.js нужен только для разработки и сборки. Импорт AWG-конфигураций есть, AWG runtime и failover пока не реализованы.

Документ сохраняет целевые контракты и требования. Фактическое состояние компонентов приведено ниже: проект уже включает Core Web, VLESS/Xray, Full Tunnel и Ubuntu installer; ранние описания Phase 0 далее по тексту являются историческими целями, а не перечнем текущих возможностей.

## Слои и зависимости

```text
React UI ── HTTP REST / SSE ── Go API ── application services
                                       ├── SQLite repositories
                                       ├── Connection Provider
                                       │    ├── XrayProvider (Phase 3)
                                       │    └── AmneziaWGProvider (Phase 4)
                                       └── Network controller (Phase 5+)
```

API переводит HTTP-запросы в команды/запросы прикладного слоя. UI не знает команд провайдера и не формирует системные команды. Репозитории скрывают SQL и подключаются к сервисам через узкие интерфейсы. Сетевые привилегированные операции доступны только как заранее определённые функции контроллера; endpoint вида `/exec` запрещён.

Основные фактические пакеты: `cmd/gateway`, `cmd/network-watchdog`, `internal/tunnel`, `internal/{config,httpapi,provider,proxy,store}`, `web/`, `scripts/` и `packaging/systemd/`. `uvg-watchdog` — отдельный бинарник для recovery/rollback, а не goroutine backend. Backend управляет VLESS/Xray и host Full Tunnel IPv4 TCP/UDP; Docker, AWG runtime, kill switch и IPv6 tunnel остаются вне реализованного объёма.

## HTTP контракт Phase 0

- `GET /api/v1/health` — Phase 0 возвращает `{status:"ok", message:"Сервис работает"}`. Поля версии/времени не обещаются до фактической реализации.
- `GET /api/v1/status` — Phase 0 возвращает демонстрационное `{state:"disconnected", message:"Провайдер не подключен"}`. Это константа, а не опрос VPN.
- `/` и пути UI обслуживаются встроенной статикой; неизвестный API-путь возвращает JSON 404, а не `index.html`.
- API версионируется префиксом `/api/v1`. Следующие фазы добавляют пространства `/auth`, `/status`, `/connections`, `/subscriptions`, `/awg`, `/routing`, `/failover`, `/diagnostics`, `/events`, `/settings`.
- Live status будет передаваться SSE (предпочтительно для односторонних обновлений); команды остаются REST. Не вводить секундный polling.
- Снаружи интерфейс и пользовательские описания — на русском языке, если продуктовые указания позднее не потребуют иного.

## Контракт провайдера соединения

Контракт нейтрален к типу транспорта. Он оперирует идентификаторами записей и типизированными результатами, не принимает shell-фрагменты. Минимальная форма Go API:

```go
type Provider interface {
    Connect(ctx context.Context, id string) error
    Disconnect(ctx context.Context) error
    Status(ctx context.Context) (Status, error)
    Test(ctx context.Context, id string) (TestResult, error)
}
```

Фактический `internal/provider.Provider` уже соответствует показанным сигнатурам. `Status` содержит `State` и `Message`; `TestResult` содержит `Service`, `Endpoint`, `Internet`, `ExitIP`. Enum `State` знает `disconnected`, `connecting`, `connected`, `disconnecting`, `testing`, `switching`, `failed`, `rolling_back` и проверяется `Valid()`. Реализации провайдера и автомата переходов пока нет. `TestResult` предназначен различать уровни: процесс/сервис работает, endpoint достижим, интернет через заданный туннель доступен, exit IP определён. Рабочий процесс не считается доказательством работоспособности VPN.

## Состояния соединения и сериализация

Единый автомат: `Disconnected`, `Connecting`, `Connected`, `Testing`, `Switching`, `Disconnecting`, `Failed`, `RollingBack`. Переходы принадлежат connection service, а не UI или драйверу провайдера. Запросы `Connect`, `Disconnect`, `Switch`, изменение routing и failover сериализуются общей блокировкой/очередью операций; два изменения активного соединения одновременно невозможны. Невалидные переходы возвращают конфликт состояния. После перезапуска runtime сверяется с системой, SQLite не считается источником истины о живом туннеле.

Текущий runtime опрашивает реальные сервисы и durable journal. HTTP health приложения не является доказательством наличия туннеля; состояние Full Tunnel читается через tunnel API и восстанавливается из журнала при старте.

## SQLite: целевая схема Phase 1+

SQLite — постоянное хранилище метаданных, с версионируемыми последовательными миграциями. Включить `foreign_keys`, настроить busy timeout и разумный режим WAL; транзакции для импортов, обновления подписок и изменения порядка failover. Для времени использовать UTC RFC3339 или целочисленный Unix UTC последовательно во всех таблицах. Для стабильной переносимости Windows-разработки выбрать pure-Go драйвер SQLite; перед принятием зависимости проверить лицензирование, сборку и миграции.

Секреты узлов и AWG должны быть зашифрованы на уровне приложения ключом, хранимым вне БД в `/etc/ubuntu-vpn-gateway/secrets/` с ограниченными правами. Не записывать plaintext в журналы, API-ответы, диагностику и резервные копии по умолчанию. Нужны отдельные явно обозначенные экспорт/импорт с секретами. URL подписки также может содержать токен и хранится как секрет.

### Таблицы

| Таблица | Основные поля и ограничения |
|---|---|
| `schema_migrations` | `version INTEGER PRIMARY KEY`, `applied_at TEXT NOT NULL` |
| `users` | `id TEXT PK`, `username TEXT UNIQUE NOT NULL`, `password_hash TEXT NOT NULL` (Argon2id), `disabled INTEGER`, `created_at`, `updated_at`, `password_changed_at`; plaintext пароля нет |
| `sessions` | `id_hash TEXT PK` (только хеш случайного токена), `user_id FK`, `csrf_secret TEXT NOT NULL`, `created_at`, `expires_at`, `last_seen_at`; истёкшие сессии очищаются |
| `settings` | `key TEXT PK`, `value_json TEXT NOT NULL`, `updated_at`; whitelist известных ключей, никаких произвольных системных команд |
| `subscriptions` | `id TEXT PK`, `name`, `url_ciphertext`, `enabled`, `update_interval` enum `disabled/6h/12h/24h`, `last_update_at`, `update_status`, `last_error_safe`, `created_at`, `updated_at` |
| `nodes` | `id TEXT PK`, `subscription_id FK NULL`, `kind` enum `vless/awg`, `name`, `endpoint_host`, `endpoint_port`, `protocol_json`, `secret_ciphertext`, `enabled`, `favorite`, `stale_at`, `last_latency_ms`, `last_test_at`, `last_error_safe`, `created_at`, `updated_at`; индекс по подписке и enabled |
| `awg_profiles` | `node_id TEXT PK/FK nodes`, `config_ciphertext BLOB NOT NULL`, `format_version`, `updated_at`; секретный конфиг целиком шифруется |
| `failover_entries` | `id TEXT PK`, `node_id FK`, `priority INTEGER UNIQUE NOT NULL`, `enabled`, `created_at`; уникальность node/списка |
| `runtime_snapshot` | singleton `id=1`, `connection_state`, `active_node_id FK NULL`, `mode`, `changed_at`, `last_error_safe`; подсказка для восстановления, не авторитетное состояние системы |
| `events` | `id INTEGER PK`, `occurred_at`, `level`, `event_type`, `message_safe`, `details_json_safe`; ограниченная ротация, без UUID/private key/токена |

`protocol_json` содержит нормализованные несекретные VLESS-поля (transport, security, SNI, fingerprint, public key, short ID, flow и XHTTP options). UUID и AWG private key хранятся только в `secret_ciphertext`. `name`, hostname и текст ошибки валидируются и экранируются при отображении. Не хранить производные данные, которые можно безопасно вычислить.

Минимальные настройки Phase 1 включают безопасные defaults для bind `127.0.0.1`, SOCKS `1080`, HTTP proxy `8080`, режим туннеля, DNS, reconnect-on-boot и failover. Применять их можно только в соответствующих поздних фазах. Порты и LAN bind требуют валидации и явного разрешения доступа.

### Целостность и миграции

- Внешние ключи включены при каждом соединении; удаление подписки не должно каскадно удалять активный узел. Узел становится независимым или stale по явной политике.
- Миграция каждой версии применяется атомарно. Перед обновлением production создаётся согласованный backup SQLite; миграции никогда не сбрасывают данные автоматически.
- Импорт/обновление подписки выполняется транзакционно: upsert найденных nodes, отсутствующим ставится `stale_at`; активный node автоматически не удаляется и соединение не останавливается.
- События ограничены по размеру/сроку хранения, application logs структурированы и маскируют секреты.

## Безопасность веб-части

Phase 1 добавляет Argon2id, случайный первичный пароль, secure/HttpOnly/SameSite cookies, CSRF, rate limiting и защиту перебора. HTTPS использует автоматически созданный self-signed сертификат на `:8443`; состояние и ключ сертификата не входят в публичную статику. В ответах и событиях секреты редактируются. Импорт URI/файлов — недоверенный ввод: лимиты размера, строгая схема, безопасные пути и отсутствие shell-интерполяции.

## Ubuntu/Linux networking — отдельная высокорисковая граница

Реализованный host Full Tunnel описан в `docs/networking-design.md`. Safe Apply сохраняет транзакцию и получает durable armed ACK watchdog до первой сетевой мутации. Затем публикуются только conntrack hooks; backend отслеживает состояние, запечатывает разрешённые SSH/UI потоки по полному 4-tuple и получает независимый flow ACK до установки маршрутов и атомарного включения intercept. Стадии watchdog: `tracking → sealing → sealed → pending`; nft правила проекта точечные, внешнее состояние сохраняется. IPv4 TCP/UDP направляются через VLESS; внешние IPv6-пакеты блокируются. Поддержка AWG-профилей, которые меняют default route, ещё не реализована.

Full Tunnel нельзя принимать по проверкам Windows или WSL. Linux namespace и Ubuntu LXC acceptance текущего кандидата прошли для описанных в `docs/test-environment.md` сценариев; WSL kernel в этой среде не поддерживает требуемый nft TPROXY. Публичный GitHub release/update ещё не опубликован и не проверен. AmneziaWG runtime и его совместимость с Ubuntu/kernel не проверялись.

## Размещение на сервере

Пути установки: backend `/usr/local/bin/ubuntu-vpn-gateway`, watchdog и Xray в `/usr/local/lib/ubuntu-vpn-gateway/`, состояние `/var/lib/ubuntu-vpn-gateway/`, конфигурация `/etc/ubuntu-vpn-gateway/`, журналы через journald. Systemd recovery unit выполняется до backend; `ExecStartPre` повторяет durable recovery перед каждым запуском backend.
