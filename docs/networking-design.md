# Full Tunnel VLESS: проект сети MVP

Статус: **реализация одобрена после независимого review; staged namespace и VM acceptance пройдены, публикация проверяется отдельно**. Пользователь уже разрешил испытания на доступной Ubuntu VM, поэтому дополнительных формальных разрешений для согласованных тестов не требуется. В рамках подготовки документа код, маршруты, nftables, DNS и VM не изменялись.

## Объём MVP

MVP перехватывает только IPv4 TCP/UDP, созданный процессами самого host, и направляет его в VLESS через Xray TPROXY. `FORWARD`, контейнеры, Docker, AWG, kill switch и IPv6 TPROXY остаются следующими этапами. Режим MVP — только fail open. После reboot Full Tunnel автоматически не восстанавливается: ранний recovery сначала приводит систему к проверенному direct-состоянию, а новое подключение требует новой Safe Apply.

Внешний IPv6 в активном Full Tunnel блокируется владельческими правилами, пока отдельное ревью не подтвердит IPv6 TPROXY end-to-end. `::1`, link-local, multicast, connected LAN и подтверждённое управление продолжают работать direct. Неизвестные внешние протоколы кроме TCP/UDP блокируются. Необходимые ICMP/ICMPv6 ошибки PMTU разрешаются для bypass-потоков Xray и управления; произвольный внешний ICMP не выпускается direct.

Приложение не меняет sysctl, `/etc/resolv.conf`, конфигурацию systemd-resolved или чужие объекты. Оно не выполняет `nft flush ruleset`, не очищает чужие routes/rules и не заменяет полный ruleset из snapshot.

## Фиксированные идентификаторы

| Объект | Значение |
|---|---|
| nft table | `inet uvg_tproxy` |
| output chain | `uvg_output`, type `route`, hook `output`, priority `mangle` |
| prerouting chain | `uvg_prerouting`, type `filter`, hook `prerouting`, priority `mangle` |
| intercept mark | `0x0100/0xff00` |
| Xray bypass `SO_MARK` | `0x0200/0xff00` |
| policy table | IPv4 table `200` |
| policy rule | priority `10000`, `fwmark 0x0100/0xff00 lookup 200` |
| TPROXY inbound | `127.0.0.1:12345`, TCP и UDP |
| DNS inbound | `127.0.0.1:12346`, TCP и UDP |

До prepare завершается проверка конфликтов имён, family, hooks, priority, table 200, rule priority 10000 и пересечения mark mask. Любой конфликт прекращает операцию с диагностикой. Случайный выбор других идентификаторов и присвоение чужих объектов запрещены. При установке mark меняются только биты `0xff00`; остальные биты skb mark сохраняются.

## Данные, фиксируемые до мутации

Backend до применения:

1. читает `ip -4 rule`, main/local routes, connected routes, default gateway/interface и весь nft ruleset без изменений;
2. определяет точный remote IP текущего management peer из соединения, отдельно фиксирует SSH `sport 22` и UI `sport 8443` для ответов;
   непосредственно перед вооружением watchdog повторно читает установленные TCP-соединения управления и сохраняет в неизменяемом manifest их полные tuple `remote IP + local server port + remote client port`. В выборку входят только адреса из подготовленного `management_peers` и только local port 22/8443. Эти значения не входят в hash публичного Plan: Plan описывает политику захвата, а подписанный checksum manifest фиксирует фактические соединения в момент Apply;
3. разрешает hostname VLESS endpoint через исходную сеть и фиксирует один проверенный IPv4 вместе с точным TCP/UDP port узла; hostname после применения не используется;
4. создаёт host route `/32` к endpoint через исходные gateway/interface;
5. формирует набор connected LAN IPv4/IPv6, loopback, link-local, multicast и broadcast;
6. проверяет готовность Xray, DNS inbound и возможность поставить `SO_MARK=0x0200` на **все** outbounds Xray, включая VLESS, DNS и probe.

Изменение endpoint IP, management peer, LAN-наборов или исходного gateway требует новой Safe Apply. Endpoint route не заменяет `SO_MARK`: нужны обе защиты от петли.

