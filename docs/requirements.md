# Роль

Ты — ведущий архитектор и технический руководитель проекта **Ubuntu VPN Gateway**.

Твоя задача — разработать production-ready open-source приложение для Ubuntu Server, которое позволяет самому серверу выходить в интернет через:

- VLESS RAW/TCP + REALITY;
- VLESS XHTTP + REALITY;
- VLESS XHTTP + TLS;
- обычный VLESS RAW/TCP;
- AmneziaWG 3.1.

Проект должен устанавливаться одним скриптом и полностью управляться через Web-интерфейс.

---

# ВАЖНО: стратегия использования моделей

Основная модель должна выполнять только дорогие задачи:

- архитектура;
- декомпозиция;
- ревью;
- принятие технических решений;
- контроль интерфейсов между компонентами;
- анализ результатов субагентов;
- интеграция;
- финальное ревью;
- поиск сложных ошибок.

Не используй основную модель для рутинного написания большого количества кода, тестов, документации или форматирования.

Максимально используй субагентов.

Если Codex позволяет выбирать модель субагента:

- основной агент: самая сильная доступная модель;
- обычное программирование: более дешёвая модель уровня Luna;
- тесты: дешёвая модель;
- документация: дешёвая модель;
- исследование существующего кода: дешёвая модель;
- frontend: дешёвая модель;
- небольшие исправления: дешёвая модель.

Главная модель должна преимущественно управлять работой.

---

# Правила работы субагентов

Каждому субагенту давай:

1. только необходимый контекст;
2. конкретную задачу;
3. список файлов, с которыми разрешено работать;
4. ожидаемый результат;
5. критерии приёмки.

Не передавай субагентам весь контекст проекта без необходимости.

После выполнения задачи субагент должен возвращать основной модели только:

- краткое описание выполненного;
- изменённые файлы;
- важные архитектурные решения;
- найденные проблемы;
- результаты тестов;
- что осталось сделать.

Не проси субагента пересказывать весь код.

---

# Экономия токенов

Обязательно придерживайся следующих правил.

Не перечитывай весь репозиторий после каждого изменения.

Используй:

- `git diff`;
- `git status`;
- точечный поиск;
- чтение только изменённых файлов;
- чтение только нужных функций и структур.

Не отправляй субагенту большие файлы целиком, если достаточно нескольких функций.

При ревью анализируй прежде всего diff.

Не генерируй длинные пояснения, если они не нужны для принятия решения.

Не дублируй документацию в сообщениях.

Документацию сохраняй в репозитории.

---

# Общая задача проекта

Разработать приложение:

**Ubuntu VPN Gateway**

Его назначение:

> Ubuntu Server должен иметь возможность подключаться к внешним VLESS или AmneziaWG-серверам и направлять через них интернет-трафик самого сервера и отдельных приложений.

Это НЕ VPN-сервер для выдачи доступа другим пользователям.

Это VPN/Proxy CLIENT + Web Management UI.

---

# Основной пользовательский сценарий

Пользователь устанавливает приложение:

```bash
curl -fsSL https://example/install.sh | sudo bash
```

После установки получает:

```text
Ubuntu VPN Gateway installed

Web UI:
https://SERVER-IP:8443

Username:
admin

Password:
<generated-password>
```

Домен не требуется.

Используется self-signed TLS certificate.

Пользователь входит в Web UI и может:

- добавить VLESS subscription URL;
- вставить отдельный `vless://`;
- импортировать AmneziaWG `.conf`;
- проверить доступность узлов;
- выбрать VPN;
- подключиться;
- отключиться;
- переключить сервер;
- использовать Full Tunnel;
- использовать локальный SOCKS5 proxy;
- использовать локальный HTTP proxy;
- видеть текущий внешний IP;
- видеть статус туннеля;
- видеть RX/TX;
- видеть latency;
- видеть журнал событий;
- видеть ошибки;
- управлять failover.

---

# Поддерживаемые Ubuntu

Поддерживать актуальные поддерживаемые версии Ubuntu Server.

Installer должен определять:

- Ubuntu version;
- architecture;
- kernel version;
- systemd;
- nftables availability;
- совместимость AmneziaWG kernel module.

Не использовать жёсткую привязку только к Ubuntu 24.04.

Если компонент несовместим с конкретной версией Ubuntu/kernel:

- не ломать систему;
- показать понятную ошибку;
- не продолжать опасную установку.

---

# Docker

Основной проект НЕ должен использовать Docker.

Использовать нативные:

- systemd;
- Xray-core;
- AmneziaWG;
- nftables;
- Linux routing;
- TUN;
- netlink.

Docker может поддерживаться в будущем только как объект маршрутизации.

---

# Архитектура

Предпочтительный стек:

## Backend

Go.

Причины:

- один бинарный файл;
- небольшой runtime;
- хорошая работа с Linux API;
- systemd;
- netlink;
- nftables;
- HTTP;
- WebSocket;
- SQLite.

## Frontend

React + TypeScript.

После сборки frontend должен встраиваться в Go binary через `embed`.

На production сервере Node.js не требуется.

## Database

SQLite.

---

# Предлагаемая структура проекта

Используй примерно такую структуру, если не найдёшь объективных причин сделать лучше:

```text
ubuntu-vpn-gateway/
├── cmd/
│   └── gateway/
│
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── database/
│   ├── diagnostics/
│   ├── events/
│   ├── health/
│   ├── installer/
│   ├── network/
│   │   ├── nftables/
│   │   ├── routing/
│   │   └── netlink/
│   │
│   ├── proxy/
│   │   ├── xray/
│   │   └── subscription/
│   │
│   ├── vpn/
│   │   └── amneziawg/
│   │
│   ├── failover/
│   └── services/
│
├── web/
│
├── scripts/
│   ├── install.sh
│   ├── uninstall.sh
│   └── update.sh
│
├── packaging/
│   └── systemd/
│
├── docs/
│
├── tests/
│
├── go.mod
├── Makefile
└── README.md
```

Архитектуру можно улучшить, но избегай overengineering.

---

# Системные каталоги

Production install:

```text
/usr/local/bin/ubuntu-vpn-gateway
```

Data:

```text
/var/lib/ubuntu-vpn-gateway/
```

Config:

```text
/etc/ubuntu-vpn-gateway/
```

Secrets:

```text
/etc/ubuntu-vpn-gateway/secrets/
```

Logs — предпочтительно journald.

Не создавай собственные огромные log-файлы без необходимости.

---

# systemd

Создать:

```text
ubuntu-vpn-gateway.service
```

Xray может использовать собственный systemd service.

AmneziaWG должен использовать нативные Linux interfaces/systemd integration.

---

# VLESS

Использовать Xray-core как клиент.

Поддержать:

## MVP

- VLESS RAW/TCP + REALITY;
- VLESS RAW/TCP + REALITY + Vision;
- VLESS XHTTP + REALITY;
- VLESS XHTTP + TLS;
- VLESS RAW/TCP.

Архитектура должна позволять позже добавить:

- gRPC;
- WebSocket;
- HTTPUpgrade.

---

# Импорт VLESS

Поддерживать:

## 1. Одиночная URI

```text
vless://...
```

Парсер должен извлекать необходимые параметры автоматически.

Не заставлять пользователя вручную вводить UUID, SNI, Public Key, Short ID и т. п.

---

## 2. Subscription URL

Можно добавить несколько подписок.

Пример:

```text
Personal
Backup
Tests
```

Поддержать:

- plain-text список `vless://`;
- Base64 encoded subscription.

Архитектуру сделать расширяемой для будущей поддержки:

- Clash YAML;
- Mihomo;
- sing-box JSON.

---

# Subscription Manager

Для каждой подписки:

- name;
- URL;
- enabled;
- last update;
- update status;
- auto-update interval.

Варианты обновления:

- disabled;
- 6 hours;
- 12 hours;
- 24 hours.

При обновлении:

- новые nodes добавить;
- существующие обновить;
- отсутствующие в новой подписке не удалять мгновенно;
- сначала помечать stale/removed;
- не удалять активное соединение автоматически.

---

# Nodes

Для каждого VLESS node хранить:

- ID;
- subscription;
- name;
- host;
- port;
- UUID;
- transport;
- security;
- SNI;
- fingerprint;
- public key;
- short ID;
- flow;
- XHTTP parameters;
- enabled;
- favorite;
- last latency;
- last test;
- last error.

Секретные параметры не выводить полностью в UI/logs.

---

# AmneziaWG

Использовать AmneziaWG 3.1.

Основной сценарий:

пользователь импортирует готовый клиентский:

```text
.conf
```

