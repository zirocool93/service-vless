import { useEffect, useState } from 'react'
import {
  Activity, ArrowRight, Check, CircleHelp, Cloud, Command, Compass,
  Gauge, Globe2, LayoutDashboard, LockKeyhole, Menu,
  Network, Radio, Route, ScrollText, Settings2, Shield, ShieldCheck,
  Sparkles, Wifi, X,
} from 'lucide-react'

type Page = 'Обзор' | 'Подключения' | 'Подписки' | 'AmneziaWG' | 'Маршрутизация' | 'Резервирование' | 'Диагностика' | 'Журнал' | 'Настройки'
type ApiStatus = { state: string; message?: string }
const knownStates = new Set(['disconnected', 'connecting', 'connected', 'disconnecting', 'testing', 'switching', 'failed', 'rolling_back'])

const nav: { label: Page; icon: typeof LayoutDashboard; group: 'main' | 'system' }[] = [
  { label: 'Обзор', icon: LayoutDashboard, group: 'main' },
  { label: 'Подключения', icon: Radio, group: 'main' },
  { label: 'Подписки', icon: Globe2, group: 'main' },
  { label: 'AmneziaWG', icon: Shield, group: 'main' },
  { label: 'Маршрутизация', icon: Route, group: 'main' },
  { label: 'Резервирование', icon: Cloud, group: 'system' },
  { label: 'Диагностика', icon: Activity, group: 'system' },
  { label: 'Журнал', icon: ScrollText, group: 'system' },
  { label: 'Настройки', icon: Settings2, group: 'system' },
]

