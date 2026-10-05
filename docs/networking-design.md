# Проект Full Tunnel для VLESS — на отдельный архитектурный review

Статус: **проект решения, реализация запрещена до отдельного одобрения**. Документ описывает Full Tunnel только для VLESS/Xray через nftables TPROXY. Он не является разрешением менять сеть. На тестовой Ubuntu 24.04 LXC `10.5.2.70` подтверждены лишь `unshare -n` и проверка синтаксиса TPROXY в отдельном network namespace; трафик основного namespace, маршруты, DNS и firewall не изменялись. `/dev/net/tun` отсутствует, поэтому TUN и AmneziaWG не входят в этот дизайн.

## Цели и границы

Full Tunnel направляет поддерживаемый TCP/UDP-трафик, созданный самим сервером, в локальный transparent inbound Xray. Трафик других узлов через сервер в MVP не перехватывается. Управляющий SSH, Web UI, локальные сети и соединение Xray с VLESS endpoint сохраняют прямой путь. IPv4 и IPv6 рассматриваются совместно: включение режима разрешено только при доказанном безопасном поведении обеих семей.

Режим по умолчанию — fail open. Kill switch включается только явным выбором пользователя. ICMP/ICMPv6 Xray не проксирует; в активном Full Tunnel он блокируется для внешних назначений, а не выпускается напрямую. Исключения управления и LAN продолжают работать.

Проект владеет только своими объектами. Он не выполняет `nft flush ruleset`, не очищает чужие таблицы, цепочки, rules или routes и не меняет существующую таблицу `inet filter`. Все операции адресуются по точным именам/priority/handle и сверяются с сохранённым manifest владельца.

## Идентификаторы

Окончательные значения должны пройти проверку конфликтов перед применением. Предлагаются:

| Объект | Значение | Назначение |
|---|---:|---|
| nft table | `inet uvg_tproxy` | Только правила приложения |
| route/output chain | `uvg_output` | Классификация локально созданных пакетов |
| filter/prerouting chain | `uvg_prerouting` | TPROXY после повторного входа через `lo` |
| filter/output chain | `uvg_guard` | Kill switch и явная блокировка ICMP |
| метка перехвата | `0x0100/0xff00` | Policy lookup в локальную таблицу |
| метка обхода | `0x0200/0xff00` | `SO_MARK` на исходящих сокетах Xray |
| routing table | `200` (`uvg_tproxy`) | Локальная доставка в `lo` |
| rule priority | `10000` | `fwmark 0x0100/0xff00 lookup 200` |
| transparent inbound | TCP/UDP `:12345` | Локальный transparent socket; точный bind проверяется интеграционно |

Маска обязательна: приложение не стирает младшие биты marks других подсистем. Перед применением оно проверяет отсутствие совпадающих nft-имён, table ID, rule priority и пересечения масок. При конфликте применение прекращается с диагностикой; автоматический выбор случайных значений запрещён, поскольку осложняет восстановление.

## Packet flow

### Локальный IPv4/IPv6 TCP и UDP

1. Пакет процесса попадает в nft chain типа `route`, hook `output`, priority `mangle`.
2. Пакет с bypass-mark `0x0200/0xff00` принимается без изменения. Эту метку Xray обязан ставить через outbound `sockopt.mark` (`SO_MARK`) на **все** свои исходящие сокеты, включая DNS и соединения к endpoint. UID процесса используется только как дополнительная защита от петли, не как основной контракт.
3. Служебные назначения и управление проходят direct по строгим исключениям ниже. DNS обрабатывается до общего LAN-исключения.
4. Остальной TCP/UDP получает только биты intercept-mark `0x0100/0xff00`; остальные биты сохраняются.
5. `ip rule` для соответствующей семьи выбирает table 200. В ней единственный принадлежащий приложению маршрут `local 0.0.0.0/0 dev lo` или `local ::/0 dev lo`. Пакет возвращается через `lo` в hook `prerouting`.
6. `uvg_prerouting` принимает только `iifname lo` и intercept-mark. Для TCP/UDP выполняется TPROXY на локальный transparent inbound и сохраняется intercept-mark. Пакет, не удовлетворяющий обоим условиям, цепочка не трогает.
7. Xray восстанавливает исходное назначение, отправляет поток через выбранный VLESS outbound, а его новый сокет получает bypass-mark и проходит обычную main routing table напрямую к endpoint.

