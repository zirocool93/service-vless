# Карта репозитория

Дата осмотра: 2026-10-05. Основание: ТЗ «Ubuntu VPN Gateway» и фактическое дерево Phase 0.

## Начало проекта

Изначально рабочий каталог был пуст. `git init` выполнен в корне `H:\codex\vless-panel-gate`; начальных коммитов и существующего приложения не было. После этого созданы код, документы и сборочные файлы Phase 0. Это чистый старт, а не доработка ранее работающего VPN.

## Реализованное дерево Phase 0

| Путь | Назначение и состояние |
|---|---|
| `cmd/gateway/main.go` | CLI `gateway serve`, HTTP на `127.0.0.1:8443` по умолчанию; локальный development запуск |
| `internal/config/` | Валидация адреса и параметров запуска; подключения не создаёт |
| `internal/httpapi/` | Health/status, JSON 404 для неизвестного API, раздача встроенного UI |
| `internal/provider/` | Контракт Provider, Status/TestResult и enum State с `Valid()`; автомата переходов и реализации провайдера нет |
| `web/` | React + TypeScript, сборка `dist`, встраивание через `embed.go` |
| `docs/` | Требования, архитектура, план, инструкции, журнал и незавершённый проект сетевого управления |
| `.github/workflows/ci.yml` | Проверки frontend и Go на Linux для Go 1.26/1.27 |
| `Makefile` | Установка зависимостей frontend, проверка и сборка бинарника со статикой |

Реальный интерфейс создаётся `npm run build` в `web/dist`; `make build` задаёт порядок frontend → Go. В чистом checkout нельзя начинать с `go build`, если `web/dist` отсутствует.

## Следующие фазы

Phase 1 добавит SQLite, миграции, аутентификацию и HTTPS; Phase 2–4 — импорт VLESS, Xray и клиент AWG; Phase 5–7 — Full Tunnel, Safe Apply/watchdog и failover; Phase 8 — установщик/systemd; Phase 9 — security и интеграционную приёмку. Их каталоги (`internal/database`, `internal/auth`, `internal/network`, `scripts`, `packaging` и другие) пока не являются реализованной функциональностью. Перед сетевым кодом обязателен отдельный review [сетевого проекта](networking-design.md).

Подробные зависимости и критерии — в [плане](implementation-plan.md), контракты — в [архитектуре](architecture.md). Windows и CI подтверждают сборку и unit-тесты; поведение Linux networking, systemd, nftables, TUN, AWG и восстановление после сетевого сбоя можно принимать только на Ubuntu VM.
