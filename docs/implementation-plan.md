# План реализации

План следует ТЗ Ubuntu VPN Gateway. Реализованы Core Web, импорт VLESS/подписок, локальное проксирование Xray, host Full Tunnel IPv4 TCP/UDP и Ubuntu install/update/recovery. Docker/AWG runtime/kill switch/IPv6 tunnel и failover остаются backlog. Публичный GitHub release/update ещё не опубликован и не проверен; это не равнозначно выпуску продукта.

## Текущее сравнение

В проекте есть Go API, HTTPS/auth, SQLite с шифрованием секретов, VLESS-парсер/подписки и Xray. Host Full Tunnel использует отдельный network-watchdog и recovery units. Namespace/Ubuntu LXC результаты, включая fault harness 10/10, повторный lease expiry, reboot с активным туннелем и installer update из active состояния, перечислены в [тестовой среде](test-environment.md). Публичный GitHub release/update остаётся непроверенным. Архитектурный target и оставшийся backlog сохранены ниже.

## Зависимости фаз

```text
0 Bootstrap
  └─ 1 Core Web (DB, auth, HTTPS, dashboard/API/SSE)
       └─ 2 VLESS import/subscriptions ──┐
            └─ 3 Xray local proxy        ├─ 5 Full Tunnel ─ 6 Watchdog
       └─ 4 AmneziaWG client ────────────┘                    └─ 7 Failover
                                                                  └─ 9 Security/QA
8 Installer развивается как skeleton после 0, hardening после 1–7
```

Phase 5 зависит от стабильных провайдеров Phase 3/4. Phase 6 обязателен для приёмки Phase 5, хотя подготовку интерфейса snapshot/watchdog можно делать заранее. Failover после стабильных Full Tunnel + rollback, поскольку безопасное переключение опирается на те же транзакции. Phase 9 проверяет интегрированный продукт. Phase 8 установочный скелет можно готовить независимо после согласования каталогов; production installer принимается только с end-to-end тестами на VM.

## Проверяемые этапы

### Phase 0 — Bootstrap (завершена; исторические критерии)

**Сделать:** Go module и HTTP-сервер; React + TypeScript skeleton; воспроизводимая сборка frontend; встраивание `web/dist` через `embed`; `GET /api/v1/health` и демонстрационный `GET /api/v1/status`; интерфейс `Provider`, тип состояния и валидация enum без сетевых эффектов; локальная команда запуска.

**Границы:** без SQLite, VPN-процессов, privileged helper, systemd, HTTPS/auth, реального routing и команд ОС. Backend и frontend компилируются на Windows; UI API на русском. Директория embed должна существовать при чистой сборке и иметь понятную ошибку, если assets ещё не созданы.

**Приёмка:** после сборки frontend `go build` собирает единый бинарник с UI; запуск и health дают 200/JSON (`status`, `message`); status выдаёт статическое `disconnected`; встроенная статика возвращается; отсутствующий API route возвращает 404; enum состояния валидируется; никакое действие не запускает Xray/AWG или меняет сеть. Полный автомат переходов запланирован до управления соединениями. Сборка frontend требует Node только в dev/build-среде.

### Phase 1 — Core Web Application

SQLite и миграции; repositories; HTTPS/self-signed; локальный admin, Argon2id, secure sessions, CSRF и rate limit; базовый dashboard; REST namespaces; SSE/live status; event log. Runtime status сверять с системой в будущих интеграциях, не считать SQLite доказательством подключения.

**Приёмка:** миграция новой БД и обновление версии проходят без потери данных; login/logout и CSRF работают; сессии хранят только hash токена; UI доступен по HTTPS; сессия/секрет не попадают в логи.

### Phase 2 — VLESS import

URI parser; plain/Base64 subscription parser; несколько подписок, nodes, избранное, лимиты размера и валидация; плановое обновление 6/12/24 часа. Сопоставление node стабильным fingerprint нормализованных параметров; отсутствующие помечаются stale, активное соединение не удаляется.