Путь `local → output mark → policy route → lo → prerouting → TPROXY` должен быть доказан отдельно для TCP/UDP и IPv4/IPv6 в network namespace до VM. Если конкретное ядро/LXC не обеспечивает этот путь, применение запрещается; замена механизма требует нового review.

### Входящий трафик управления

Новые подключения к SSH `tcp/22` и Web UI `tcp/8443` приходят обычным INPUT-путём и не являются объектом локального Full Tunnel. Ответы сервера в OUTPUT исключаются по адресу управляющего клиента. Для текущей тестовой сессии обязательное исключение — `10.9.1.9/32`; оно создаётся до активации перехвата и живёт до завершения транзакции либо явного изменения списка управления.

Одного исключения по порту недостаточно: ответ SSH имеет source port 22, а ответ UI — source port 8443. Поэтому набор управления содержит подтверждённые remote IP/prefix и допускает direct ответы с `sport {22,8443}` только к ним. Произвольный трафик сервера к этому адресу не получает широкое исключение без отдельной настройки. Established-состояние не считается единственной защитой: адрес текущего peer фиксируется из сокета backend/SSH и snapshot до мутации.

### Перенаправляемый трафик

MVP не включает FORWARD и не перехватывает пакеты LAN-клиентов. Появление маршрутизации для других namespace, контейнеров или хостов требует отдельной модели доверия, anti-spoofing и review.

## Исключения и порядок правил

Правила классификации применяются в следующем порядке:

1. loopback, multicast, broadcast и IPv6 link-local остаются локальными;
2. bypass-mark Xray проходит direct;
3. трафик к зафиксированным IP активного VLESS endpoint и его порту проходит direct;
4. ответы SSH/UI к подтверждённым management peers проходят direct;
5. DNS TCP/UDP port 53 перехватывается либо блокируется по DNS-политике **до** общего исключения LAN;
6. разрешённые LAN/on-link prefixes проходят direct, кроме DNS;
7. TCP/UDP публичных назначений маркируется для TPROXY;
8. внешний ICMP/ICMPv6 блокируется с rate-limited журналированием; ICMP внутри разрешённых LAN/link-local исключений остаётся direct;
9. неизвестные L4-протоколы блокируются в kill switch и проходят direct в fail open только при неактивном/откаченном Full Tunnel.

LAN-набор строится не из всех RFC1918/ULA автоматически, а из валидированных connected routes до мутации и явных настроек администратора. Это сохраняет текущую сеть `10.5.2.0/24`, gateway `10.5.2.1` и доступ к UI, но не создаёт обход для произвольного частного адреса. Endpoint IP имеет отдельный узкий host-route через исходные gateway/interface в main table; IPv4/IPv6 адреса endpoint разрешаются и проверяются до мутации. Изменение DNS-ответа endpoint запускает новую Safe Apply, а не незаметное расширение набора.

## DNS и защита от утечек

DNS — часть транзакции. До включения сохраняются активная конфигурация резолвера, `/etc/resolv.conf` как ссылка/файл, состояние systemd-resolved, upstream и split-DNS. Ручная перезапись файла без учёта владельца запрещена.

В активном режиме все локальные TCP/UDP запросы на port 53, включая запросы к LAN DNS, перехватываются Xray раньше LAN bypass. Xray использует отдельный DNS outbound через VLESS. Его bootstrap-доступ к уже зафиксированным endpoint IP не зависит от этого DNS. DoT/DoH общего назначения являются обычным TCP/UDP-трафиком и также идут через VLESS; известный LAN DNS не получает исключение для 53. Если DNS через туннель не прошёл end-to-end проверку для A и AAAA, commit запрещён.

