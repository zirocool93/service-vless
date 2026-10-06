# Сетевые функции

Приложение поддерживает два режима VLESS:

- локальные SOCKS5 `127.0.0.1:1080` и HTTP `127.0.0.1:8080` proxy без изменения host routing;
- Full Tunnel исходящего IPv4 TCP/UDP самого Ubuntu-host через отдельный Xray TPROXY.

Full Tunnel не обрабатывает `FORWARD` и трафик других хостов/контейнеров. Docker routing, AWG runtime, kill switch и IPv6 TPROXY находятся вне текущего scope.

## Безопасное применение

Операция проходит `prepare → explicit apply → armed → tracking → sealing → sealed → pending checks → peer-bound confirm`. Prepare выполняет read-only discovery и показывает endpoint IPv4/port, management peers, connected LAN, DNS, блокировку внешнего IPv6 и срок 120 секунд. До сетевых изменений durable сохраняются snapshot/manifest, запускается отдельный Full Tunnel Xray и получается armed ACK независимого watchdog. После ACK создаются только постоянные владельческие conntrack hooks без маркировки, маршрутизации или TPROXY; затем снимаются и запечатываются точные management flows, и watchdog отдельно подтверждает flow seal.

После flow ACK backend повторно проверяет lease, владельца/child, seal и ACK под `network.lock`, применяет владельческие route/rule, затем атомарным nft batch наполняет те же base chains interception-правилами. Таблица/hooks не удаляются и не создаются повторно на границе staging→intercept. Snapshot выполняется через `ss -Hnt state all`: включаются established, SYN-RECV и активные closing states, исключаются LISTEN и SYN-SENT. Для management peers и local server ports SSH `22`/UI `8443` сохраняется полный 4-tuple: local IP, remote IP, local server port, remote client port. Точный flow bypass стоит до общего conntrack reply правила; bypass для новых replies всё равно требует точный peer, server source port, established/related и `ct direction reply`. Endpoint фиксируется узкой парой IP+port. Loopback, connected LAN, link-local и multicast остаются direct, кроме DNS port 53, который перехватывается раньше LAN bypass и отправляется к `1.1.1.1:53` через VLESS.

DNS inbound использует dokodemo-door `followRedirect: true`. Routing направляет его в `uvg-dns-rewrite` freedom outbound с redirect `1.1.1.1:53`; `proxySettings.tag: "proxy"` заставляет этот outbound идти через VLESS. Это два отдельных outbounds — `proxy` (VLESS) и `uvg-dns-rewrite`. Фиксированный адрес во входящем inbound с `followRedirect: false` не применяется.

Внешний IPv6 блокируется до проверки bypass mark, поэтому SO_MARK не создаёт IPv6-утечку. Необходимые link-local/ND и related PMTU сохраняются. Приложение не меняет sysctl, `/etc/resolv.conf` и systemd-resolved.

## Watchdog и восстановление

`uvg-watchdog@.service` — отдельный постоянный процесс с безусловным `ExecStopPost` rollback. Он проверяет durable hashes, boot ID и PID+process starttime backend и Full Tunnel Xray. Deadline 120 секунд относится только к pending; heartbeat его не продлевает. После commit watchdog живёт постоянно. Падение backend, Full Tunnel Xray или watchdog снимает Full Tunnel и возвращает fail-open direct-сеть.

`uvg-network-recovery.service` выполняется до backend при boot, а `ExecStartPre` основного unit отдельно вызывает recovery перед каждым запуском/restart backend. Оба пути откатывают также committed Full Tunnel. Reboot acceptance staged-кандидата прошла; автоматического reconnect после reboot нет. Update/uninstall запускают recovery до остановки backend и замены helper.

Recovery durable записывает root-owned `host-netns.json` с host namespace identity и boot ID. Backend сравнивает его со своим namespace/boot ID и не читает `/proc/1/ns/net`: в sandbox deployment это чтение дало `EPERM`, а расширение до `CAP_SYS_PTRACE` не допускается.

Backend и дочерний Full Tunnel Xray получают `CAP_NET_ADMIN`, `CAP_NET_RAW` и `CAP_NET_BIND_SERVICE`. Проверка transient bind на VM вернула `errno 13` без `CAP_NET_BIND_SERVICE` и прошла после её добавления. Production sysctl не меняются.

Rollback удаляет только объекты своего transaction manifest. Чужие nftables, routes, rules и sysctl не очищаются.

Rollback сначала удаляет только владельческую table. При ошибке удаления best-effort очищаются output/prerouting chains; route/rule dependencies и Full Tunnel Xray удаляются только после подтверждения, что оба intercept hooks выключены. Пока proof отсутствует, dependencies и Xray сохраняются. Если cleanup неполон, журнал получает `rollback_failed`; API не сообщает fail-open success и требует диагностики/повторного recovery. Отсутствующий durable commit ACK также считается ошибкой confirm и запускает rollback.

Namespace acceptance staged-кандидата прошла: старый management 4-tuple сохранён, новый TCP flow, созданный между capture и publish, сохранил direct-доступ; DNS-before-LAN, mark bypass, IPv6 drop и rollback подтверждены. Ранее пройдены также dummy-source проверки без изменения production sysctl.

В VM Apply вернул исходный HTTP-ответ и статус `pending`; `confirm` установил `active`. Старый SSH-flow сохранял heartbeat, 12 новых SSH flows во время Apply работали. Проверены VPN exit IPv4 `89.125.93.116`, DNS A/AAAA UDP/TCP через LAN и публичный resolver, 0 пакетов WAN port 53 и UDP NTP с ответом 48 байт. Гибель активных Xray, watchdog или backend вызывала rollback. Backend restart прошёл `ExecStartPre` recovery и health, затем восстановился direct-доступ.

Повторный staged lease 120 секунд прошёл: rollback оставил table 200 пустой, сохранил SSH/API и восстановил direct. Reboot Ubuntu LXC из active Full Tunnel прошёл: boot ID сменился, backend стал active, API сообщил `disabled`, policy rule `10000`, table 200 и владельческая nft table отсутствовали, direct IPv4 вернулся к `194.186.91.130`, DNS/master/TLS hashes не изменились. Локальный штатный installer обновил active-туннельную VM: recovery выполнился до замены файлов, после установки backend healthy, API `disabled`, direct и hashes сохранены. Не проверена только публикация/обновление этого кандидата через публичный GitHub release. Точный контракт: [networking-design.md](networking-design.md); пользовательский/API-поток: [full-tunnel.md](full-tunnel.md).