## Порядок классификации

Порядок правил принципиален:

1. для IPv6 сначала разрешаются только loopback, connected LAN, link-local, multicast, точный management reply и связанные PMTU/ND; затем весь внешний IPv6 блокируется **до** проверки bypass mark, поэтому `SO_MARK` не создаёт IPv6-утечку;
2. IPv4-пакеты с `meta mark & 0xff00 == 0x0200` идут direct;
3. ответы TCP-соединений управления, существовавших до установки nft table, идут direct по полному сохранённому tuple без зависимости от conntrack. Затем ответы новых management-соединений проходят по точному peer, `tcp sport {22,8443}`, `ct state established,related` и `ct direction reply`. Широкого bypass всего трафика к peer нет;
4. зафиксированная пара IPv4+port активного узла Xray идёт direct только для соответствующего TCP/UDP; исключения всего endpoint IP нет;
5. DNS IPv4 TCP/UDP `dport 53` обрабатывается **до** LAN/loopback исключений и получает отдельный redirect в DNS inbound;
6. loopback, connected LAN, link-local, multicast и broadcast идут direct, кроме уже перехваченного DNS;
7. остальной IPv4 TCP/UDP получает intercept mark `0x0100/0xff00`;
8. внешний IPv4 non-TCP/UDP блокируется, кроме связанных PMTU ICMP ошибок для bypass/management.

Сохранение inbound-служб обеспечивается узкими ответными исключениями. Для соединения, существовавшего до первого появления conntrack hooks, направление conntrack может быть определено неверно, поэтому Apply фиксирует полный tuple и ставит exact-match раньше conntrack-правил. Новый входящий SSH/UI проходит обычный INPUT, а его OUTPUT-ответ совпадает с точным peer IP и source port; для него `ct direction reply` остаётся дополнительным обязательным условием. Один адрес peer, один source port или один `ct established` не дают bypass. Новое произвольное соединение к тому же peer по другому tuple перехватывается обычной Full Tunnel policy.

## Концептуальный nftables batch

Ниже показана семантика, а не готовый исполняемый файл. Реализация генерирует один проверенный `nft --check` batch с конкретными sets и без shell-интерполяции пользовательских строк.

```nft
table inet uvg_tproxy {
  set endpoint_tcp4   { type ipv4_addr . inet_service; flags constant; elements = { ENDPOINT4 . ENDPOINT_PORT } }
  set endpoint_udp4   { type ipv4_addr . inet_service; flags constant; elements = { ENDPOINT4 . ENDPOINT_PORT } }
  set management4     { type ipv4_addr; flags constant; elements = { MGMT4 } }
  set management6     { type ipv6_addr; flags constant; elements = { MGMT6 } }
  set management_flows4 { type ipv4_addr . inet_service . inet_service; flags constant; elements = { REMOTE4 . LOCAL_SERVICE . REMOTE_CLIENT... } }
  set management_flows6 { type ipv6_addr . inet_service . inet_service; flags constant; elements = { REMOTE6 . LOCAL_SERVICE . REMOTE_CLIENT... } }
  set connected4      { type ipv4_addr; flags interval,constant; elements = { LAN4... } }
  set connected6      { type ipv6_addr; flags interval,constant; elements = { LAN6... } }

  chain uvg_output {
    type route hook output priority mangle; policy accept;

    # IPv6 guard обязан находиться раньше SO_MARK bypass.
    meta nfproto ipv6 ip6 daddr . tcp sport . tcp dport @management_flows6 return
    meta nfproto ipv6 ip6 daddr @management6 tcp sport { 22, 8443 } ct state established,related ct direction reply return
    meta nfproto ipv6 ip6 daddr @connected6 return
    meta nfproto ipv6 ip6 daddr ::1 return
    meta nfproto ipv6 ip6 daddr fe80::/10 return
    meta nfproto ipv6 ip6 daddr ff00::/8 return
    meta nfproto ipv6 meta l4proto ipv6-icmp ct state related return
    meta nfproto ipv6 drop

    meta nfproto ipv4 meta mark & 0xff00 == 0x0200 return
    ip daddr . tcp sport . tcp dport @management_flows4 return
    ip daddr @management4 tcp sport { 22, 8443 } ct state established,related ct direction reply return
    ip daddr . tcp dport @endpoint_tcp4 return
    ip daddr . udp dport @endpoint_udp4 return

    # DNS стоит раньше loopback/LAN. Реализация задаёт route-to-lo mark,
    # а prerouting отправляет такие пакеты в отдельный DNS inbound 12346.
    meta nfproto ipv4 meta l4proto { tcp, udp } th dport 53 meta mark set ((meta mark & 0xffff00ff) | 0x0100) return

    ip daddr @connected4 return
    ip daddr 127.0.0.0/8 return
    ip daddr 169.254.0.0/16 return
    ip daddr 224.0.0.0/4 return
    ip daddr 255.255.255.255 return

    meta nfproto ipv4 meta l4proto { tcp, udp } meta mark set ((meta mark & 0xffff00ff) | 0x0100) return
    meta nfproto ipv4 ip protocol icmp ct state related return
    meta nfproto ipv4 drop
  }

  chain uvg_prerouting {
    type filter hook prerouting priority mangle; policy accept;
    iifname "lo" meta mark & 0xff00 == 0x0100 meta l4proto { tcp, udp } th dport 53 tproxy ip to 127.0.0.1:12346 accept
    iifname "lo" meta mark & 0xff00 == 0x0100 meta l4proto tcp tproxy ip to 127.0.0.1:12345 accept
    iifname "lo" meta mark & 0xff00 == 0x0100 meta l4proto udp tproxy ip to 127.0.0.1:12345 accept
  }
}
```

