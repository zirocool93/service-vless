import { useEffect, useState } from 'react'
import { api } from './api'
import type { ConnectionStatus, PreparedTunnel, TunnelStatus } from './api'

export default function FullTunnel({ csrf }: { csrf: string }) {
  const [status, setStatus] = useState<TunnelStatus>()
  const [connection, setConnection] = useState<ConnectionStatus>()
  const [prepared, setPrepared] = useState<PreparedTunnel>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    let stopped = false
    let timer: ReturnType<typeof setTimeout>
    async function refresh() {
      try {
        const [tunnel, node] = await Promise.all([api<TunnelStatus>('/api/v1/tunnel/status'), api<ConnectionStatus>('/api/v1/connections/status')])
        if (!stopped) { setStatus(tunnel); setConnection(node) }
      } catch (err) { if (!stopped) setError(err instanceof Error ? err.message : 'Не удалось прочитать состояние') }
      finally { if (!stopped) timer = setTimeout(() => void refresh(), 2000) }
    }
    void refresh()
    const clock = setInterval(() => setNow(Date.now()), 1000)
    return () => { stopped = true; clearTimeout(timer); clearInterval(clock) }
  }, [])
  async function action(kind: 'prepare' | 'apply' | 'confirm' | 'disable') {
    setBusy(true); setError('')
    try {
      if (kind === 'prepare') {
        setPrepared(await api<PreparedTunnel>('/api/v1/tunnel/prepare', { method: 'POST', body: JSON.stringify({ node_id: connection?.active_node_id }) }, csrf))
        setStatus(await api<TunnelStatus>('/api/v1/tunnel/status'))
      } else {
        const body = kind === 'disable' ? {} : { transaction_id: prepared?.plan.transaction_id, hash: prepared?.plan.hash, token: prepared?.token }
        setStatus(await api<TunnelStatus>(`/api/v1/tunnel/${kind}`, { method: 'POST', body: JSON.stringify(body) }, csrf))
        if (kind === 'confirm' || kind === 'disable') setPrepared(undefined)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Операция не выполнена')
      try { setStatus(await api<TunnelStatus>('/api/v1/tunnel/status')) } catch { /* Основная ошибка уже показана. */ }
    } finally { setBusy(false) }
  }
  const inProgress = ['armed', 'tracking', 'sealing', 'sealed', 'pending'].includes(status?.state ?? '')
  const awaitingConfirmation = status?.state === 'pending'
  const active = status?.state === 'active'
  const recoveryRequired = status?.state === 'rollback_failed' || status?.state === 'failed'
  const plan = prepared?.plan ?? status?.plan
  const remaining = status?.deadline ? Math.max(0, Math.ceil((Date.parse(status.deadline) - now) / 1000)) : 0
  const checked = status?.checks && Object.values(status.checks).every(Boolean)
  const labels: Record<string, string> = { disabled: 'Выключен', prepared: 'План подготовлен', armed: 'Watchdog вооружён', tracking: 'Проверка доступности', sealing: 'Закрепление сетевых изменений', sealed: 'Сетевые изменения закреплены', pending: 'Ожидает подтверждения', active: 'Включён', rolled_back: 'Исходная сеть восстановлена', rollback_failed: 'Откат не завершён', failed: 'Ошибка' }
  return <>
    <div className="page-heading"><div><div className="eyebrow">МАРШРУТИЗАЦИЯ СЕРВЕРА</div><h1>Full Tunnel</h1><p>TCP и UDP самого Ubuntu через VLESS с независимым автоматическим откатом.</p></div></div>
    <section className="form-card">
      <h2>{status ? labels[status.state] ?? status.state : 'Загрузка состояния…'}</h2>
      <p role="status">{status?.message}</p>
      {active && <p>Выходной IPv4: <b>{status?.exit_ip ?? '—'}</b>. Watchdog постоянно контролирует backend и Xray.</p>}
      <p className="helper-text">Локальная сеть и подтверждённые SSH/Web-сеансы сохраняют прямой доступ. DNS перенаправляется через VPN; внешний IPv6 блокируется. Docker, AWG и kill switch в этот режим не входят. При сбое сеть возвращается к обычному доступу.</p>
      {!active && !inProgress && <button className="primary-button" disabled={busy || recoveryRequired || !status?.available || connection?.state !== 'connected'} onClick={() => void action('prepare')}>{busy ? 'Подготавливаем…' : 'Подготовить безопасное включение'}</button>}
      {connection?.state !== 'connected' && !active && <p className="helper-text">Сначала подключите рабочий VLESS-узел в разделе «Подключения».</p>}
      {(active || inProgress || status?.state === 'prepared' || recoveryRequired) && <button className="outline-button" disabled={busy} onClick={() => void action('disable')}>Отключить и восстановить сеть</button>}
      {error && <p className="form-error" role="alert">{error}</p>}
    </section>
    {plan && <section className="form-card tunnel-plan"><h2>План сетевых изменений</h2><dl>
      <dt>VPN-сервер</dt><dd>{plan.endpoint_ip}:{plan.endpoint_port}</dd>
      <dt>Управляющие клиенты</dt><dd>{plan.management_peers.join(', ')}</dd>
      <dt>Прямой доступ к LAN</dt><dd>{[...(plan.lan4 ?? []), ...(plan.lan6 ?? [])].join(', ') || 'Нет подключённых сетей'}</dd>
      {plan.management_flow_policy && <><dt>Сохранение соединений</dt><dd>{plan.management_flow_policy}</dd></>}
      <dt>Порты управления</dt><dd>SSH {plan.ssh_port}, HTTPS {plan.ui_port}</dd>
      <dt>DNS через VPN</dt><dd>{plan.dns}, TCP/UDP 53</dd>
      <dt>IPv6</dt><dd>{plan.ipv6}</dd>
      <dt>Срок подтверждения</dt><dd>{plan.deadline_seconds} секунд после вооружения watchdog</dd>
    </dl>
      {prepared && status?.state === 'prepared' && <><p>Проверьте исключения. После включения подтвердите доступ к панели и проверьте новый SSH-сеанс. Без подтверждения сеть автоматически восстановится.</p><button className="primary-button" disabled={busy} onClick={() => void action('apply')}>{busy ? 'Применяем и проверяем…' : 'Включить по этому плану'}</button></>}
      {inProgress && status && <><p className="form-success">До автоматического отката: <b>{remaining} сек.</b></p>{awaitingConfirmation ? <><p>HTTPS: {status.checks.tcp ? 'прошёл' : 'ожидание'} · DNS UDP: {status.checks.dns_udp ? 'прошёл' : 'ожидание'} · DNS TCP: {status.checks.dns_tcp ? 'прошёл' : 'ожидание'} · IPv6: {status.checks.ipv6_blocked ? 'блокировка подтверждена' : 'ожидание'}</p><button className="primary-button" disabled={busy || !prepared || !checked || remaining <= 0} onClick={() => void action('confirm')}>Доступ к панели и SSH сохранён — подтвердить</button>{!prepared && <p className="helper-text">После перезагрузки страницы токен подтверждения утрачен. Дождитесь отката либо отключите режим и подготовьте новый план.</p>}</> : <p>Идёт завершающая проверка доступности и закрепление сетевых изменений. Подтверждение станет доступно только после перехода в состояние ожидания подтверждения.</p>}</>}
    </section>}
  </>
}
