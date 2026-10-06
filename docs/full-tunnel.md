# Full Tunnel VLESS

Full Tunnel направляет исходящий IPv4 TCP/UDP самого Ubuntu-host через активный VLESS-узел. Он использует отдельный Xray с двумя transparent inbound: основной TPROXY и DNS dokodemo-door. Обычный Xray локальных SOCKS5/HTTP proxy продолжает работать отдельным процессом. Все production Xray outbounds получают `SO_MARK=0x200`, чтобы не попасть в повторный перехват.

MVP работает только в режиме fail open: при ошибке запускается точечный rollback. Если очистка не подтверждена полностью, транзакция остаётся в `rollback_failed`, а direct-сеть не объявляется восстановленной. Внешний IPv6 блокируется, пока IPv6 TPROXY не прошёл отдельную приёмку; link-local, connected LAN и управление сохраняются. Docker, AWG, kill switch, маршрутизация чужих узлов и автоматическое повторное включение Full Tunnel после reboot не входят в текущую реализацию.

## Требования к установке

Full Tunnel доступен только на production Linux-запуске от root с systemd и каталогом данных `/var/lib/ubuntu-vpn-gateway`. Bundle устанавливает:

- `/usr/local/lib/ubuntu-vpn-gateway/uvg-watchdog` — независимый helper;
- `/etc/systemd/system/uvg-watchdog@.service` — постоянный watchdog отдельной транзакции;
- `/etc/systemd/system/uvg-network-recovery.service` — recovery до запуска backend;
- журнал транзакций в `/var/lib/ubuntu-vpn-gateway/tunnel` с правами root.

Main service требует успешного `uvg-network-recovery.service`; дополнительно `ExecStartPre` вызывает recovery перед каждым запуском backend, в том числе после systemd restart. Recovery снимает любой прежний Full Tunnel, включая committed-состояние. Это описывает порядок в unit-файлах, а не результат reboot acceptance; автоматического reconnect нет. Installer и updater вызывают recovery до остановки backend и до замены helper. Uninstall также сначала восстанавливает direct-сеть. Если recovery не завершился успешно, обновление helper не должно продолжаться.

В deployment с ограниченным capability-набором чтение `/proc/1/ns/net` вернуло `EPERM`; выдавать backend `CAP_SYS_PTRACE` для этой проверки запрещено. Pre-start recovery, работающий от root до sandbox backend, durable сохраняет `host-netns.json` с host network namespace identity и boot ID. Backend сравнивает этот proof со своим `/proc/self/ns/net` и текущим boot ID. Несовпадение, отсутствующий или устаревший proof делает Full Tunnel недоступным без сетевой мутации.

Backend и запускаемый им Full Tunnel Xray должны иметь `CAP_NET_ADMIN`, `CAP_NET_RAW` и `CAP_NET_BIND_SERVICE`. Отдельная VM-проверка показала `EACCES` (`errno 13`) при transparent TCP/UDP bind только с первыми двумя capability и успешный bind после добавления `CAP_NET_BIND_SERVICE`. Это требование capability, а не повод выдавать `CAP_SYS_PTRACE`. Production sysctl для исправления bind не изменяются.

## Пользовательский поток

Сначала выбранный VLESS-узел должен быть подключён в обычном proxy-режиме и иметь состояние `connected`. Full Tunnel выполняется отдельной транзакцией:

1. `prepare` выполняет read-only discovery, разрешает endpoint в фиксированный IPv4, проверяет конфликты, Xray-конфигурацию, LAN и management peer. Сеть не меняется.
2. UI показывает точный план: endpoint IP/port, management peers, LAN4/LAN6, DNS `1.1.1.1`, блокировку внешнего IPv6, SSH `22`, UI `8443`, политику management-flow и deadline 120 секунд.
3. Пользователь явно запускает `apply`, передавая transaction ID, hash и одноразовый token из prepare.
4. Backend сохраняет durable snapshot/manifest, запускает отдельный Full Tunnel Xray и ждёт durable armed ACK независимого watchdog до сетевых изменений.
5. После armed ACK backend создаёт принадлежащую транзакции nft table с постоянными output/prerouting hooks в tracking-only режиме: только conntrack/counter, без маркировки, drop, TPROXY и маршрутов. С уже установленными hooks backend считывает `ss -Hnt state all`, оставляет только состояния established, SYN-RECV и активные closing states, исключает LISTEN и SYN-SENT и сохраняет полные tuple: local IP, remote IP, local server port и remote client port. `flows.json` создаётся write-once; seal связан с manifest hash и flow hash. Backend ждёт независимый durable flow ACK watchdog. Только после ACK устанавливаются route/rule, затем одним nft batch публикуется intercept в уже созданные chains; hooks/table между tracking и публикацией не пересоздаются.
6. После atomic publish выполняются реальные проверки IPv4 TCP, UDP DNS, TCP DNS и блокировки внешнего IPv6. API остаётся в состоянии pending и отвечает HTTP 202.
7. Та же аутентифицированная сессия с того же remote IP отправляет `confirm` до deadline. Только после успешных checks создаётся durable commit. Если watchdog не сохранил и не вернул commit ACK, confirm возвращает ошибку и немедленно запускает rollback.
8. `disable` или аварийный rollback снимает владельческую nft table. Если удалить table не удалось, best-effort очищаются обе output/prerouting chains; route/rule dependencies и Xray останавливаются только после подтверждения, что intercept выключен. При неподтверждённом снятии dependencies и Xray сохраняются, состояние становится `rollback_failed`, recovery можно повторить.

Token связан с transaction ID, hash плана, cookie-сессией и IP management peer. Token другой сессии, другого peer, использованного/устаревшего плана или запрос после 120 секунд отклоняется. Все POST-запросы дополнительно требуют обычный session cookie, same-origin и `X-CSRF-Token`.

## API

Все маршруты находятся под `/api/v1`, требуют аутентификацию; POST требует CSRF.

| Метод и маршрут | Тело | Результат |
|---|---|---|
| `GET /tunnel/status` | — | `{available,state,message,transaction_id?,deadline?,plan?,checks,exit_ip?}`; при отсутствии in-memory plan выполняется только чтение последней незавершённой durable-транзакции |
| `POST /tunnel/prepare` | `{"node_id":"…"}` | `{plan,token}`; сеть не меняется |
| `POST /tunnel/apply` | `{"transaction_id":"…","hash":"…","token":"…"}` | HTTP 202 и pending status |
| `POST /tunnel/confirm` | те же transaction fields | active status после durable commit ACK |
| `POST /tunnel/disable` | `{}` | при in-memory plan выполняет точечный rollback; после restart без plan повторно запускает recovery |

`plan` содержит `transaction_id`, `hash`, `node_id`, `endpoint_ip`, `endpoint_port`, `management_peers`, `lan4`, `lan6`, optional `management_flow_policy`, `dns`, `ipv6`, `deadline_seconds`, `ssh_port`, `ui_port`. `checks` содержит `tcp`, `dns_udp`, `dns_tcp`, `ipv6_blocked` и присутствует в status во всех состояниях.

Состояния журнала: `prepared`, `armed`, `tracking`, `sealing`, `sealed`, `pending`, `active`, `rolled_back`, `rollback_failed` и `failed`. `tracking`, `sealing` и `sealed` показывают staging до публикации intercept; подтвердить можно только в `pending`. При отсутствующем in-memory plan status читает durable-журнал и не подменяет unresolved/corrupt transaction состоянием `disabled`: сообщает `transaction_id`, `rollback_failed` или исходное staging-состояние, а повреждённый manifest даёт status без `plan`. Пока транзакция не разрешена, status сообщает `available=false`, а UI скрывает/блокирует prepare. `POST disable` без plan может повторить recovery; при неудаче статус и ошибка остаются видимыми для оператора. UI показывает критическую ошибку, а не заявляет восстановленную direct-сеть. UI не должен показывать успех до `active` после durable watchdog acknowledgement.