В fail open после полного удаления объектов Full Tunnel возвращается исходная DNS-конфигурация и исходный direct путь. В kill switch при потере Xray DNS наружу блокируется вместе с internet-трафиком; direct допускается только для endpoint bootstrap без DNS, management и LAN-трафика, не направленного на DNS port 53. Локальный кэш может отвечать, но не должен отправлять upstream в LAN или WAN мимо VLESS.

IPv6 разрешается только если transparent inbound, VLESS outbound, policy route, DNS AAAA и внешний IPv6 прошли проверки. Иначе до commit включается явная блокировка внешнего IPv6 во владельческой chain, сохраняя `::1`, link-local и согласованные management/LAN prefixes. Молчаливый direct IPv6 запрещён.

## Fail open и kill switch

Fail open означает атомарное удаление только владельческих nft table, policy rules и routes и восстановление DNS snapshot при отказе Xray/health check. После отката обычная main table снова обеспечивает direct internet. Оставлять intercept-mark без local route или local route без рабочего TPROXY нельзя.

Kill switch сохраняет TPROXY только пока Xray готов принимать трафик. При отказе Xray владельческая output guard блокирует внешний TCP/UDP, ICMP/ICMPv6 и прочие протоколы; разрешены loopback, подтверждённое управление, согласованный LAN и зафиксированный endpoint direct. Сам endpoint разрешён, чтобы Xray мог восстановиться. DNS port 53 direct не разрешается. Переход fail open ↔ kill switch является новой транзакцией Safe Apply.

Порядок атомарной публикации nft batch должен обеспечивать: сначала готовый Xray listener и policy route, затем правила перехвата; при снятии — сначала прекратить маркировку новых flows, затем удалить TPROXY/rules/routes после короткого bounded drain. Детали batch и rollback проверяются реализационным review.

## Safe Apply и независимый watchdog

Каждая транзакция имеет случайный ID, immutable manifest ожидаемых владельческих объектов и snapshot. Snapshot включает машиночитаемое состояние `ip -4/-6 rule`, всех затрагиваемых route tables, nft ruleset, resolver state, endpoint/LAN/management sets и сведения о текущем default gateway/interface. Для восстановления предпочтительна обратная операция по manifest и точечное восстановление изменённых владельческих объектов; сохранённый полный ruleset служит доказательством и аварийным материалом, но не должен вслепую заменять изменившийся чужой ruleset.

Snapshot пишется во временный файл в каталоге на том же filesystem, файл закрывается после `fsync`, затем выполняется atomic rename и `fsync` каталога. Manifest и checksum синхронизируются тем же способом. Без успешного чтения и проверки checksum продолжение запрещено.

До **первой** сетевой мутации backend создаёт независимый systemd watchdog (transient service/timer либо заранее установленный шаблон), передаёт ему только transaction ID, абсолютный путь snapshot и deadline. Watchdog использует отдельный минимальный rollback helper и не зависит от goroutine, PID или IPC backend. Он должен:

- проверить snapshot/checksum и записать durable armed-state;
- вернуть backend подтверждение `armed` с совпадающими transaction ID, deadline и snapshot hash;
- автоматически восстановить сеть по expiry, падению backend до commit, crash watchdog service, перезагрузке или обнаружению незавершённой транзакции при boot;
- выполнять rollback идемпотентно и сохранять результат для диагностики;
- принимать commit только для точного transaction ID и только после durable commit-record.

Таймер по умолчанию — 60 секунд, но отсчёт начинается от подтверждённого armed-state и не продлевается неявно. Systemd timer сам по себе недостаточен для reboot-сценария: незавершённый durable record обязан обрабатываться ранним recovery unit до запуска backend и до восстановления Full Tunnel. После reboot прежний перехват не активируется автоматически, пока recovery не подтвердил чистое исходное состояние; reconnect выполняется новой транзакцией.