**Приёмка:** unit-тесты валидных/искажённых URI, Base64 и подписок; секреты редактируются; повторный импорт обновляет записи идемпотентно.

### Phase 3 — Xray local proxy

Обнаружение/версионирование Xray; безопасная генерация и проверка конфига; lifecycle; локальные SOCKS5 `127.0.0.1:1080` и HTTP `127.0.0.1:8080`; connect/disconnect/test, latency, endpoint/Internet checks и exit IP. Конфигурация создаётся как данные, не как пользовательская shell-команда.

**Приёмка исходного этапа:** SOCKS и HTTP доступны только на localhost по умолчанию; запрос через прокси подтверждает смену exit IP; остановленный, недоступный endpoint и нерабочий выход различаются. Host Full Tunnel реализован отдельно и описан в Phase 5–6 ниже.

### Phase 4 — AmneziaWG client

Парсер AWG 3.1 независимо от UI; импорт `.conf`/текста, проверка совместимости kernel/module, lifecycle, status, latency, exit IP и счётчики. Поддерживается только client.

**Приёмка:** тесты парсера и некорректных конфигов; проверка соединения и трафика на Ubuntu VM с совместимым модулем; несовместимость сообщает безопасную ошибку. Конфиг AWG может содержать default-route AllowedIPs: такое подключение выполняется через Safe Apply/watchdog как минимум для маршрутов, даже если Full Tunnel UI ещё не включён.

### Phase 5 — Host Full Tunnel (IPv4 TCP/UDP реализован; backlog остаётся)

Исходный target требует review `docs/networking-design.md`, packet flow, fwmark/table/chains, маршрута VPN endpoint, LAN/SSH/UI exclusions, DNS и recovery; kill switch и anti-lockout остаются отдельными требованиями, не входящими в текущую реализацию.

**Текущее состояние:** host Full Tunnel для VLESS IPv4 TCP/UDP реализован. Candidate прошёл namespace и Ubuntu LXC сценарии, описанные в `test-environment.md`. Docker routing, AWG runtime, kill switch и IPv6 tunnel не реализованы. Ни эти ограничения, ни локальные проверки не означают завершение всего исходного Phase 5 target.

### Phase 6 — Safe Apply / watchdog (реализовано для host Full Tunnel)

Validate → durable snapshot → независимый rollback watchdog armed ACK → первая мутация → management/traffic checks → commit или восстановление. Порядок закрывает окно отказа между мутацией и watchdog. В реализации watchdog — отдельный systemd unit, не goroutine backend. Внешний IPv6 для текущего IPv4-only туннеля блокируется.

**Текущее состояние:** staged apply и watchdog rollback прошли VM проверки, включая гибель активных компонентов, lease expiry, backend restart, reboot и update при active tunnel. `tproxy_fault_namespace.py` прошёл 10/10 отказов owner/Xray на стадиях armed, tracking, sealing, sealed и pending; оба namespace harness входят в CI/release gate.

### Phase 7 — Failover

Health monitor, priority list, пороги неудач/восстановления, cooldown, переключение через Safe Apply, возврат к preferred node по настройке. Исключить бесконечное переключение между нестабильными узлами.

**Приёмка:** тесты автомата на пороги, cooldown, recovery и отсутствие ping-pong; интеграционный сбой primary переводит на резервный и подтверждает интернет до commit.

### Phase 8 — Installer / packaging

Идемпотентные install/update/uninstall, обнаружение Ubuntu release/архитектуры/kernel/systemd/nftables/AWG; каталоги и права, отдельный service user если реализуемо, credentials first run, TLS, systemd units и health check. Uninstall сначала безопасно восстанавливает сеть и предлагает сохранить data.

