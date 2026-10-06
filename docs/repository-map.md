# Карта репозитория

Дата осмотра: 2026-10-06. Основание: текущее дерево проекта и проверенная реализация Ubuntu host Full Tunnel.

## Начало проекта

Изначально рабочий каталог был пуст. `git init` выполнен в корне `H:\codex\vless-panel-gate`; начальных коммитов и существующего приложения не было. После этого созданы код, документы и сборочные файлы Phase 0. Это чистый старт, а не доработка ранее работающего VPN.

## Основные компоненты

| Путь | Назначение и состояние |
|---|---|
| `cmd/gateway/main.go` | CLI backend, HTTPS/API и встроенный UI |
| `cmd/network-watchdog/main.go` | Отдельный CLI recovery/monitor/rollback для устойчивого Full Tunnel |
| `internal/tunnel/` | План, durable journal, network apply/rollback, manager, conntrack flow tracking/seal и status/recovery API |
| `internal/proxy/`, `internal/provider/`, `internal/store/` | VLESS/Xray lifecycle и проверки, provider API, SQLite и зашифрованные записи |
| `web/src/` | React + TypeScript UI, включая Full Tunnel status/apply/confirm/disable/recovery |
| `scripts/install.sh`, `update.sh`, `uninstall.sh`, `build-release.sh` | Локальная установка, обновление, удаление и сборка bundle |
| `scripts/tests/tproxy_namespace.py` | Интеграционная проверка TPROXY в disposable network namespace |
| `scripts/tests/tproxy_fault_namespace.py` | Десять fault-injection фаз watchdog; Ubuntu namespace прогон PASS 10/10 |
| `packaging/systemd/` | Backend, ранний network recovery и отдельный watchdog unit |
| `docs/` | Документация разработки, установки, архитектуры, networking, тестовой среды и релизного bundle |
| `.github/workflows/ci.yml` | Проверки frontend и Go на Linux для Go 1.26/1.27 |
| `Makefile` | Проверка и сборка frontend/backend со встроенной статикой |

Реальный интерфейс создаётся `npm run build` в `web/dist`; `make build` задаёт порядок frontend → Go. В чистом checkout нельзя начинать с `go build`, если `web/dist` отсутствует. Release bundle включает `docs/*.md`, systemd units, скрипты, backend/watchdog и проверенный upstream Xray.

## Статус и ограничения

Реализованы Core Web, VLESS import/local Xray proxy, host Full Tunnel IPv4 TCP/UDP, Safe Apply/watchdog и Ubuntu installer/recovery. Docker routing, AWG runtime, kill switch, IPv6 tunnel и failover остаются backlog. Full Tunnel прошёл перечисленные namespace и Ubuntu LXC проверки, включая fault harness 10/10, lease expiry, reboot и installer update; публичный GitHub release/update ещё не опубликован/проверен. Оба namespace harness включены в CI/release gate.

Подробные зависимости и критерии — в [плане](implementation-plan.md), контракты — в [архитектуре](architecture.md). Windows и CI подтверждают сборку и unit-тесты; Linux networking проверяется только в Linux namespace и на Ubuntu LXC/VM. Текущий WSL kernel не поддерживает nft TPROXY; AWG/TUN runtime не проверен.
