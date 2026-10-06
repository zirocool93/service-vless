# Веб-интерфейс

## Сессия и запросы

При старте клиент вызывает `GET /api/v1/auth/session`. Неавторизованному пользователю показывается форма входа, которая отправляет `POST /api/v1/auth/login` с именем пользователя и паролем. HttpOnly cookie обрабатывается браузером; CSRF-токен хранится в состоянии приложения и автоматически добавляется в заголовок `X-CSRF-Token` для изменяющих запросов. Выход отправляет `POST /api/v1/auth/logout`, затем закрывает локальный экран сессии.

## Реализованные разделы

- Обзор получает статус туннеля, список подключений и доступные backend-поля через `/api/v1/connections/status` и `/api/v1/connections`. Не предоставленные backend метрики отображаются прочерком.
- Подключения поддерживают импорт `POST /api/v1/connections` (`{uri}`), список, избранное через `PATCH /api/v1/connections/{id}` (`{favorite}`), тест `POST /{id}/test`, запрос подключения `POST /{id}/connect` и отключение `POST /api/v1/connections/disconnect`. Ответ проверки показывает уровни, сообщённые сервером.
- Подписки поддерживают список, добавление через `POST /api/v1/subscriptions` (`{name,url,enabled,update_interval}`) и ручное обновление `POST /{id}/refresh`. Интервал задаётся при создании; редактирование существующей записи появится после добавления API.
- AmneziaWG позволяет передать конфигурацию на импорт. Подключение намеренно отключено, пока backend не поддерживает безопасное сетевое применение.
- Full Tunnel раз в две секунды обновляет доступность backend и состояния `disabled`, `prepared`, `armed`, `tracking`, `sealing`, `sealed`, `pending`, `active`, `rolled_back`, `rollback_failed`, `failed`. Если API сообщает staging-состояние, UI блокирует новую подготовку, оставляет действие отключения и показывает прогресс; подтвердить можно только при точном состоянии `pending` после успешных TCP, DNS UDP/TCP и IPv6-block checks. Пользователь сначала просматривает план с endpoint, management peers, LAN, DNS, IPv6 block и deadline, затем отдельно применяет его. Backend после armed ACK создаёт conntrack-only hooks, собирает `ss state all` для разрешённых SSH/UI потоков и запечатывает полный tuple local IP, remote IP, local/remote ports. План может включать описание этой политики. Если commit ACK не получен, UI показывает ошибку после rollback.
- После restart status читает durable-журнал, поэтому unresolved transaction не показывается как `disabled`. При недоступном manifest API/UI оставляют `plan` пустым и показывают `rollback_failed` с transaction ID; prepare недоступна, но кнопка «Отключить и восстановить сеть» запускает повторный recovery без in-memory plan. Ошибка повторного recovery отображается оператору.
- VM-проверка staged потока подтвердила возврат ответа на Apply, отображение `pending` до confirm и `active` после confirm. Текущий SSH-flow сохранял heartbeat; 12 новых SSH-соединений во время Apply прошли. Namespace-тест проверил уже открытый 4-tuple и новый flow между capture/publish. Повторный lease 120 секунд вернул статус rollback, SSH/API и direct; после reboot API показал `disabled` и сохранился исходный direct IPv4 `194.186.91.130`. Локальный installer обновил VM из active Full Tunnel и после восстановления оставил backend healthy и туннель выключенным. Публичный GitHub release/update для этого кандидата ещё не проверен.
- Журнал принимает события `{type,message,time}` через `EventSource` `/api/v1/events`. Периодический опрос не используется.

Резервирование, диагностика и настройки остаются информационными экранами. Маршрутизация реализована только для host Full Tunnel VLESS IPv4 TCP/UDP; Docker, kill switch и AWG runtime недоступны. Ошибки API видны пользователю; ответ `401` завершает локальную сессию. Значения соединения, списков и метрик не подменяются демонстрационными данными. URL подписки отображается только как имя хоста, чтобы не раскрывать секреты из URL.

## Проверка

В каталоге `web/` доступны `npm run lint`, `npm run typecheck`, `npm run test` и `npm run build`. Vite проксирует `/api` на локальный Go backend `127.0.0.1:8443`; в production ассеты собираются в `web/dist` для встраивания в Go.