Последовательность неизменна:

1. validate capabilities, конфигурацию Xray, endpoint IP, конфликты идентификаторов и обе IP-семьи;
2. получить management context и явный ACK пользователя из текущей аутентифицированной UI-сессии: показать peer IP, SSH/UI порты, LAN prefixes, endpoint IP, режим fail open/kill switch и deadline;
3. создать и durable-сохранить snapshot/manifest;
4. arm независимый watchdog и проверить его подтверждение;
5. только теперь запустить подготовленный Xray listener и выполнить первую сетевую мутацию;
6. проверить через туннель TCP, UDP, DNS A/AAAA, внешний IPv4 и при поддержке IPv6;
7. параллельно подтвердить прямой endpoint path и доступность UI; management probe должен прийти от подтверждённого peer либо пользователь выполняет явный ACK через существующее соединение после применения;
8. записать durable commit, получить подтверждение watchdog и лишь затем снять rollback deadline;
9. при любой ошибке или отсутствии ACK — rollback.

HTTP-ответ «успешно» до пункта 8 запрещён. Потеря WebSocket/HTTP-сессии не доказывает потерю управления, но отсутствие подтверждения до deadline всегда приводит к rollback. SSH проверяется отдельным probe/сохранением активной сессии; UI ACK не заменяет SSH acceptance на VM.

## Восстановление и сосуществование

Rollback удаляет объекты по transaction manifest и восстанавливает только состояние, которое приложение действительно изменило. Перед каждым удалением проверяются тип, имя, handle/priority, mark mask и ожидаемое содержимое. Если чужая сторона изменила объект, watchdog не выполняет глобальный flush: он снимает перехват безопасным владельческим batch, восстанавливает connectivity и сообщает конфликт для ручного разбора.

На boot ранний recovery unit выполняется до backend/Xray и проверяет durable transaction journal. Состояние `armed` без подтверждённого commit всегда откатывается; повреждённый/нечитаемый journal переводит систему в fail-open recovery: владельческая output-marking chain отключается первой, затем точечно удаляются узнаваемые владельческие rule/route/table, чужие объекты не трогаются. Если конфликт не позволяет доказать владение, recovery сохраняет управление и сообщает критическую ошибку вместо глобального восстановления ruleset.

При штатном stop/restart fail open снимает Full Tunnel. Kill switch переживает restart только если его durable-состояние подтверждено и recovery unit установлен; после reboot Full Tunnel включается новой транзакцией. Обновление/удаление пакета сначала выполняет подтверждённый rollback.

## Обязательная проверка до реализации и приёмки

Сначала создаются root-tagged тесты в изолированном network namespace: точный путь OUTPUT→lo→PREROUTING, TCP/UDP, IPv4/IPv6, сохранение чужих mark bits, отсутствие петли Xray с `SO_MARK`, DNS до LAN bypass, ICMP block, fail open и kill switch. Отдельно моделируются конфликт table/rule/mark и изменение чужого ruleset во время транзакции.

Затем на disposable Ubuntu VM проверяются: активная SSH-сессия и новое SSH-подключение с `10.9.1.9`, UI `8443`, endpoint direct, смена exit IP, DNS A/AAAA без LAN/WAN leak, IPv6 tunnel либо block, падение Xray, backend и watchdog, expiry без ACK, reboot между каждым шагом apply/commit, повторный rollback и uninstall. Для LXC `10.5.2.70` тест допускается только после snapshot на стороне Proxmox и подтверждения out-of-band console; доступ к контейнеру по SSH не считается независимым каналом восстановления.

До успешного отдельного review этого документа и результатов namespace-тестов запрещены сетевой код и любые мутации тестовой VM.

