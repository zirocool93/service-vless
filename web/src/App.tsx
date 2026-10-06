import { useCallback, useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Activity, ArrowRight, Cloud, Globe2, LayoutDashboard, LockKeyhole, LogOut, Menu, Radio, RefreshCw, Route, ScrollText, Settings2, Shield, Star, Wifi, X } from 'lucide-react'
import { api, ApiError, setUnauthorizedHandler } from './api'
import type { Connection, ConnectionStatus, Session, Subscription, TunnelStatus } from './api'
import FullTunnel from './FullTunnel'

type Page = 'Обзор' | 'Подключения' | 'Подписки' | 'AmneziaWG' | 'Маршрутизация' | 'Резервирование' | 'Диагностика' | 'Журнал' | 'Настройки'
type EventItem = { id: string; type: string; message: string; time: string }
const pages: { name: Page; icon: typeof LayoutDashboard }[] = [
  { name: 'Обзор', icon: LayoutDashboard }, { name: 'Подключения', icon: Radio }, { name: 'Подписки', icon: Globe2 },
  { name: 'AmneziaWG', icon: Shield }, { name: 'Маршрутизация', icon: Route }, { name: 'Резервирование', icon: Cloud },
  { name: 'Диагностика', icon: Activity }, { name: 'Журнал', icon: ScrollText }, { name: 'Настройки', icon: Settings2 },
]

function stateLabel(state: string) { return ({ connected: 'Подключено', disconnected: 'Отключено', connecting: 'Подключение', disconnecting: 'Отключение', failed: 'Ошибка', degraded: 'Нестабильно', initializing: 'Инициализация', reconnecting: 'Переподключение' } as Record<string, string>)[state] ?? 'Неизвестно' }

function errorMessage(error: unknown) { return error instanceof Error ? error.message : 'Не удалось выполнить запрос' }

export default function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [sessionLoading, setSessionLoading] = useState(true)
  const [sessionError, setSessionError] = useState('')
  const [page, setPage] = useState<Page>('Обзор')
  const [mobileMenu, setMobileMenu] = useState(false)
  const [events, setEvents] = useState<EventItem[]>([])
  const [eventRevision, setEventRevision] = useState(0)
  const [eventError, setEventError] = useState('')

  useEffect(() => {
    setUnauthorizedHandler(() => setSession({ authenticated: false }))
    return () => setUnauthorizedHandler(undefined)
  }, [])

  const loadSession = useCallback(async () => {
    setSessionLoading(true); setSessionError('')
    try { setSession(await api<Session>('/api/v1/auth/session')) }
    catch (error) {
      if (error instanceof ApiError && error.status === 401) setSession({ authenticated: false })
      else setSessionError(errorMessage(error))
    } finally { setSessionLoading(false) }
  }, [])
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- Начальная сессия загружается после монтирования.
    void loadSession()
  }, [loadSession])

  useEffect(() => {
    if (!session?.authenticated) return
    const stream = new EventSource('/api/v1/events', { withCredentials: true })
    stream.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as Omit<EventItem, 'id'>
        setEvents(current => [{ ...data, id: `${data.time}-${crypto.randomUUID()}` }, ...current].slice(0, 100))
        setEventRevision(revision => revision + 1)
      } catch { /* Некорректные кадры потока игнорируются. */ }
    }
    stream.onerror = () => {
      stream.close()
      setEventError('Поток событий недоступен')
      void api<Session>('/api/v1/auth/session').then(current => {
        if (!current.authenticated) setSession({ authenticated: false })
      }).catch(() => undefined)
    }
    return () => stream.close()
  }, [session?.authenticated])

  const logout = async () => {
    try { await api('/api/v1/auth/logout', { method: 'POST' }, session?.csrfToken) }
    finally { setSession({ authenticated: false }); setPage('Обзор') }
  }

  if (sessionLoading) return <div className="auth-screen"><p>Проверяем сессию…</p></div>
  if (sessionError) return <div className="auth-screen"><section className="auth-card"><Brand /><h1>Сервис недоступен</h1><p>{sessionError}</p><button className="primary-button" onClick={() => void loadSession()}>Повторить</button></section></div>
  if (!session?.authenticated) return <Login onLogin={setSession} />

  return <div className="app-shell">
    <aside className={`sidebar ${mobileMenu ? 'sidebar-open' : ''}`}>
      <div className="brand"><Brand /><button className="icon-button mobile-close" aria-label="Закрыть меню" onClick={() => setMobileMenu(false)}><X size={18} /></button></div>
      <div className="nav-scroll"><div className="nav-label">ШЛЮЗ</div><nav>{pages.map(({ name, icon: Icon }) => <button key={name} className={`nav-item ${page === name ? 'active' : ''}`} onClick={() => { setPage(name); setMobileMenu(false) }}><Icon size={17} /><span>{name}</span></button>)}</nav></div>
      <div className="sidebar-bottom"><div className="phase-mini"><span className="pulse-dot" /><span><b>Ubuntu VPN Gateway</b><small>Локальное управление</small></span></div><div className="profile"><div className="profile-avatar"><LockKeyhole size={16} /></div><span><b>{session.user?.username ?? 'Пользователь'}</b><small>{session.user?.role ?? 'Сессия активна'}</small></span><button className="icon-button" aria-label="Выйти" title="Выйти" onClick={() => void logout()}><LogOut size={17} /></button></div></div>
    </aside>
    {mobileMenu && <button className="backdrop" aria-label="Закрыть меню" onClick={() => setMobileMenu(false)} />}
    <main className="main-area"><header className="topbar"><div className="breadcrumbs"><button className="icon-button menu-button" aria-label="Открыть меню" onClick={() => setMobileMenu(true)}><Menu size={19} /></button><span>Ubuntu VPN Gateway</span><span className="crumb-slash">/</span><b>{page}</b></div><div className="top-actions"><span className="phase-chip"><span className="status-dot" /> VLESS</span><span className="top-user">{session.user?.username}</span><button className="icon-button" aria-label="Выйти" onClick={() => void logout()}><LogOut size={17} /></button></div></header>
      <div className="content">{eventError && <div className="stream-error" role="status">{eventError}</div>}{page === 'Обзор' ? <Dashboard csrf={session.csrfToken ?? ''} onNavigate={setPage} events={events} eventRevision={eventRevision} /> : page === 'Подключения' ? <Connections csrf={session.csrfToken ?? ''} /> : page === 'Подписки' ? <Subscriptions csrf={session.csrfToken ?? ''} /> : page === 'AmneziaWG' ? <AWG csrf={session.csrfToken ?? ''} /> : page === 'Маршрутизация' ? <FullTunnel csrf={session.csrfToken ?? ''} /> : page === 'Журнал' ? <EventLog events={events} /> : <Unimplemented page={page} />}</div>
    </main>
  </div>
}