## Watchdog и fail open

Watchdog — отдельный `Type=simple`, `Restart=no` systemd process с безусловным `ExecStopPost=… rollback`. Он сверяет manifest hash, snapshot hash, boot ID, PID и `/proc/PID/stat` starttime backend и Full Tunnel Xray. Pending deadline равен 120 секундам и heartbeat его не продлевает.

После commit watchdog остаётся жив постоянно и проверяет manifest, flows, seal и ACK. Остановка/crash watchdog, backend или Full Tunnel Xray приводит к идемпотентному rollback. Boot recovery и `ExecStartPre` каждого backend start откатывают незавершённые и committed-транзакции до запуска backend. Reboot test staged-кандидата описан ниже. Обычный proxy Xray не является Full Tunnel Xray и не заменяет эту проверку.

Rollback удаляет только объекты с ожидаемыми owner manifest/transaction ID. Чужие nftables, routes, rules и sysctl не очищаются и не восстанавливаются из полного snapshot.

## DNS-контракт

DNS TPROXY сохраняет исходное назначение, поэтому dokodemo-door использует `followRedirect: true`. Отдельный outbound `uvg-dns-rewrite` имеет protocol `freedom`, переназначает запрос на `1.1.1.1:53` и содержит `proxySettings.tag: "proxy"`; так TCP/UDP DNS принудительно проходит через VLESS. Full Tunnel Xray содержит два outbounds: `proxy` (VLESS) и `uvg-dns-rewrite`. Модель с `followRedirect: false` и фиксированным адресом только во входящем inbound не используется.

## Проверки и текущая граница приёмки

Root namespace-тесты на Ubuntu LXC подтвердили:

- watchdog armed до первой route/rule/nft mutation;
- IPv4 TCP/UDP TPROXY и DNS port 53 до LAN bypass;
- bypass mark и сохранение младших чужих mark bits;
- блокировку внешнего IPv6;
- идемпотентный rollback с сохранением чужих nft/routing объектов.

Новый staged-кандидат прошёл namespace acceptance: сохранён старый полный management 4-tuple, новый TCP flow, возникший между capture и publish, не перехватывается; DNS-before-LAN, mark bypass, IPv6 drop и rollback прошли.

VM acceptance staged-кандидата подтвердила, что HTTP-ответ на `apply` возвращается, API остаётся в `pending`, а `confirm` переводит его в `active`. Старая SSH-сессия сохраняла heartbeat, 12 новых SSH-соединений, открытых во время Apply, прошли. Обычный `curl` вышел с VPN IPv4 `89.125.93.116`. DNS A/AAAA через UDP/TCP прошёл для LAN и публичного resolver; WAN capture на port 53 показал 0 пакетов. UDP NTP вернул 48 байт. Гибель активных Xray, watchdog и backend запускала rollback. Backend restart с `ExecStartPre` завершился health-check, после чего direct-доступ восстановился.

Повторный staged lease 120 секунд прошёл: watchdog перевёл транзакцию в `rolled_back`, table 200 опустела, SSH/API оставались доступны, direct-сеть восстановилась. Reboot Ubuntu LXC с active Full Tunnel прошёл: boot ID изменился, backend запустился, API показал `disabled`, policy rule priority `10000`, table 200 и владельческая nft table отсутствовали; вернулся исходный IPv4 `194.186.91.130`, hashes DNS/master/TLS сохранились. Это подтверждает reboot-поведение этого VM-кандидата без автоматического reconnect.

Штатный installer обновил VM при активном Full Tunnel. Recovery выполнился до замены файлов; после update backend прошёл health, API показывал `disabled`, direct-доступ и hashes DNS/master/TLS сохранились. Публичный GitHub release и update через него для этого кандидата ещё не опубликованы и не проверены.

Полный утверждённый сетевой контракт и порядок правил находятся в `docs/networking-design.md`.