`th dport` применяется только после проверки TCP/UDP. После policy return исходный destination port сохраняется, поэтому первое правило `prerouting` отправляет весь TCP/UDP port 53 в DNS inbound, а следующие правила обслуживают остальной transparent traffic. Тест обязан подтвердить этот порядок для loopback, LAN и публичного исходного DNS destination.

Владельческая table не создаёт INPUT chain: входящие ICMP Fragmentation Needed / ICMPv6 Packet Too Big для bypass-соединения Xray продолжают доходить по существующей host policy. `ct state related` выше сохраняет необходимые исходящие related ошибки, а link-local/multicast исключения сохраняют IPv6 ND. Acceptance отдельно доказывает PMTU для VLESS endpoint; если внешний firewall host уже блокирует обязательный ICMP, apply прекращается с диагностикой вместо изменения чужой policy.

Policy routing создаётся точечно:

```text
ip -4 rule add priority 10000 fwmark 0x0100/0xff00 lookup 200
ip -4 route add local 0.0.0.0/0 dev lo table 200
```

Сначала создаются listener и route/rule, затем атомарно публикуется nft batch. При снятии сначала отключается новая маркировка, затем после bounded drain удаляются владельческие rule/route/table. Чужие объекты никогда не flush-ятся.

## DNS через VLESS

`/etc/resolv.conf` не переписывается. Любой локальный IPv4 DNS-запрос, даже к loopback stub `127.0.0.53` или LAN resolver, перехватывается до loopback/LAN direct-исключений и попадает в фиксированный dokodemo-door inbound. Это также перехватывает исходящий upstream port 53 самого локального stub resolver и не позволяет ему сделать direct fallback. Назначение принудительно заменяется на публичный `1.1.1.1:53`, поэтому исходный частный адрес не отправляется удалённому серверу.

Концептуальный фрагмент Xray:

```json
{
  "inbounds": [
    {
      "tag": "uvg-dns-in",
      "listen": "127.0.0.1",
      "port": 12346,
      "protocol": "dokodemo-door",
      "settings": {
        "address": "1.1.1.1",
        "port": 53,
        "network": "tcp,udp",
        "followRedirect": false
      },
      "streamSettings": { "sockopt": { "tproxy": "tproxy" } }
    }
  ],
  "routing": {
    "rules": [
      { "type": "field", "inboundTag": ["uvg-dns-in"], "outboundTag": "vless" }
    ]
  },
  "outbounds": [
    {
      "tag": "vless",
      "streamSettings": { "sockopt": { "mark": 512 } }
    }
  ]
}
```