function Brand() { return <><div className="brand-mark"><Shield size={19} /></div><div><strong>Ubuntu VPN Gateway</strong><span>ПАНЕЛЬ УПРАВЛЕНИЯ</span></div></> }

function Login({ onLogin }: { onLogin: (session: Session) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('')
    try { onLogin(await api<Session>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })) }
    catch (err) { setError(errorMessage(err)) }
    finally { setBusy(false) }
  }
  return <div className="auth-screen"><form className="auth-card" onSubmit={submit}><div className="auth-brand"><Brand /></div><div className="eyebrow">ЗАЩИЩЁННЫЙ ДОСТУП</div><h1>Вход в панель</h1><p>Введите имя пользователя и пароль.</p><label>Имя пользователя<input autoComplete="username" required value={username} onChange={e => setUsername(e.target.value)} /></label><label>Пароль<input type="password" autoComplete="current-password" required value={password} onChange={e => setPassword(e.target.value)} /></label>{error && <div className="form-error" role="alert">{error}</div>}<button className="primary-button" disabled={busy}>{busy ? 'Входим…' : 'Войти'}</button></form></div>
}

function useResource<T>(path: string, csrf: string, initial: T) {
  const [data, setData] = useState<T>(initial)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const reload = useCallback(async () => {
    setLoading(true); setError('')
    try { setData(await api<T>(path, {}, csrf)) } catch (err) { setError(errorMessage(err)) } finally { setLoading(false) }
  }, [path, csrf])
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- Начальные данные страницы загружаются после монтирования.
    void reload()
  }, [reload])
  return { data, setData, loading, error, reload }
}

function PageHeading({ eyebrow, title, description }: { eyebrow: string; title: string; description: string }) {
  return <div className="page-heading"><div><div className="eyebrow">{eyebrow}</div><h1>{title}</h1><p>{description}</p></div></div>
}

