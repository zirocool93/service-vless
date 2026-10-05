import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App'

const node = { id: 'node-1', name: 'Тестовый узел', kind: 'vless', enabled: true, favorite: false }
function response(body: unknown, status = 200) { return new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }

function installApiMock() {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/api/v1/auth/session') return response({ authenticated: false }, 401)
    if (url === '/api/v1/auth/login') return response({ authenticated: true, user: { username: 'admin' }, csrfToken: 'csrf-test' })
    if (url === '/api/v1/auth/logout') return response({ ok: true })
    if (url === '/api/v1/connections/status') return response({ state: 'disconnected', message: 'Отключено' })
    if (url === '/api/v1/connections' && (!init?.method || init.method === 'GET')) return response([node])
    if (url === '/api/v1/subscriptions' && (!init?.method || init.method === 'GET')) return response([])
    if (url === '/api/v1/subscriptions' && init?.method === 'POST') return response({ id: 'sub-1', name: 'Main' }, 201)
    if (url.endsWith('/refresh')) return response({ count: 1 })
    if (url.endsWith('/test')) return response({ service: true, endpoint: true, internet: false })
    if (url.endsWith('/connect')) return response({ state: 'connected' })
    if (init?.method === 'PATCH') return response(node)
    return response({ message: `unknown route ${url}` }, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function signIn() {
  const user = userEvent.setup()
  render(<App />)
  await user.type(await screen.findByLabelText('Имя пользователя'), 'admin')
  await user.type(screen.getByLabelText('Пароль'), 'secret')
  await user.click(screen.getByRole('button', { name: 'Войти' }))
  await screen.findByRole('heading', { name: 'Обзор шлюза' })
  return user
}

function mutationHeaders(call: [RequestInfo | URL, RequestInit?] | undefined) {
  if (!call) throw new Error('Запрос не найден')
  return new Headers(call[1]?.headers)
}

describe('управление шлюзом', () => {
  beforeEach(() => installApiMock())
  afterEach(() => { cleanup(); vi.unstubAllGlobals() })

  it('входит, сохраняет CSRF-токен для выхода и не отправляет его при login', async () => {
    const fetchMock = installApiMock()
    const user = await signIn()
    const loginCall = fetchMock.mock.calls.find(call => call[0] === '/api/v1/auth/login')
    expect(JSON.parse(String(loginCall?.[1]?.body))).toEqual({ username: 'admin', password: 'secret' })
    expect(mutationHeaders(loginCall).has('X-CSRF-Token')).toBe(false)
    await user.click(screen.getAllByRole('button', { name: 'Выйти' })[0])
    await waitFor(() => expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/auth/logout')).toBe(true))
    const logoutCall = fetchMock.mock.calls.find(call => call[0] === '/api/v1/auth/logout')
    expect(mutationHeaders(logoutCall).get('X-CSRF-Token')).toBe('csrf-test')
  })

  it('импортирует VLESS URI через API с CSRF-токеном', async () => {
    const fetchMock = installApiMock()
    const user = await signIn()
    await user.click(screen.getAllByRole('button', { name: 'Подключения' })[0])
    await user.type(await screen.findByLabelText('VLESS URI'), 'vless://12345678-1234-1234-1234-123456789abc@example.com:443#demo')
    await user.click(screen.getByRole('button', { name: 'Импортировать' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/connections' && call[1]?.method === 'POST')).toBe(true))
    const call = fetchMock.mock.calls.find(c => c[0] === '/api/v1/connections' && c[1]?.method === 'POST')
    expect(JSON.parse(String(call?.[1]?.body)).uri).toContain('vless://')
    expect(mutationHeaders(call).get('X-CSRF-Token')).toBe('csrf-test')
  })

  it('передаёт CSRF при проверке узла и подключении', async () => {
    const fetchMock = installApiMock()
    const user = await signIn()
    await user.click(screen.getAllByRole('button', { name: 'Подключения' })[0])
    const row = await screen.findByText('Тестовый узел')
    const article = row.closest('article')
    if (!article) throw new Error('Карточка узла отсутствует')
    await user.click(within(article).getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(call => String(call[0]).endsWith('/test'))).toBe(true))
    const testCall = fetchMock.mock.calls.find(call => String(call[0]).endsWith('/test'))
    expect(mutationHeaders(testCall).get('X-CSRF-Token')).toBe('csrf-test')
    await user.click(within(article).getByRole('button', { name: 'Подключить' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(call => String(call[0]).endsWith('/connect'))).toBe(true))
    const connectCall = fetchMock.mock.calls.find(call => String(call[0]).endsWith('/connect'))
    expect(mutationHeaders(connectCall).get('X-CSRF-Token')).toBe('csrf-test')
  })

  it('создаёт подписку с интервалом и CSRF-токеном', async () => {
    const fetchMock = installApiMock()
    const user = await signIn()
    await user.click(screen.getAllByRole('button', { name: 'Подписки' })[0])
    await user.type(await screen.findByLabelText('Название'), 'Основная')
    await user.type(screen.getByLabelText('URL подписки'), 'https://example.org/list')
    fireEvent.change(screen.getByLabelText('Интервал'), { target: { value: '12' } })
    await user.click(screen.getByRole('button', { name: 'Добавить' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/subscriptions' && call[1]?.method === 'POST')).toBe(true))
    const call = fetchMock.mock.calls.find(c => c[0] === '/api/v1/subscriptions' && c[1]?.method === 'POST')
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({ name: 'Основная', url: 'https://example.org/list', update_interval: 12 })
    expect(mutationHeaders(call).get('X-CSRF-Token')).toBe('csrf-test')
  })
})