function App() {
  const [page, setPage] = useState<Page>('Обзор')
  const [status, setStatus] = useState<ApiStatus | null>(null)
  const [apiError, setApiError] = useState(false)
  const [mobileMenu, setMobileMenu] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    fetch('/api/v1/status', { signal: controller.signal, headers: { Accept: 'application/json' } })
      .then((response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`)
        return response.json() as Promise<unknown>
      })
      .then((data) => {
        if (typeof data !== 'object' || data === null || !('state' in data) || typeof data.state !== 'string' || !knownStates.has(data.state)) throw new Error('Некорректный ответ API')
        setStatus({ state: data.state })
        setApiError(false)
      })
      .catch(() => { if (!controller.signal.aborted) setApiError(true) })
    return () => controller.abort()
  }, [])

  const isConnected = status?.state === 'connected'
  const apiState = apiError ? 'Нет ответа' : status ? 'Доступен' : 'Проверяется'
  const vpnState = apiError || !status ? 'Неизвестно' : status.state === 'disconnected' ? 'Отключён' : isConnected ? 'Подключён по API' : 'Проверяется'

  return (
    <div className="app-shell">
      <aside className={`sidebar ${mobileMenu ? 'sidebar-open' : ''}`}>
        <div className="brand"><div className="brand-mark"><ShieldCheck size={20} strokeWidth={2.2} /></div><div><strong>Ubuntu VPN Gateway</strong><span>ПАНЕЛЬ УПРАВЛЕНИЯ</span></div><button className="icon-button mobile-close" aria-label="Закрыть меню" onClick={() => setMobileMenu(false)}><X size={18} /></button></div>
        <div className="nav-scroll"><div className="nav-label">УПРАВЛЕНИЕ</div><nav>{nav.filter(n => n.group === 'main').map(item => <NavItem key={item.label} item={item} active={page === item.label} onClick={() => { setPage(item.label); setMobileMenu(false) }} />)}<div className="nav-label system-label">СИСТЕМА</div>{nav.filter(n => n.group === 'system').map(item => <NavItem key={item.label} item={item} active={page === item.label} onClick={() => { setPage(item.label); setMobileMenu(false) }} />)}</nav></div>
        <div className="sidebar-bottom"><div className="phase-mini"><span className="pulse-dot" /><span><b>Этап 0</b><small>Панель в разработке</small></span><span className="phase-mini-tag">ОБЗОР</span></div><button className="help-link" onClick={() => setPage('Диагностика')}><CircleHelp size={17} /> Диагностика <ArrowRight size={14} /></button><div className="profile"><div className="profile-avatar"><LockKeyhole size={17} /></div><span><b>Вход не настроен</b><small>Локальная разработка</small></span></div></div>
      </aside>
      {mobileMenu && <button className="backdrop" aria-label="Закрыть меню" onClick={() => setMobileMenu(false)} />}
      <main className="main-area">
        <header className="topbar"><div className="breadcrumbs"><button className="icon-button menu-button" aria-label="Открыть меню" onClick={() => setMobileMenu(true)}><Menu size={19} /></button><span>Шлюз Ubuntu</span><span className="crumb-slash">/</span><b>{page}</b></div><div className="top-actions"><div className="phase-chip"><span className="status-dot" /> Этап 0 · Обзор</div></div></header>
        <div className="content">
          {page === 'Обзор' ? <Dashboard isConnected={isConnected} apiError={apiError} statusLoaded={status !== null} apiState={apiState} vpnState={vpnState} onNavigate={setPage} /> : <Placeholder page={page} />}
          <footer className="footer"><span>Ubuntu VPN Gateway <i>·</i> Панель управления</span><span>Интерфейс этапа 0 <i>·</i> VPN не настроен</span></footer>
        </div>
      </main>
    </div>
  )
}

function NavItem({ item, active, onClick }: { item: typeof nav[number]; active: boolean; onClick: () => void }) {
  const Icon = item.icon
  return <button className={`nav-item ${active ? 'active' : ''}`} onClick={onClick}><Icon size={18} strokeWidth={active ? 2 : 1.8} /><span>{item.label}</span>{item.label !== 'Обзор' && <span className="nav-soon">—</span>}</button>
}

function Dashboard({ isConnected, apiError, statusLoaded, apiState, vpnState, onNavigate }: { isConnected: boolean; apiError: boolean; statusLoaded: boolean; apiState: string; vpnState: string; onNavigate: (p: Page) => void }) {
  return <>
    <div className="page-heading"><div><div className="eyebrow"><Sparkles size={14} /> UBUNTU VPN GATEWAY</div><h1>Обзор шлюза</h1><p>Состояние интерфейса и диагностического API.</p></div><button className="outline-button" onClick={() => onNavigate('Диагностика')}><CircleHelp size={16} /> Что дальше?</button></div>
    <section className="notice-banner"><div className="notice-icon"><Command size={18} /></div><div className="notice-copy"><b>Это предварительная версия панели</b><span>Сейчас доступен интерфейс и проверка API. VPN, авторизация и управляющие действия ещё не реализованы.</span></div><span className="notice-stage">ЭТАП 0</span></section>
    <section className="overview-grid">
      <article className="connection-card"><div className="card-top"><div className="card-title"><div className="small-icon violet"><Wifi size={17} /></div><span>VPN-подключение</span></div></div><div className="connection-state"><div className={`state-orb ${isConnected ? 'connected' : ''}`}><div className="orb-inner"><Wifi size={27} /></div><i /></div><div className="state-copy"><span className={`state-label ${isConnected ? 'online' : ''}`}><span className="status-dot" />{apiError ? 'Статус неизвестен' : !statusLoaded ? 'Проверяется' : isConnected ? 'Подключено' : 'Отключено'}</span><h2>{apiError ? 'Нет ответа от API' : !statusLoaded ? 'Получаем состояние' : isConnected ? 'API сообщает о подключении' : 'VPN не подключён'}</h2><p>{apiError ? 'Не удалось получить состояние. Повторите загрузку страницы.' : !statusLoaded ? 'Ожидаем ответ локального API.' : isConnected ? 'Статус получен из API; проверка туннеля пока не реализована.' : 'Подключение будет доступно после реализации VPN-управления.'}</p></div></div><div className="connection-meta"><div><span>Текущий протокол</span><b><Shield size={14} /> Не настроен</b></div><div><span>Время работы</span><b className="meta-dash">—</b></div><div><span>Внешний адрес</span><b className="meta-dash">—</b></div></div><div className="card-divider" /><button className="text-link" onClick={() => onNavigate('Подключения')}>Перейти к подключениям <ArrowRight size={15} /></button></article>
      <article className="status-card"><div className="card-top"><div className="card-title"><div className="small-icon mint"><Gauge size={17} /></div><span>Состояние системы</span></div><span className={`live-pill ${apiError ? 'offline' : 'live'}`}><i />{apiState}</span></div><div className="system-list"><StatusRow icon={apiError ? X : Check} label="Сервер API" value={apiState} tone={apiError ? 'gray' : 'blue'} /><StatusRow icon={X} label="VPN-туннель" value={vpnState} tone={isConnected ? 'green' : 'gray'} /><StatusRow icon={X} label="Аутентификация" value="Не реализована" tone="gray" /><StatusRow icon={Check} label="Веб-интерфейс" value="Доступен" tone="green" /></div><div className="system-foot"><span>Режим</span><b>Локальная разработка</b></div></article>
    </section>
    <section className="lower-grid"><article className="quick-card"><div className="section-heading"><div><h3>Быстрый доступ</h3><p>Разделы панели управления</p></div></div><div className="quick-links"><QuickLink icon={Network} color="blue" title="Подключения" desc="Серверы и туннели" onClick={() => onNavigate('Подключения')} /><QuickLink icon={Globe2} color="orange" title="Подписки" desc="Импорт конфигурации" onClick={() => onNavigate('Подписки')} /><QuickLink icon={Route} color="purple" title="Маршрутизация" desc="Правила трафика" onClick={() => onNavigate('Маршрутизация')} /><QuickLink icon={Activity} color="green" title="Диагностика" desc="Проверка доступности" onClick={() => onNavigate('Диагностика')} /></div></article><article className="activity-card"><div className="section-heading"><div><h3>Последние события</h3><p>Системный журнал</p></div><button className="text-link compact" onClick={() => onNavigate('Журнал')}>Весь журнал <ArrowRight size={14} /></button></div><div className="empty-activity"><div className="empty-icon"><ScrollText size={19} /></div><div><b>Событий пока нет</b><span>{apiError ? 'API не ответил. Журнал станет доступен после подключения backend.' : 'События появятся после реализации системного журнала.'}</span></div><span className="empty-line" /></div><div className="activity-foot"><span><span className="status-dot muted-dot" /> Журнал ещё не реализован</span><span>Этап 1</span></div></article></section>
    <section className="bottom-callout"><div className="callout-icon"><Compass size={19} /></div><div><b>Готовим ваш шлюз к работе</b><span>В следующих этапах появятся настройка туннелей, подписок и правил маршрутизации.</span></div><button className="text-link" onClick={() => onNavigate('Диагностика')}>План реализации <ArrowRight size={15} /></button></section>
  </>
}

function StatusRow({ icon: Icon, label, value, tone }: { icon: typeof Check; label: string; value: string; tone: string }) {
  return <div className="status-row"><div className={`row-icon ${tone}`}><Icon size={13} strokeWidth={2.4} /></div><span>{label}</span><b className={tone === 'gray' ? 'value-gray' : ''}>{value}</b></div>
}

function QuickLink({ icon: Icon, color, title, desc, onClick }: { icon: typeof Wifi; color: string; title: string; desc: string; onClick: () => void }) {
  return <button className="quick-link" onClick={onClick}><div className={`quick-icon ${color}`}><Icon size={17} /></div><span><b>{title}</b><small>{desc}</small></span><ArrowRight size={15} className="quick-arrow" /></button>
}

const pageInfo: Record<Exclude<Page, 'Обзор'>, { icon: typeof Wifi; eyebrow: string; title: string; description: string; state: string; details: string[] }> = {
  'Подключения': { icon: Radio, eyebrow: 'УПРАВЛЕНИЕ ТУННЕЛЯМИ', title: 'Подключения', description: 'Серверы и VPN-туннели шлюза.', state: 'Управление подключениями ещё не реализовано.', details: ['Нет настроенных VPN-серверов', 'Создание и переключение туннелей недоступно', 'API для подключения отсутствует'] },
  'Подписки': { icon: Globe2, eyebrow: 'ИСТОЧНИКИ КОНФИГУРАЦИЙ', title: 'Подписки', description: 'Импорт и обновление конфигураций серверов.', state: 'Подписки пока не поддерживаются.', details: ['Импорт ссылок не реализован', 'Автоматическое обновление отсутствует', 'Сохранённых подписок нет'] },
  'AmneziaWG': { icon: Shield, eyebrow: 'ПРОТОКОЛЬНЫЙ МОДУЛЬ', title: 'AmneziaWG', description: 'Настройка протокола AmneziaWG.', state: 'Модуль AmneziaWG ещё не подключён.', details: ['Пакет и конфигурация не установлены', 'Генерация параметров недоступна', 'VPN-управление отсутствует'] },
  'Маршрутизация': { icon: Route, eyebrow: 'СЕТЕВЫЕ ПРАВИЛА', title: 'Маршрутизация', description: 'Правила маршрутов и направления трафика.', state: 'Правила маршрутизации ещё не реализованы.', details: ['Активных правил нет', 'Редактор правил недоступен', 'Настройки системы не изменяются'] },
  'Резервирование': { icon: Cloud, eyebrow: 'ОТКАЗОУСТОЙЧИВОСТЬ', title: 'Резервирование', description: 'Резервные туннели и переключение каналов.', state: 'Автоматическое резервирование пока не настроено.', details: ['Резервные каналы не заданы', 'Автоматическое переключение отсутствует', 'Мониторинг каналов не выполняется'] },
  'Диагностика': { icon: Activity, eyebrow: 'ПРОВЕРКА СИСТЕМЫ', title: 'Диагностика', description: 'Доступность API и компоненты шлюза.', state: 'Доступна только базовая проверка статуса API.', details: ['VPN-туннель: не настроен', 'Авторизация: не реализована', 'Системная диагностика: недоступна'] },
  'Журнал': { icon: ScrollText, eyebrow: 'СОБЫТИЯ И АУДИТ', title: 'Журнал событий', description: 'Системные сообщения и история действий.', state: 'Журнал событий ещё не подключён.', details: ['Записи не собираются', 'Фильтрация и экспорт недоступны', 'История действий отсутствует'] },
  'Настройки': { icon: Settings2, eyebrow: 'КОНФИГУРАЦИЯ ШЛЮЗА', title: 'Настройки', description: 'Общие параметры и управление доступом.', state: 'Настройки пока недоступны.', details: ['Сохранение конфигурации не реализовано', 'Авторизация отсутствует', 'Этот экран не меняет состояние сервера'] },
}

function Placeholder({ page }: { page: Exclude<Page, 'Обзор'> }) {
  const info = pageInfo[page]
  const Icon = info.icon
  return <div className="placeholder-page"><div className="page-heading"><div><div className="eyebrow"><Icon size={14} /> {info.eyebrow}</div><h1>{info.title}</h1><p>{info.description}</p></div><span className="phase-chip"><span className="status-dot" /> Этап 0 · Обзор</span></div><section className="placeholder-card"><div className="placeholder-art"><div className="placeholder-ring"><Icon size={29} /></div><span className="art-spark spark-a">✳</span><span className="art-spark spark-b">·</span><span className="art-spark spark-c">✦</span></div><div className="placeholder-copy"><span className="implementation-tag"><span className="status-dot" /> В разработке</span><h2>{info.state}</h2><p>Экран показывает фактическое состояние реализации. Действия и данные появятся после подключения соответствующего API.</p></div><div className="implementation-list">{info.details.map(d => <div key={d}><span className="list-dash">—</span>{d}</div>)}</div><div className="placeholder-foot"><span><LockKeyhole size={14} /> Только информационный экран</span><span>Функции управления не активны</span></div></section><div className="phase-note"><div className="notice-icon"><Sparkles size={17} /></div><span><b>Разработка ведётся поэтапно</b><small>Сейчас реализованы каркас интерфейса и запрос статуса. Этот раздел станет доступен после появления backend API.</small></span></div></div>
}

export default App