function Dashboard({ csrf, onNavigate, events, eventRevision }: { csrf: string; onNavigate: (p: Page) => void; events: EventItem[]; eventRevision: number }) {
  const status = useResource<ConnectionStatus>('/api/v1/connections/status', csrf, { state: 'unknown' })
  const tunnel = useResource<TunnelStatus>('/api/v1/tunnel/status', csrf, { available: false, state: 'unknown', message: '', checks: { tcp: false, dns_udp: false, dns_tcp: false, ipv6_blocked: false } })
  const connections = useResource<Connection[]>('/api/v1/connections', csrf, [])
  const reloadStatus = status.reload
  const reloadTunnel = tunnel.reload
  const reloadConnections = connections.reload
  useEffect(() => { if (eventRevision > 0) { void reloadStatus(); void reloadTunnel(); void reloadConnections() } }, [eventRevision, reloadStatus, reloadTunnel, reloadConnections])
  const s = status.data
  const known = !status.loading && !status.error && s.state !== 'unknown'
  const active = known && s.state.toLowerCase() === 'connected'
  const tunnelLabel: Record<string, string> = { disabled: 'Выключен', prepared: 'План подготовлен', armed: 'Ожидает применения', pending: 'Ожидает подтверждения', active: 'Включён', rolled_back: 'Восстановлена исходная сеть', failed: 'Ошибка' }
  const tunnelKnown = !tunnel.loading && !tunnel.error && tunnel.data.state !== 'unknown'
  const fullTunnel = tunnelKnown && tunnel.data.state === 'active'
  const tunnelPending = tunnelKnown && ['prepared', 'armed', 'pending'].includes(tunnel.data.state)
  const modeTitle = tunnel.loading ? 'Загружаем режим сети' : tunnel.error ? 'Режим сети недоступен' : fullTunnel ? 'Режим Full Tunnel' : tunnelPending ? 'Full Tunnel: требуется действие' : tunnelKnown && tunnel.data.state !== 'disabled' ? `Full Tunnel: ${tunnelLabel[tunnel.data.state] ?? tunnel.data.state}` : 'Режим локального прокси'
  const modeDescription = fullTunnel
    ? `Весь трафик Ubuntu направляется через VLESS. Внешний IPv4: ${tunnel.data.exit_ip ?? 'проверяется'}.`
    : tunnelPending
      ? `${tunnel.data.message || tunnelLabel[tunnel.data.state]}. Проверьте состояние и продолжите в разделе маршрутизации.`
      : tunnel.error ? tunnel.error : tunnelKnown && tunnel.data.state !== 'disabled'
        ? tunnel.data.message || 'Состояние Full Tunnel требует внимания.'
        : `${tunnel.data.available ? 'Full Tunnel доступен для включения. ' : 'Full Tunnel недоступен. '}${tunnel.data.message || ''} Через VPN работают приложения, настроенные на SOCKS5 127.0.0.1:1080 или HTTP 127.0.0.1:8080.`
  return <><PageHeading eyebrow="ОБЗОР СИСТЕМЫ" title="Обзор шлюза" description="Текущее состояние VPN-клиента и доступных узлов." />
    <section className="notice-banner"><div className="notice-icon"><Activity size={18} /></div><div className="notice-copy"><b>{modeTitle}</b><span>{modeDescription}</span></div><button className="text-link" onClick={() => onNavigate('Маршрутизация')}>Маршрутизация <ArrowRight size={15} /></button><span className="notice-stage">{fullTunnel ? 'FULL TUNNEL' : 'VLESS'}</span></section>
    <section className="overview-grid"><article className="connection-card"><div className="card-top"><div className="card-title"><div className="small-icon violet"><Wifi size={17} /></div><span>VPN-соединение</span></div></div><div className="connection-state"><div className={`state-orb ${active ? 'connected' : ''}`}><div className="orb-inner"><Wifi size={27} /></div></div><div className="state-copy"><span className={`state-label ${active ? 'online' : ''}`}><span className="status-dot" />{status.loading ? 'Загрузка' : status.error ? 'Недоступно' : stateLabel(s.state)}</span><h2>{status.loading ? 'Получаем статус' : status.error ? 'Статус неизвестен' : active ? 'Прокси подключён' : s.state === 'connecting' ? 'Проверяем новый узел' : s.state === 'failed' ? 'Ошибка подключения' : 'Прокси отключён'}</h2><p>{status.error || s.message || 'Состояние подключения получено.'}</p></div></div><div className="connection-meta"><div><span>Активный узел</span><b>{known ? connections.data.find(c => c.id === s.active_node_id)?.name ?? '—' : '—'}</b></div><div><span>Внешний IP</span><b>{s.exit_ip || '—'}</b></div><div><span>Задержка</span><b>—</b></div></div><div className="card-divider" /><button className="text-link" onClick={() => onNavigate('Подключения')}>Открыть подключения <ArrowRight size={15} /></button></article><article className="status-card"><div className="card-top"><div className="card-title"><div className="small-icon mint"><Activity size={17} /></div><span>Показатели</span></div></div><div className="system-list"><Metric label="Состояние прокси" value={status.loading ? 'Загрузка…' : status.error ? '—' : stateLabel(s.state)} /><Metric label="Full Tunnel" value={tunnel.loading ? 'Загрузка…' : tunnel.error ? '—' : tunnelLabel[tunnel.data.state] ?? tunnel.data.state} /><Metric label="Доступные узлы" value={connections.loading || connections.error ? '—' : String(connections.data.length)} /><Metric label="Входящий трафик" value="—" /><Metric label="Исходящий трафик" value="—" /></div><div className="system-foot"><span>Источник</span><b>API шлюза</b></div></article></section>
    <section className="lower-grid"><article className="quick-card"><div className="section-heading"><div><h3>Подключения</h3><p>{connections.error || (connections.loading ? 'Загрузка…' : `${connections.data.length} узлов`)}</p></div><button className="text-link compact" onClick={() => onNavigate('Подключения')}>Открыть <ArrowRight size={14} /></button></div><div className="connection-list compact-list">{connections.data.slice(0, 5).map(c => <ConnectionRow key={c.id} item={c} csrf={csrf} onChange={connections.reload} />)}{!connections.loading && !connections.error && connections.data.length === 0 && <Empty text="Подключения не добавлены." />}{connections.loading && <Empty text="Загружаем подключения…" />}{connections.error && <Empty text={connections.error} />}</div></article><article className="activity-card"><div className="section-heading"><div><h3>Последние события</h3><p>Поток событий сервера</p></div><button className="text-link compact" onClick={() => onNavigate('Журнал')}>Журнал <ArrowRight size={14} /></button></div><div className="event-list">{events.slice(0, 4).map(e => <div className="event-row" key={e.id}><span className="event-dot" /><div><b>{e.type}</b><span>{e.message}</span></div><time>{formatTime(e.time)}</time></div>)}{events.length === 0 && <Empty text="События появятся при получении данных из SSE-потока." />}</div></article></section>
  </>
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="status-row"><span>{label}</span><b>{value}</b></div> }
function Empty({ text }: { text: string }) { return <p className="empty-message">{text}</p> }
function formatTime(value: string) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? value : date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' }) }