Фактическая конфигурация объединяется с существующим VLESS outbound и проходит `xray run -test`. Все outbounds, включая direct/probe/DNS bootstrap, обязаны иметь mark `512` (`0x200`). Commit требует успешных UDP и TCP DNS-запросов A и AAAA через этот inbound, проверки ответа и отсутствия пакетов port 53 на исходном WAN/LAN пути. Ответ AAAA проверяет DNS, но внешний IPv6-трафик по полученному адресу остаётся заблокированным.

## Независимый watchdog

Watchdog — отдельный минимальный бинарник и systemd template instance, а не goroutine backend. Backend передаёт transaction ID; остальные данные watchdog читает из root-only durable manifest.

Концептуальный unit:

```ini
[Unit]
Description=Ubuntu VPN Gateway rollback watchdog %i
Before=ubuntu-vpn-gateway.service

[Service]
Type=simple
ExecStart=/usr/local/lib/ubuntu-vpn-gateway/uvg-watchdog monitor %i
ExecStopPost=/usr/local/lib/ubuntu-vpn-gateway/uvg-watchdog rollback-if-needed %i
Restart=no
```

Manifest содержит snapshot hash, deadline, boot ID из `/proc/sys/kernel/random/boot_id`, backend PID + `/proc/PID/stat` starttime, Xray PID + starttime, ожидаемые владельческие объекты и исходное состояние. PID без proc starttime и совпадающего boot ID не считается идентичностью из-за повторного использования PID и перезагрузки. Watchdog:

- проверяет checksum snapshot и manifest, durable записывает `armed`, делает `fsync` файла и каталога и только затем отправляет backend ACK с transaction ID и hash;
- выдаёт lease ровно на 120 секунд от armed ACK без неявного продления; heartbeat подтверждает живость, но никогда не сдвигает pending deadline;
- следит за обоими PID+starttime, своим deadline и целостностью manifest;
- при expiry, исчезновении/подмене процесса, crash/restart watchdog или неподтверждённой транзакции выполняет идемпотентный rollback;
- после durable commit остаётся активным постоянно: deadline ограничивает только pending, а потеря backend/Xray/watchdog всегда снимает Full Tunnel в fail open;
- `ExecStopPost` всегда вызывает `rollback-if-needed`; helper сверяет transaction ID/hash и ничего не меняет для уже безопасно завершённой записи;
- сохраняет durable результат rollback для раннего boot recovery.

Отдельный early-boot recovery unit запускается до backend и Xray. Любая оставшаяся `armed`, `pending`, `committed` или нечитаемая запись сначала отключает владельческую output-marking chain, затем точечно снимает узнаваемые владельческие nft/rule/route объекты. При конфликте чужие объекты не удаляются и глобальный ruleset не восстанавливается. Backend не запускается, пока recovery не подтвердит direct-состояние.

## Safe Apply и UX

Операция имеет состояния:

1. **Prepare** — read-only discovery, resolve endpoint, conflict checks, `xray -test`, генерация nft batch и snapshot. DNS snapshot содержит hash и тип `/etc/resolv.conf`, а также read-only status systemd-resolved; DNS-файлы и настройки не изменяются.
2. **Display** — UI показывает management peer, SSH/UI ports, connected LAN, endpoint IPv4, blocked IPv6, DNS target, точный deadline и ожидаемые проверки.
3. **Explicit apply** — пользователь отдельно подтверждает показанный план; prepare само ничего не применяет.
4. **Arm** — durable snapshot + manifest + hashes записываются atomic rename с `fsync`; watchdog возвращает проверенный armed ACK **до первой сетевой мутации**.
5. **Apply** — запускаются Xray listeners. Watchdog уже вооружён и подтверждён до любых `ip route add`, `ip rule add` или `nft` mutation; затем применяются route/rule и атомарный nft batch.
6. **Pending checks** — backend проверяет TCP, UDP, DNS A/AAAA, exit IPv4, endpoint direct, внешний IPv6 block, отсутствие DNS leak и сохранность management.
7. **Confirm** — существующая аутентифицированная UI-сессия отправляет одноразовый peer-bound token. Token связан с transaction ID, точным remote IP, session ID и deadline; подтверждение с другого peer/session не принимается. Отдельно проверяется существующий или новый SSH flow.
8. **Commit** — только после всех checks и peer-bound confirmation durable записывается commit и watchdog подтверждает его. До этого API возвращает pending, а не success.
9. Любая ошибка, потеря процессов, отсутствие confirm или истечение 120 секунд запускает rollback.