**Приёмка:** чистые Ubuntu VM как минимум на актуальной LTS и ещё одном поддерживаемом выпуске; повторная установка не сбрасывает настройки; update failure восстанавливает binary; uninstall сохраняет SSH и по выбору данные.

### Phase 9 — Security + QA

Полный security review: авторизация каждого API, CSRF, command/path injection, секреты, права, недоверенные конфиги/подписки, network privilege boundary. Reboot/failure/malformed input tests; установка, upgrade, rollback и восстановление на чистых VM; CI: gofmt, vet, tests/build, frontend lint/build, shellcheck/security scanning при доступности.

**Приёмка MVP:** весь сценарий из ТЗ от clean Ubuntu/install/login до subscription, прокси, Full Tunnel без потери SSH, отключения с восстановлением, AWG и failover пройден на VM. Windows green build не заменяет этот acceptance.

## Параллельная работа и границы владения

Использовать отдельных исполнителей только для независимых областей. После фиксации Phase 0 API/DB contracts можно распараллелить:

| Область | Допустимое параллельное направление | Владение |
|---|---|---|
| Backend core | миграции/repositories отдельно от auth/API после утверждения схемы и маршрутов | `internal/database/**`, `internal/auth/**`, `internal/api/**` |
| VLESS | parser/subscriptions отдельно от frontend shell по согласованной DTO | `internal/proxy/subscription/**` |
| Frontend | страницы/формы против стабильного API mock | `web/**` |
| Installer | анализ совместимости и systemd skeleton после контрактов каталогов | `scripts/**`, `packaging/**` |
| Tests | независимые тест-кейсы/reproducers, без самостоятельного переписывания production кода | тестовые каталоги |

Xray lifecycle и AmneziaWG lifecycle могут разрабатываться параллельно после общего Provider; объединение соединений в UI согласуется по DTO. Full Tunnel design предшествует коду. Routing, watchdog, failover и их интеграцию не распараллеливать: они используют общие транзакции, rollback и исключения управления. Высокорисковый networking diff требует review tech lead. При назначении каждому исполнителю выдавать узкий список файлов; пересечение — только через предложение владельцу.

## Среда разработки и ограничения

Текущая рабочая среда Windows пригодна для Go/frontend разработки, `gofmt`, компиляции Windows и Linux target, unit-тестов без привязки к реальным Linux-командам, проверки embedded assets, парсеров и SQLite миграций. Использовать портоустойчивые пути и тесты без жёсткого `/sbin/ip`. Выбранный SQLite driver должен собираться на Windows и Ubuntu; наличие CGO не должно быть скрытой предпосылкой обычной сборки.

Windows не предоставляет целевые systemd, nftables, Linux policy routing, TUN и AmneziaWG kernel module. Поэтому среда не подтверждает их реальное поведение. Нужна Ubuntu VM с root для integration/acceptance, сетевым доступом и snapshots; высокорисковые тесты начинать в изолированной VM/network namespace. Нельзя применять маршруты на пользовательской Windows-машине как замену VM-тесту.

## Документирование и запись результатов

Архитектурное решение сначала отражается в `docs/architecture.md`, порядок/критерии — здесь, карта состояния — в `docs/repository-map.md`, фактические действия/результаты и незавершённое — в `docs/work-log.md`. При каждом этапе обновлять журнал, а актуальные риски/решения — профильный документ. Каждый этап закрывается его критериями и результатом проверки до начала зависимых фаз.

## Фактический статус на 2026-10-06

Core Web, VLESS/Xray local proxy, host Full Tunnel IPv4 TCP/UDP и Ubuntu installer/recovery реализованы. VM acceptance staged apply, rollback, lease expiry, reboot с активным туннелем и installer update из active состояния прошли. Fault namespace harness прошёл 10/10, а оба namespace harness добавлены в CI/release gate. Полный исходный MVP не завершён: Docker, AWG runtime, kill switch, IPv6 tunnel и failover остаются backlog. Проверка публичного GitHub release/update ожидается.