или вставляет конфигурацию текстом.

НЕ реализовывать AWG server.

Нам нужен только AWG client.

Поддерживать актуальные параметры AmneziaWG 3.1.

Архитектура парсера конфигурации должна быть независимой от UI.

---

# Connections

Web UI должен показывать общий список:

```text
★ NL Amsterdam
VLESS XHTTP REALITY
48 ms
ONLINE

DE Frankfurt
VLESS RAW REALITY
64 ms
ONLINE

Backup AWG
AmneziaWG 3.1
71 ms
ONLINE
```

Поддержать:

- favorites;
- status;
- latency;
- protocol;
- source subscription;
- connect;
- disconnect;
- test.

---

# Connectivity Test

Нельзя считать VPN рабочим только потому, что Xray process работает.

Проверять уровни:

## Level 1

Service running.

## Level 2

VPN/Proxy endpoint reachable.

## Level 3

Internet доступен через конкретный tunnel/proxy.

## Level 4

Определить exit IP.

---

# Test all nodes

Реализовать:

```text
Test all
```

Но не создавать одновременно сотни тяжёлых Xray instances.

Сделать ограничение concurrency.

Например:

```text
max concurrent tests = 5
```

Значение должно быть конфигурируемым.

---

# Local Proxy Mode

Это одна из основных функций MVP.

При VLESS соединении Xray должен предоставлять:

```text
SOCKS5
127.0.0.1:1080
```

и желательно:

```text
HTTP
127.0.0.1:8080
```

Порты конфигурируемые.

По умолчанию bind только:

```text
127.0.0.1
```

Не выставлять proxy в LAN автоматически.

Для разрешения LAN доступа пользователь должен явно включить соответствующую настройку.

---

# Full Tunnel Mode

Вторая основная функция.

Пользователь может включить:

```text
Route all server traffic through VPN
```

Для AmneziaWG использовать обычную routing table.

Для Xray использовать transparent proxy.

Предпочтительно:

```text
nftables + TPROXY
```

или другой корректный современный подход.

Не использовать устаревшие костыли, если Linux/Xray предоставляет более правильный механизм.

---

# Очень важное требование: Anti-Lockout

Проект будет часто устанавливаться на удалённый Ubuntu Server.

НИКОГДА не допускай, чтобы включение Full Tunnel приводило к потере SSH/Web UI.

Перед изменением default routing определить:

- default gateway;
- physical interface;
- SSH client remote IP, если возможно;
- локальные сети;
- VPN endpoint;
- адрес Web UI.

VPN endpoint ОБЯЗАТЕЛЬНО должен получить direct route через исходный gateway.

Локальная сеть не должна случайно уйти в VPN.

Управляющий SSH-трафик не должен потеряться.

---

# Safe Apply

Любая операция изменения routing должна быть транзакционной.

Алгоритм:

```text
Generate configuration

↓

Validate

↓

Save current routing/firewall state

↓

Apply temporary configuration

↓

Start watchdog rollback timer

↓

Test VPN

↓

Test Internet

↓

Test management connectivity

↓

Commit
```

Если commit не произошёл:

```text
automatic rollback
```

---

# Rollback Watchdog

Это обязательная функция.

При применении Full Tunnel создать независимый rollback watchdog.

Например:

```text
60 second rollback timer
```

После успешной проверки backend подтверждает конфигурацию.

Если backend:

- завис;
- упал;
- потерял сеть;
- не смог подтвердить;

watchdog автоматически восстанавливает предыдущие:

- routes;
- nftables rules;
- policy rules.

Watchdog не должен зависеть только от основного backend process.

---

# Fail Open / Kill Switch

Поддержать два режима.

## Fail Open

При падении VPN:

```text
Internet → DIRECT
```

Это режим по умолчанию.

## Kill Switch

При падении VPN:

```text
Internet → BLOCK
```

Но:

- SSH management;
- local LAN;
- VPN endpoint;
- Web UI;

должны продолжать работать согласно настройкам.

---

# Auto Failover

Реализовать.

Пользователь может создать priority list:

```text
1. NL-XHTTP
2. DE-REALITY
3. AWG-BACKUP
```

Настройки:

```text
fail after: 3 failed health checks
recovery checks: 2
```

При падении:

```text
Active node failed

↓

Test next node

↓

Safe switch

↓

Validate Internet

↓

Commit
```