Snapshot включает `ip -4/-6 rule`, затрагиваемые route tables, весь nft ruleset как диагностическое доказательство, точный manifest владельческих объектов, resolver state без изменения, endpoint/LAN/management sets и исходный gateway/interface. Rollback использует manifest и обратные точечные операции. Полный snapshot никогда не загружается поверх текущего чужого ruleset.

## Acceptance до реализации и перед выпуском

Сначала независимый reviewer проверяет этот документ, nft/DNS/watchdog contracts и фиксирует замечания. После устранения основной агент записывает approval. Только затем допускаются код и тестовые мутации.

Namespace/root-тесты:

- конфликт каждого фиксированного имени, table, priority и mark mask приводит к abort до мутации;
- IPv4 TCP и UDP проходят `OUTPUT → mark → table 200 → lo → PREROUTING → TPROXY`;
- все outbounds Xray имеют bypass mark, endpoint использует зафиксированный `/32`, петли нет;
- management reply требует точный peer, `sport 22/8443` и `ct direction reply`; другие потоки к peer перехватываются;
- connected LAN, loopback, link-local и multicast direct, но DNS port 53 перехватывается раньше;
- UDP/TCP A и AAAA идут к `1.1.1.1:53` через VLESS; на LAN/WAN нет direct DNS;
- внешний IPv6 и IPv4 non-TCP/UDP блокируются, необходимые ND/related PMTU работают;
- чужие routes/rules/nft/sysctl не изменяются; изменение чужого объекта во время транзакции не вызывает flush;
- snapshot corruption, watchdog/backend/Xray crash, PID reuse, lease expiry и reboot на каждом шаге приводят к идемпотентному fail-open recovery;
- commit без peer-bound token, с другим peer или после deadline отклоняется; после commit watchdog остаётся жив постоянно, а 120 секунд ограничивают только pending;
- после reboot Full Tunnel не включается автоматически.

На Ubuntu VM дополнительно проверяются активная и новая SSH-сессия, UI `8443`, direct endpoint, TCP/UDP приложения, внешний IPv4 через VLESS, DNS A/AAAA, отсутствие IPv6/DNS leak, падение Xray/backend/watchdog, reboot между arm/apply/check/commit и повторный rollback. На текущей VM доступен IPv4, а IPv6 представлен только link-local: acceptance требует сохранения link-local и доказанной блокировки попыток внешнего IPv6, но не заявляет проверенный IPv6 internet. Пользовательское разрешение на эти согласованные VM-тесты уже получено; snapshot и out-of-band console рекомендуются как дополнительные меры; обязательная программная защита — независимый watchdog и точечный откат. LXC без TUN не используется как доказательство AWG, но может применяться для безопасных read-only и namespace-проверок TPROXY.

## Следующие этапы

Kill switch, Docker/namespace routing, AWG default route, IPv6 TPROXY, автоматический reconnect после boot и failover требуют отдельных дизайнов и review. Они не должны расширять MVP скрытыми флагами.

## Первичные источники