function Connections({ csrf }: { csrf: string }) {
  const resource = useResource<Connection[]>('/api/v1/connections', csrf, [])
  const [uri, setUri] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const status = useResource<ConnectionStatus>('/api/v1/connections/status', csrf, { state: 'unknown' })
  async function add(event: FormEvent) {
    event.preventDefault(); setBusy(true); setNotice(''); setError('')
    try { await api<Connection>('/api/v1/connections', { method: 'POST', body: JSON.stringify({ uri }) }, csrf); setUri(''); setNotice('Узел добавлен.'); await resource.reload() }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  async function disconnect() {
    setBusy(true); setNotice(''); setError('')
    try { await api('/api/v1/connections/disconnect', { method: 'POST', body: '{}' }, csrf); setNotice('Запрос на отключение выполнен.'); await status.reload() }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  return <><PageHeading eyebrow="VLESS И УЗЛЫ" title="Подключения" description="Импортируйте VLESS URI, проверяйте узлы и управляйте подключением." /><section className="form-card"><h2>Добавить VLESS-узел</h2><form className="inline-form" onSubmit={add}><label className="grow">VLESS URI<input required value={uri} onChange={e => setUri(e.target.value)} placeholder="vless://…" /></label><button className="primary-button" disabled={busy || !uri.startsWith('vless://')}>{busy ? 'Добавление…' : 'Импортировать'}</button></form><p className="helper-text">Параметры подключения хранятся в зашифрованном виде.</p>{notice && <p className="form-success" role="status">{notice}</p>}{error && <p className="form-error" role="alert">{error}</p>}</section><section className="list-card"><div className="section-heading"><div><h2>Узлы</h2><p>{resource.error || (resource.loading ? 'Загрузка…' : `${resource.data.length} записей`)}</p></div><button className="outline-button" disabled={busy || status.loading || Boolean(status.error) || status.data.state.toLowerCase() !== 'connected'} onClick={() => void disconnect()}>Отключить</button></div>{resource.error ? <Empty text={resource.error} /> : resource.loading ? <Empty text="Загружаем узлы…" /> : resource.data.length === 0 ? <Empty text="Узлов пока нет. Добавьте VLESS URI выше." /> : <div className="connection-list">{resource.data.map(item => <ConnectionRow key={item.id} item={item} csrf={csrf} onChange={resource.reload} activeNodeId={status.data.active_node_id} />)}</div>}</section></>
}

function ConnectionRow({ item, csrf, onChange, activeNodeId }: { item: Connection; csrf: string; onChange: () => void; activeNodeId?: string }) {
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  async function mutate(path: string, method: string, body?: unknown) {
    setBusy(true); setError(''); setMessage('')
    try { const result = await api<unknown>(path, { method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) }, csrf); if (path.endsWith('/test')) setMessage(formatTestResult(result)); onChange() }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  const isAWG = item.kind.toLowerCase().includes('awg')
  const isActive = activeNodeId === item.id
  return <article className="connection-row"><div className="node-icon"><Wifi size={17} /></div><div className="node-main"><div><b>{item.name}</b><span className="kind-tag">{item.kind}</span>{item.stale && <span className="kind-tag">Устарел</span>}</div><small>{item.last_error || (item.latency_ms != null ? `Задержка ${item.latency_ms} мс` : 'Задержка неизвестна')}</small>{message && <small className="form-success">Результат проверки: {message}</small>}{error && <small className="form-error">{error}</small>}</div><button className={`icon-button star-button ${item.favorite ? 'is-favorite' : ''}`} aria-label={item.favorite ? 'Убрать из избранного' : 'Добавить в избранное'} disabled={busy} onClick={() => void mutate(`/api/v1/connections/${encodeURIComponent(item.id)}`, 'PATCH', { favorite: !item.favorite })}><Star size={17} fill={item.favorite ? 'currentColor' : 'none'} /></button><div className="node-actions"><button className="outline-button" disabled={busy} onClick={() => void mutate(`/api/v1/connections/${encodeURIComponent(item.id)}/test`, 'POST', {})}>Проверить</button><button className="primary-button" disabled={busy || isAWG || isActive} title={isAWG ? 'Подключение AmneziaWG отключено: безопасное сетевое применение не реализовано.' : undefined} onClick={() => void mutate(`/api/v1/connections/${encodeURIComponent(item.id)}/connect`, 'POST', {})}>{isAWG ? 'Недоступно' : isActive ? 'Подключён' : 'Подключить'}</button></div></article>
}

function formatTestResult(value: unknown) {
  if (!value || typeof value !== 'object') return 'Ответ получен'
  const result = value as Record<string, unknown>
  const level = (name: string, label: string) => typeof result[name] === 'boolean' ? `${label}: ${result[name] ? 'да' : 'нет'}` : ''
  return [level('service', 'Сервис'), level('endpoint', 'Узел'), level('internet', 'Интернет'), typeof result.exit_ip === 'string' ? `Внешний IP: ${result.exit_ip}` : ''].filter(Boolean).join(' · ') || 'Ответ получен'
}

function Subscriptions({ csrf }: { csrf: string }) {
  const resource = useResource<Subscription[]>('/api/v1/subscriptions', csrf, [])
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [interval, setInterval] = useState(24)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function add(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('')
    try { await api<Subscription>('/api/v1/subscriptions', { method: 'POST', body: JSON.stringify({ name, url, enabled: true, update_interval: interval }) }, csrf); setName(''); setUrl(''); await resource.reload() }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  async function refresh(id: string) {
    setBusy(true); setError('')
    try { await api(`/api/v1/subscriptions/${encodeURIComponent(id)}/refresh`, { method: 'POST', body: '{}' }, csrf); await resource.reload() }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  return <><PageHeading eyebrow="ИСТОЧНИКИ VLESS" title="Подписки" description="Добавляйте URL и обновляйте список узлов из источников." /><section className="form-card"><h2>Добавить подписку</h2><form className="subscription-form" onSubmit={add}><label>Название<input required value={name} onChange={e => setName(e.target.value)} placeholder="Основная" /></label><label className="grow">URL подписки<input required type="url" value={url} onChange={e => setUrl(e.target.value)} placeholder="https://…" /></label><label>Интервал<select value={interval} onChange={e => setInterval(Number(e.target.value))}><option value={0}>Отключён</option><option value={6}>6 часов</option><option value={12}>12 часов</option><option value={24}>24 часа</option></select></label><button className="primary-button" disabled={busy}>{busy ? 'Сохранение…' : 'Добавить'}</button></form>{error && <p className="form-error" role="alert">{error}</p>}</section><section className="list-card"><div className="section-heading"><div><h2>Сохранённые подписки</h2><p>{resource.error || (resource.loading ? 'Загрузка…' : `${resource.data.length} записей`)}</p></div></div>{resource.error ? <Empty text={resource.error} /> : resource.loading ? <Empty text="Загружаем подписки…" /> : resource.data.length === 0 ? <Empty text="Подписок пока нет." /> : resource.data.map(s => <article className="subscription-row" key={s.id}><div className="node-icon"><Globe2 size={17} /></div><div className="node-main"><b>{s.name}</b><small>{subscriptionOrigin(s.url)}</small><small>Автообновление: {s.update_interval ? `каждые ${s.update_interval} ч` : 'выключено'}</small></div><button className="outline-button" disabled={busy} onClick={() => void refresh(s.id)}><RefreshCw size={14} /> Обновить</button></article>)}</section></>
}

function subscriptionOrigin(value: string) { try { return new URL(value).host || 'URL сохранён' } catch { return 'URL сохранён' } }

function AWG({ csrf }: { csrf: string }) {
  const [config, setConfig] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  async function importConfig(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(''); setMessage('')
    try { await api<Connection>('/api/v1/connections', { method: 'POST', body: JSON.stringify({ awg_config: config }) }, csrf); setConfig(''); setMessage('Конфигурация импортирована. Подключение пока отключено.') }
    catch (err) { setError(errorMessage(err)) } finally { setBusy(false) }
  }
  return <><PageHeading eyebrow="КОНФИГУРАЦИЯ КЛИЕНТА" title="AmneziaWG" description="Импортируйте клиентскую конфигурацию AmneziaWG." /><section className="form-card"><div className="awg-notice"><Shield size={18} /><p>Импорт конфигурации доступен через API. Подключение отключено: безопасное применение сетевой конфигурации ещё не реализовано.</p></div><form onSubmit={importConfig}><label>Конфигурация .conf<textarea required rows={12} spellCheck={false} value={config} onChange={e => setConfig(e.target.value)} placeholder="[Interface]&#10;…&#10;&#10;[Peer]&#10;…" /></label><button className="primary-button" disabled={busy || !config.trim()}>{busy ? 'Импорт…' : 'Импортировать конфигурацию'}</button></form>{message && <p className="form-success" role="status">{message}</p>}{error && <p className="form-error" role="alert">{error}</p>}</section></>
}

function EventLog({ events }: { events: EventItem[] }) {
  return <><PageHeading eyebrow="СЕРВЕРНЫЕ СОБЫТИЯ · SSE" title="Журнал" description="События поступают из SSE-потока без периодического опроса." /><section className="list-card"><div className="event-list full-events">{events.map(e => <div className="event-row" key={e.id}><span className="event-dot" /><div><b>{e.type}</b><span>{e.message}</span></div><time>{formatTime(e.time)}</time></div>)}{events.length === 0 && <Empty text="Поток подключён; событий пока не получено." />}</div></section></>
}

function Unimplemented({ page }: { page: Page }) {
  return <><PageHeading eyebrow="СОСТОЯНИЕ РЕАЛИЗАЦИИ" title={page} description="Этот раздел не выполняет управляющих действий." /><section className="placeholder-card"><div className="placeholder-copy"><span className="implementation-tag">Не реализовано</span><h2>API для этого раздела пока отсутствует</h2><p>Интерфейс не показывает вымышленные данные и не меняет конфигурацию системы.</p></div><div className="implementation-list"><div>— Правила маршрутизации недоступны</div><div>— Управление резервированием недоступно</div><div>— Системные настройки не изменяются</div></div></section></>
}