Не переключаться бесконечно между двумя нестабильными серверами.

Использовать cooldown.

---

# Восстановление primary

Добавить настройку:

```text
Return to preferred node automatically
```

Варианты:

- disabled;
- after N successful checks;
- manual.

---

# Dashboard

Главная страница должна показывать:

```text
VPN STATUS
CONNECTED

Connection:
NL-Amsterdam-01

Protocol:
VLESS XHTTP REALITY

Mode:
Full Tunnel

External IP:
xxx.xxx.xxx.xxx

Latency:
48 ms

Connected:
3h 42m

Traffic:
↓ 4.8 GB
↑ 830 MB
```

Ниже:

```text
CPU
RAM
Uptime
Ubuntu version
Xray version
AmneziaWG version
Gateway version
```

---

# Web UI

Интерфейс должен быть:

- простой;
- адаптивный;
- пригодный для мобильного браузера;
- без перегруженных enterprise-компонентов.

Основные страницы:

```text
Dashboard

Connections

Subscriptions

AmneziaWG

Routing

Failover

Diagnostics

Logs

Settings
```

---

# Authentication

В MVP достаточно одного или нескольких локальных admin users.

Использовать:

- Argon2id;
- secure session cookies;
- HttpOnly;
- SameSite;
- CSRF protection;
- login rate limiting;
- brute-force protection.

Не хранить plaintext password.

---

# First Run

Installer генерирует случайный admin password.

При первом входе желательно предложить его изменить.

---

# HTTPS

Домен не требуется.

При первом запуске автоматически генерировать self-signed certificate.

Web UI:

```text
https://SERVER-IP:8443
```

Позже можно позволить:

- upload custom certificate;
- custom key;
- Let's Encrypt.

Но Let's Encrypt не является задачей MVP.

---

# Firewall

Использовать nftables.

Не удалять существующие firewall rules пользователя.

Это критично.

Создавать собственные таблицы/chains с уникальными именами.

Например:

```text
table inet uvg
```

Не делать:

```text
nft flush ruleset
```

НИКОГДА.

Удалять только правила, созданные самим Ubuntu VPN Gateway.

---

# Routing isolation

Все созданные проектом:

- nftables tables;
- routing tables;
- fwmarks;
- ip rules;

должны иметь чётко определённые identifiers.

Не конфликтовать с:

- Docker;
- Kubernetes;
- WireGuard;
- Tailscale;
- existing VPN;
- custom firewall.

---

# DNS

Для MVP:

по умолчанию не ломать системный DNS.

Добавить диагностическую проверку DNS.

При Full Tunnel предусмотреть настройку:

```text
DNS mode:

System
Through VPN
Custom
```

Полноценную сложную DNS routing систему можно оставить на следующий этап.

---

# Logs

Использовать структурированные логи.

Не логировать:

- UUID полностью;
- passwords;
- private keys;
- subscription URLs полностью;
- tokens.

В UI показывать Event Log.

Примеры:

```text
Subscription updated

Connection NL-01 started

Internet check passed

Failover: NL-01 → DE-01

Routing rollback triggered
```

---

# Diagnostics

Реализовать страницу Diagnostics.

Проверки:

- physical Internet;
- DNS;
- default gateway;
- Xray binary;
- Xray version;
- Xray config validation;
- AWG module;
- AWG interface;
- nftables;
- routing;
- proxy connection;
- Full Tunnel;
- exit IP;
- SOCKS;
- HTTP proxy.

Кнопка:

```text
Run diagnostics
```

---

# Backup

Создать экспорт backup.

Backup включает:

- SQLite;
- settings;
- subscriptions;
- nodes;
- AWG configs;
- certificates;
- routing settings;
- failover settings.

Private secrets можно включать только с явным предупреждением.

Предусмотреть восстановление.

---

# Update

Поддержать:

```bash
ubuntu-vpn-gateway update
```

и позже кнопку в Web UI.

Обновлять независимо:

- Gateway;
- Xray;
- AmneziaWG.

Перед update:

```text
backup
```

При неудаче предусмотреть rollback Gateway binary.

---

# Installer

Создать idempotent installer:

```text
scripts/install.sh
```

Он должен:

1. проверить root;
2. определить Ubuntu;
3. определить CPU architecture;
4. определить kernel;
5. проверить networking prerequisites;
6. установить зависимости;
7. установить Xray;
8. установить AmneziaWG;
9. установить Gateway binary;
10. создать directories;
11. создать service user;
12. настроить permissions;
13. создать DB;
14. создать admin account;
15. создать TLS certificate;
16. установить systemd units;
17. запустить Gateway;
18. проверить health endpoint;
19. вывести Web UI URL и credentials.

Повторный запуск installer не должен разрушать существующую конфигурацию.

---

# Uninstall

Создать:

```text
scripts/uninstall.sh
```

Uninstall должен сначала:

- отключить Full Tunnel;
- восстановить маршруты;
- удалить собственные nftables rules;
- остановить сервисы.

По умолчанию спросить/предоставить выбор:

```text
Keep configuration and data
```

---

# API

Backend REST API.

Примерные namespaces:

```text
/api/v1/auth
/api/v1/status
/api/v1/connections
/api/v1/subscriptions
/api/v1/awg
/api/v1/routing
/api/v1/failover
/api/v1/diagnostics
/api/v1/events
/api/v1/settings
```

Использовать WebSocket или SSE для live status.

Не делать polling каждую секунду без необходимости.

---

# Internal abstraction

Сделать общий интерфейс connection provider.

Примерно:

```go
type Provider interface {
    Connect(ctx context.Context, id string) error
    Disconnect(ctx context.Context) error
    Status(ctx context.Context) (Status, error)
    Test(ctx context.Context, id string) (TestResult, error)
}
```

Реальные реализации:

```text
XrayProvider
AmneziaWGProvider
```

Не связывать frontend напрямую с особенностями конкретного provider.

---

# State Machine

Connection management должен иметь явные states:

```text
Disconnected

Connecting

Connected

Testing

Switching

Disconnecting

Failed

RollingBack
```

Не использовать десятки независимых boolean flags.

---

# Concurrency

Защитить операции:

```text
Connect
Disconnect
Switch
Apply routing
Failover
```

от одновременного запуска.

Одновременно должна выполняться только одна операция, меняющая активное соединение.

---

# Crash Recovery

После перезагрузки сервера приложение должно определить реальное состояние системы.

Не доверять только данным SQLite.

Проверять:

- running Xray;
- active AWG interface;
- nftables;
- routes.

Затем синхронизировать runtime state.

---

# Reboot behavior

Настройка:

```text
Reconnect on boot
```

По умолчанию:

```text
enabled
```

Если до reboot был активный connection и включён reconnect, восстановить его безопасно.

---

# Security model

Основной backend НЕ должен исполнять произвольные shell-команды от Web UI.

Не создавать API типа:

```text
POST /exec
```

Никогда.

Все privileged operations должны быть реализованы как заранее определённые функции.

Проверять пользовательский ввод.

Особое внимание:

- command injection;
- path traversal;
- malformed subscription;
- malicious node names;
- malicious config import.

---

# Privileges

Если возможно:

- Web backend работает под отдельным service user;
- privileged networking operations вынести в минимальный helper.

Если это чрезмерно усложняет MVP, допустим временный root backend, НО архитектура должна позволять затем убрать root.

Если используешь root в MVP:

- явно задокументируй причины;
- максимально ограничь входные данные;
- не предоставляй arbitrary command execution.

---

# Tests

Обязательные unit tests:

- VLESS URI parsing;
- Base64 subscription parsing;
- AWG config parsing;
- configuration validation;
- failover state machine;
- routing state;
- rollback logic;
- auth;
- config migration.

---

# Integration tests

Разработай тестируемую abstraction над Linux networking.

Не делай unit tests зависимыми от реального `/sbin/ip`.

Для integration tests можно использовать:

- Linux network namespaces;
- dummy interfaces;
- temporary nftables tables.

Integration tests, требующие root, должны иметь отдельный tag/profile.

---

# Frontend tests

Не трать много ресурсов на snapshot testing.

Приоритет:

- critical forms;
- authentication;
- connection switching;
- subscription import;
- routing safety confirmations.

---

# CI

Создать GitHub Actions.

Минимум:

- gofmt;
- go vet;
- go test;
- frontend lint;
- frontend build;
- backend build.

Если возможно:

- race detector;
- security scanning;
- installer shellcheck.

---

# Documentation

Минимальные документы:

```text
README.md
docs/architecture.md
docs/install.md
docs/networking.md
docs/security.md
docs/development.md
```