- [Linux kernel: Transparent proxy support](https://docs.kernel.org/networking/tproxy.html)
- [nftables man page](https://netfilter.org/projects/nftables/manpage.html)
- [Xray transparent proxy](https://xtls.github.io/en/document/level-2/transparent_proxy/transparent_proxy.html)
- [Xray socket options](https://xtls.github.io/en/config/transports/sockopt.html)
- [systemd.service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html)
- [ip-rule(8)](https://man7.org/linux/man-pages/man8/ip-rule.8.html) и [ip-route(8)](https://man7.org/linux/man-pages/man8/ip-route.8.html)

## Результат отдельного review и решение ведущего агента

Review network_design выявил endpoint pinning, DNS rewrite, SO_MARK, management scope, IPv6 guard и постоянный lifecycle watchdog как условия допуска. Условия включены в этот документ; код разрешён, VM apply до namespace-проверки запрещён. В реализации локальный production Xray также получает SO_MARK 512; отдельный Full Tunnel Xray слушает только transparent/DNS inbounds. DNS TPROXY правило завершается accept, иначе последующее правило могло бы перенаправить его в другой inbound. Снятие атомарно удаляет владельческую nft table до точечного удаления policy rule/route, чужие таблицы не меняются. Владелец route отмечается протоколом 186; ownership table отмечается transaction ID в comment. Независимый reviewer и ведущий агент не разрешали kill switch, Docker, AWG или автоматический reconnect этим review.

### Уточнение DNS после первой VM-проверки

До повторного применения уточнён DNS-путь: `followRedirect=false` в pinned Xray v26.3.27 не создаёт forged UDP reply с адреса исходного DNS, поэтому connected UDP-клиент игнорирует ответ transparent listener. DNS inbound должен использовать `followRedirect=true`, сохранять original destination для ответа и направлять поток через отдельный freedom outbound с `redirect=1.1.1.1:53` и обязательным `proxySettings.tag=proxy`. Этот outbound не получает самостоятельный direct dial: фактическое соединение выполняет VLESS outbound с SO_MARK 512. Freedom override убирает UDP source metadata, поэтому dokodemo возвращает ответ от исходного DNS IP:53. Контракт DNS через VPN, порядок nft, watchdog и отсутствие записи resolv.conf сохраняются. Ведущий агент отдельно review разрешил эту коррекцию на основании исходников именно v26.3.27 до повторной VM-мутации; требуются конфигурационные тесты и реальный UDP/TCP запрос к public и LAN DNS без WAN leak.

Источники: [dokodemo v26.3.27](https://github.com/XTLS/Xray-core/blob/v26.3.27/proxy/dokodemo/dokodemo.go), [freedom v26.3.27](https://github.com/XTLS/Xray-core/blob/v26.3.27/proxy/freedom/freedom.go).

### Привилегия UDP reply без изменения sysctl

Namespace приёмка повторена с источником на dummy-интерфейсе и VM-подобными rp_filter=2/accept_local=0: TCP/UDP/DNS и полный rollback прошли. Production sysctl остаются без изменения. Отдельный transient systemd-тест под теми же ограничениями доказал отказ transparent bind к исходному UDP-порту 53 (errno13) при CAP_NET_ADMIN/CAP_NET_RAW и успех с дополнением CAP_NET_BIND_SERVICE. Pinned Xray FakeUDP использует syscall.Bind к исходному порту ответа. Поэтому backend и наследующий его Full Tunnel Xray требуют минимальное дополнение CAP_NET_BIND_SERVICE; watchdog/recovery такой привилегии не требуют. Ведущий агент review разрешил дополнение capability до следующего VM apply. CAP_SYS_PTRACE и изменение ip_unprivileged_port_start не добавляются.

Источник: [Xray FakeUDP v26.3.27](https://github.com/XTLS/Xray-core/blob/v26.3.27/proxy/dokodemo/fakeudp_linux.go).

## 2026-10-06 — отдельное review окна management-соединений

Независимый review GPT-6 Luna установил, что снятие management tuple до arm не закрывает окно новых SSH/UI-соединений между snapshot и публикацией conntrack hooks. Также local server IP должен входить в полный 4-tuple. До реализации ведущий агент принимает staged-контракт:

1. После conflict checks, durable snapshot и подтверждённого независимого armed watchdog создать только владельческую nft table с постоянными base chains uvg_output (route/output/mangle) и uvg_prerouting (filter/prerouting/mangle). Первоначальные rules только активируют conntrack/counter; нет mark/drop/tproxy/маршрутных изменений. Чужие notrack, ct zones, flow offload и marks — конфликт до любых мутаций.
2. При уже зарегистрированных hooks снять ss state all, включить SYN-RECV и активные закрывающие состояния; исключить LISTEN и SYN-SENT. Сохранить только локальные service ports22/8443 и подготовленные peers, полный tuple local IP + remote IP + local server port + remote client port. Новые входящие соединения после этого снимка уже получают корректное conntrack направление.
3. Write-once durable flows.json и seal с transaction ID, исходным неизменяемым manifest hash и flow hash подтверждает независимый watchdog через durable ACK. Backend не меняет routes/rules и не публикует intercept без точного ACK и действующего deadline. Watchdog продолжает проверять seal после commit, повреждение/утрата приводит к rollback.
4. Под network.lock повторно проверить lease, owner/child, seal/ACK. Затем применить только владельческие endpoint/lo route и fwmark rule; атомарным nft batch наполнить уже существующие base chains. Chain/table/hooks не удаляются и не пересоздаются между tracking и intercept. Staged table распознаётся как принадлежащая текущей транзакции; старый conflicts checker не должен ошибочно считать её чужим конфликтом.
5. Rollback удаляет всю владельческую table, включая tracking-only стадию. Если delete не удался, best-effort очищаются обе output/prerouting chains; route/rule dependencies и Xray разрешено снимать лишь после доказанного отключения intercept. При невозможности очистки сохраняются зависимости и процесс, state=rollback_failed, требуется повторное recovery. Это ограничение явно отличает partial failure от гарантированного завершённого fail-open.

TCP flags/source-port fallback без точного tuple review отклонил. Sysctl остаются неизменными. Этот review разрешает реализацию до повторной VM-приёмки; обязательны namespace проверки tracking→flow ACK→atomic publish→точечный rollback, исходная/новая SSH и старый HTTPS Apply reply, новая сессия во время staging и watchdog/crash/lease tests. Решение ведущего агента: APPROVED IMPLEMENTATION, acceptance pending.

Дополнительное review восстановления подтвердило: boot oneshot RemainAfterExit не обеспечивает повторный recovery при каждом backend restart. Каждый запуск backend должен отдельно вызвать idempotent recovery до serve; unresolved journal не должен показываться как disabled или теряться при retry. Восстановление не изменяет не принадлежащие транзакции объекты и ошибку не скрывает.

## Приёмка утверждённого staged-контракта — 2026-10-06

Независимые GPT-6 Luna review подтвердили staged hooks/seal/ACK, проверку идентичности nft JSON и восстановление после restart. До основной сети пройдены isolated namespace tests: старый 4-tuple и новый TCP между capture/publish, IPv4 TCP/UDP, DNS-before-LAN, bypass, IPv6 guard, точечный rollback. Дополнительные 10/10 owner/Xray fault cases на armed/tracking/sealing/sealed/pending сохраняют чужие nft/rule/routes.

На предоставленной Ubuntu LXC проверены исходный Apply reply, old/new SSH, HTTPS, реальные TCP/UDP и DNS A/AAAA без WAN port53, VPN exitIPv4, active process faults, lease120 expiry, reboot и обновление active Full Tunnel. После reboot/update Full Tunnel disabled, исходная сеть восстановлена, ключи/сертификат/DNS и 3 узла сохранены. Production sysctl не менялись. На VM нет глобального IPv6/default IPv6 route: блокировка реального внешнего IPv6 доказана синтетическим трафиком в isolated namespace, а в основной сети проверена структура guard. Public release/update проверяется отдельно в work-log; Docker/AWG/killswitch/IPv6 tunnel вне MVP.

Публичный v0.2.0 опубликован и установлен штатным updater при active Full Tunnel. Повторная приёмка публичного бинарника подтвердила Apply/Confirm, TCP/UDP, DNS, включая stub 127.0.0.53 (A/AAAA UDP/TCP), и явный rollback. Итоговые результаты и ссылки CI/release записаны в work-log.md.