## Вопросы архитектурного review

### Первое review ведущего архитектора

Пакетный путь и порядок durable snapshot → подтверждённый независимый watchdog → первая мутация приняты как основа. Сетевой код и применение пока не одобрены: требуется уточнить следующие условия и затем проверить их в namespace.

- Перехват DNS к 10.5.2.1 не может просто отправлять прежнее частное назначение через удалённый VLESS. Нужен явно заданный публичный резолвер через VPN либо DNS outbound с переназначением, а также проверка TCP/UDP DNS и отсутствия fallback в LAN. Локальные имена и split DNS должны быть отдельной явной политикой.
- Нельзя блокировать необходимые ICMP ошибки PMTU для внешнего соединения Xray. Разрешаются связанные ошибки для bypass-соединения; пользовательский внешний ICMP не выпускается напрямую. IPv6 Packet Too Big и neighbour discovery учитываются отдельно.
- Отсутствие TUN не запрещает VLESS TPROXY в LXC. Проверки capabilities и восстановление обязательны; snapshot Proxmox и консоль рекомендованы как дополнительная защита, их отсутствие само по себе не отменяет ранее предоставленное пользователем разрешение тестировать сервер.
- При неподдерживаемом IPv6 можно использовать явно проверенную блокировку внешнего IPv6 вместо непроверенного ip6 TPROXY. LAN/link-local и управление сохраняются.
- Фиксированные идентификаторы допустимы только после проверки конфликтов. Watchdog deadline должен включать реальную проверку и management ACK; предлагается 120 секунд без неявного продления. Commit возможен лишь после ACK через UI, проверки второго SSH-сеанса и успешного независимого watchdog.

Следующее review должно проверить конкретные nft batches, DNS-конфигурацию, recovery unit и результаты namespace до применения основной сети.

1. Подтверждаем ли фиксированные mark/table/rule identifiers или нужен выделяемый администратором диапазон?
2. Разрешаем ли UDP через Xray в MVP; если нет, его следует явно блокировать, а не выпускать direct.
3. Какой независимый management probe доступен на production-хосте кроме ACK текущей UI-сессии, и обязателен ли второй SSH-сеанс?
4. Следует ли kill switch переживать reboot? Предложение: да, только как минимальная блокирующая policy recovery unit; Full Tunnel после reboot включать новой транзакцией.
5. Какие LAN prefixes пользователь подтверждает помимо connected routes, и должен ли трафик к ним целиком обходить туннель кроме DNS?
6. Принимается ли блокировка внешнего ICMP/ICMPv6 в Full Tunnel, учитывая потерю PMTU diagnostics; proxyable альтернативы у Xray нет.
7. Достаточен ли 60-секундный deadline для проверки обеих IP-семей и ручного management ACK на целевой VM?

## Первичные источники

- [Linux kernel: Transparent proxy support](https://docs.kernel.org/networking/tproxy.html) — TPROXY, transparent socket и обязательная policy routing.
- [Linux kernel: IP sysctl](https://docs.kernel.org/networking/ip-sysctl.html) — параметры IPv4/IPv6 и routing behaviour.
- [nftables man page](https://netfilter.org/projects/nftables/manpage.html) — hooks, chain types, priorities, marks и ruleset operations.
- [Xray: transparent proxy](https://xtls.github.io/en/document/level-2/transparent_proxy/transparent_proxy.html) — transparent inbound и предотвращение routing loop.
- [Xray: socket options](https://xtls.github.io/en/config/transports/sockopt.html) — outbound mark и TPROXY socket option.
- [systemd.timer](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html) и [systemd.service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html) — timer/service lifecycle; durable recovery поверх них является требованием проекта.
- [ip-rule(8)](https://man7.org/linux/man-pages/man8/ip-rule.8.html) и [ip-route(8)](https://man7.org/linux/man-pages/man8/ip-route.8.html) — policy rules, fwmark selectors и local routes.