Не пиши огромную документацию до появления работающего MVP.

Документируй решения по мере реализации.

---

# Git strategy

Работать небольшими логическими commits.

Не делать один commit на весь проект.

Пример:

```text
feat: bootstrap Go backend

feat: add authentication

feat: parse VLESS subscriptions

feat: integrate Xray client

feat: add local proxy mode

feat: implement safe routing

feat: add AmneziaWG provider

feat: add failover manager
```

---

# НЕ ДЕЛАТЬ

В первой версии НЕ реализовывать:

- собственный VLESS server;
- собственный AWG server;
- billing;
- multi-tenant VPN users;
- subscription sales;
- mobile native app;
- Kubernetes;
- distributed controller;
- multi-server management;
- BGP routing;
- GeoSite Smart Routing;
- complicated DNS filtering;
- traffic accounting for billing.

Архитектура может позволять это позже, но сейчас не тратить токены и время.

---

# Деление работы между субагентами

Создай специализированных субагентов.

## Agent A — Architecture / repository analysis

Задачи:

- изучить существующий repository;
- определить состояние проекта;
- предложить минимальные архитектурные изменения;
- найти технические долги.

Не писать большие объёмы кода.

---

## Agent B — Go Backend

Задачи:

- API;
- database;
- services;
- state management;
- auth;
- configuration.

---

## Agent C — Xray

Задачи:

- VLESS parser;
- subscriptions;
- Xray config generation;
- Xray validation;
- lifecycle;
- local SOCKS/HTTP proxy;
- transparent proxy integration.

---

## Agent D — Networking

Задачи:

- nftables;
- routes;
- policy routing;
- TPROXY;
- safe apply;
- rollback watchdog;
- anti-lockout;
- fail-open;
- kill switch.

Этот компонент считать HIGH RISK.

Любые изменения Agent D обязательно ревьюит основной агент.

---

## Agent E — AmneziaWG

Задачи:

- AWG 3.1;
- config parser;
- import;
- interface lifecycle;
- traffic counters;
- status;
- routing integration.

---

## Agent F — Frontend

Задачи:

- React;
- responsive UI;
- dashboard;
- connections;
- subscriptions;
- routing;
- failover;
- diagnostics;
- logs;
- settings.

---

## Agent G — Tests

Задачи:

- unit tests;
- integration tests;
- edge cases;
- regression tests.

Agent G не должен переписывать production код без согласования.

Если найдена ошибка — возвращает reproducer основному агенту.

---

## Agent H — Installer / packaging

Задачи:

- install.sh;
- update.sh;
- uninstall.sh;
- systemd;
- permissions;
- Ubuntu compatibility.

---

## Agent I — Security Review

Запускать периодически, не постоянно.

Проверять:

- auth;
- command injection;
- file permissions;
- secrets;
- API authorization;
- CSRF;
- network privilege boundaries.

---

# Как экономить основную модель

Основная модель НЕ должна сама писать весь код.

Рабочий цикл:

```text
Main Agent
   ↓
design task
   ↓
delegate
   ↓
Subagent
   ↓
code + tests
   ↓
short report
   ↓
Main Agent reviews diff
   ↓
accept / small corrections
```

Если diff простой и тесты проходят — не переписывать его основной моделью.

---

# Параллельная работа

Параллельно можно выполнять только независимые задачи.

Например:

```text
Backend database
Frontend skeleton
Installer skeleton
VLESS parser
```

можно делать одновременно.

Но:

```text
routing
+
full tunnel
+
failover
```

сильно связаны.

Их сначала проектировать последовательно.

---

# Избегание конфликтов субагентов

Каждому агенту назначать области файлов.

Например:

```text
Agent C:
internal/proxy/**

Agent D:
internal/network/**

Agent E:
internal/vpn/amneziawg/**

Agent F:
web/**
```

Если изменение затрагивает чужую область — агент возвращает предложение основному агенту, а не самостоятельно делает крупную переработку.

---

# План реализации

Работать по этапам.

---

# Phase 0 — Repository bootstrap

Сначала:

- проанализировать repo;
- определить текущее состояние;
- создать architecture document;
- определить interfaces;
- определить database schema;
- подготовить development environment.

Результат:

backend/frontend собираются.

---

# Phase 1 — Core Web Application

Реализовать:

- Go backend;
- SQLite;
- migrations;
- React frontend;
- HTTPS;
- admin login;
- sessions;
- dashboard skeleton;
- REST API;
- SSE/WebSocket.

После этого проект должен запускаться без VPN functionality.

---

# Phase 2 — VLESS Import

Реализовать:

- `vless://` parser;
- subscription URL;
- Base64 subscription;
- multiple subscriptions;
- nodes;
- favorites;
- update subscriptions.

Unit tests обязательны.

---

# Phase 3 — Xray Local Proxy

Реализовать:

- Xray installation detection;
- config generation;
- SOCKS5;
- HTTP proxy;
- connect;
- disconnect;
- test;
- external IP;
- latency;
- status.

На этом этапе НЕ включать Full Tunnel.

После Phase 3 приложение уже должно быть практически полезным.

---

# Phase 4 — AmneziaWG

Реализовать:

- import `.conf`;
- AWG 3.1;
- connect;
- disconnect;
- status;
- latency;
- exit IP;
- counters.

---

# Phase 5 — Full Tunnel

Это HIGH RISK этап.

До написания кода Agent D должен сначала предоставить:

```text
docs/networking-design.md
```

с описанием:

- packet flow;
- fwmark;
- nftables chains;
- routing tables;
- direct endpoint route;
- LAN exclusions;
- SSH exclusions;
- DNS;
- rollback.

Основной агент должен провести ревью design до начала реализации.

После одобрения реализовать Full Tunnel.

---

# Phase 6 — Safe Apply / Watchdog

Реализовать independent rollback mechanism.

Обязательно тестировать искусственный failure.

Например:

```text
apply bad route
↓
health fails
↓
watchdog restores network
```

Без работающего rollback нельзя считать Full Tunnel готовым.

---

# Phase 7 — Failover

После стабильного Full Tunnel:

- health monitor;
- failure threshold;
- priority list;
- cooldown;
- switch;
- recovery;
- optional return to preferred.

---

# Phase 8 — Installer

Сделать production installer.

Проверить на clean Ubuntu VM.

Минимум:

- одна актуальная LTS;
- ещё одна поддерживаемая Ubuntu release.

---

# Phase 9 — Security + QA

Запустить:

- Security Agent;
- Test Agent;
- installer testing;
- reboot testing;
- failure testing;
- malformed configs;
- invalid subscriptions;
- wrong routes;
- Xray crash;
- AWG crash.

---

# Критерии MVP

MVP считается завершённым только если выполняется сценарий:

```text
Clean Ubuntu
↓
run install.sh
↓
open Web UI
↓
login
↓
add subscription
↓
see VLESS nodes
↓
test nodes
↓
connect
↓
SOCKS proxy works
↓
exit IP changed through proxy
↓
enable Full Tunnel
↓
server Internet exits via VPN
↓
SSH remains available
↓
disconnect
↓
original routing restored
↓
import AWG config
↓
connect AWG
↓
Internet works through AWG
↓
fail active connection
↓
failover works
```

---

# Проверка перед каждым merge

Основной агент проверяет:

```text
git diff
```

и отвечает на вопросы:

1. Не ломает ли изменение существующую архитектуру?
2. Не появилась ли возможность command injection?
3. Не логируются ли secrets?
4. Не ломается ли SSH?
5. Не модифицируется ли чужой nftables ruleset?
6. Есть ли rollback?
7. Покрыта ли новая логика тестами?
8. Есть ли unnecessary complexity?

---

# Правило архитектуры

При выборе между:

```text
сложное универсальное решение
```

и:

```text
простое решение, которое покрывает наши реальные задачи
```

в MVP выбирай второе.

---

# Первое действие

Не начинай сразу писать весь проект.

Выполни следующий workflow:

1. изучи текущий repository;
2. создай краткую карту существующих файлов и возможностей;
3. сравни текущее состояние с данным ТЗ;
4. создай `docs/implementation-plan.md`;
5. разбей работу на Phase 0–9;
6. укажи зависимости между задачами;
7. определи, какие задачи можно передать субагентам параллельно;
8. после этого начни Phase 0;
9. после каждой фазы запускай тесты;
10. не переходи к HIGH RISK networking code без предварительного design review.

Основная модель должна действовать как Tech Lead, а не как единственный программист.

Главная цель:

**получить надёжный Ubuntu VPN Gateway при минимальном расходе токенов основной модели.**