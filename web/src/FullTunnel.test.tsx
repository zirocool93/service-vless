import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import FullTunnel from './FullTunnel'

const plan = {
  transaction_id: 'tx-1', hash: 'sha256:abc', node_id: 'node-1', endpoint_ip: '198.51.100.8', endpoint_port: 443,
  management_peers: ['203.0.113.9'], lan4: ['192.168.1.0/24'], lan6: null,
  management_flow_policy: 'При Apply сохраняются полные tuple текущих SSH/UI-соединений только для management peers',
  dns: '10.0.0.1', ipv6: 'Блокировать внешний IPv6',
  deadline_seconds: 120, ssh_port: 22, ui_port: 443,
}
const allChecks = { tcp: true, dns_udp: true, dns_tcp: true, ipv6_blocked: true }
function response(body: unknown, status = 200) { return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }
function tunnelState(state: string, overrides: Record<string, unknown> = {}) {
  return { available: true, state, message: `Состояние: ${state}`, checks: allChecks, plan, deadline: new Date(Date.now() + 120_000).toISOString(), ...overrides }
}
function installMock(initial: ReturnType<typeof tunnelState>, applyChecks = allChecks) {
  let status = initial
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    void init
    const url = String(input)
    if (url === '/api/v1/tunnel/status') return response(status)
    if (url === '/api/v1/connections/status') return response({ state: 'connected', active_node_id: 'node-1' })
    if (url === '/api/v1/tunnel/prepare') {
      status = tunnelState('prepared', { checks: { tcp: false, dns_udp: false, dns_tcp: false, ipv6_blocked: false } })
      return response({ plan, token: 'one-time-token' })
    }
    if (url === '/api/v1/tunnel/apply') { status = tunnelState('pending', { checks: applyChecks }); return response(status) }
    if (url === '/api/v1/tunnel/confirm') { status = tunnelState('active'); return response(status) }
    if (url === '/api/v1/tunnel/disable') { status = tunnelState('disabled'); return response(status) }
    return response({ message: `unknown route ${url}` }, 404)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}
function headers(call: [RequestInfo | URL, RequestInit?] | undefined) { return new Headers(call?.[1]?.headers) }

describe('безопасное управление Full Tunnel', () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals() })

  it.each([
    ['armed', 'Watchdog вооружён'],
    ['tracking', 'Проверка доступности'],
    ['sealing', 'Закрепление сетевых изменений'],
    ['sealed', 'Сетевые изменения закреплены'],
  ])('для промежуточного состояния %s блокирует подготовку и подтверждение, но разрешает отключение', async (state, label) => {
    const fetchMock = installMock(tunnelState(state))
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)

    expect(await screen.findByRole('heading', { name: label })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Подготовить безопасное включение' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Доступ к панели и SSH сохранён — подтвердить' })).not.toBeInTheDocument()
    const disable = screen.getByRole('button', { name: 'Отключить и восстановить сеть' })
    expect(disable).toBeEnabled()
    await user.click(disable)
    await waitFor(() => expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/tunnel/disable')).toBe(true))
  })

  it('сначала показывает план, применяет только после явного нажатия и блокирует подтверждение без проверок', async () => {
    const fetchMock = installMock(tunnelState('disabled'), { tcp: false, dns_udp: false, dns_tcp: false, ipv6_blocked: false })
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)

    await user.click(await screen.findByRole('button', { name: 'Подготовить безопасное включение' }))
    expect(await screen.findByText('198.51.100.8:443')).toBeInTheDocument()
    expect(screen.getByText('При Apply сохраняются полные tuple текущих SSH/UI-соединений только для management peers')).toBeInTheDocument()
    expect(screen.getByText('192.168.1.0/24')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/tunnel/apply')).toBe(false)

    await user.click(screen.getByRole('button', { name: 'Включить по этому плану' }))
    const applyCall = await waitFor(() => {
      const call = fetchMock.mock.calls.find(item => item[0] === '/api/v1/tunnel/apply')
      expect(call).toBeDefined()
      return call
    })
    expect(headers(applyCall).get('X-CSRF-Token')).toBe('csrf-test')
    const confirm = await screen.findByRole('button', { name: 'Доступ к панели и SSH сохранён — подтвердить' })
    expect(confirm).toBeDisabled()
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/tunnel/confirm')).toBe(false)
  })

  it('подтверждает только с токеном и пройденными проверками, отправляя CSRF', async () => {
    const fetchMock = installMock(tunnelState('disabled'))
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)
    await user.click(await screen.findByRole('button', { name: 'Подготовить безопасное включение' }))
    await user.click(await screen.findByRole('button', { name: 'Включить по этому плану' }))

    const confirm = await screen.findByRole('button', { name: 'Доступ к панели и SSH сохранён — подтвердить' })
    expect(confirm).toBeEnabled()
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/tunnel/confirm')).toBe(false)
    await user.click(confirm)
    const confirmCall = await waitFor(() => {
      const call = fetchMock.mock.calls.find(item => item[0] === '/api/v1/tunnel/confirm')
      expect(call).toBeDefined()
      return call
    })
    expect(headers(confirmCall).get('X-CSRF-Token')).toBe('csrf-test')
    expect(JSON.parse(String(confirmCall?.[1]?.body))).toEqual({ transaction_id: 'tx-1', hash: 'sha256:abc', token: 'one-time-token' })
  })

  it('после перезагрузки не подтверждает pending без сохранённого одноразового токена', async () => {
    const fetchMock = installMock(tunnelState('pending'))
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)
    expect(await screen.findByText(/токен подтверждения утрачен/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Доступ к панели и SSH сохранён — подтвердить' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Отключить и восстановить сеть' }))
    const disableCall = await waitFor(() => {
      const call = fetchMock.mock.calls.find(item => item[0] === '/api/v1/tunnel/disable')
      expect(call).toBeDefined()
      return call
    })
    expect(headers(disableCall).get('X-CSRF-Token')).toBe('csrf-test')
    expect(JSON.parse(String(disableCall?.[1]?.body))).toEqual({})
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/v1/tunnel/confirm')).toBe(false)
  })

  it('при незавершённом откате блокирует подготовку и позволяет повторить очистку с CSRF', async () => {
    const fetchMock = installMock(tunnelState('rollback_failed', { message: 'Нужно повторить восстановление маршрутов.' }))
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)

    expect(await screen.findByRole('heading', { name: 'Откат не завершён' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Подготовить безопасное включение' })).toBeDisabled()
    const cleanupButton = screen.getByRole('button', { name: 'Отключить и восстановить сеть' })
    await user.click(cleanupButton)

    const disableCall = await waitFor(() => {
      const call = fetchMock.mock.calls.find(item => item[0] === '/api/v1/tunnel/disable')
      expect(call).toBeDefined()
      return call
    })
    expect(headers(disableCall).get('X-CSRF-Token')).toBe('csrf-test')
    expect(JSON.parse(String(disableCall?.[1]?.body))).toEqual({})
    await waitFor(() => expect(screen.getByRole('button', { name: 'Подготовить безопасное включение' })).toBeEnabled())
  })

  it('при failed journal без плана блокирует подготовку и оставляет повторный recovery доступным', async () => {
    const fetchMock = installMock(tunnelState('failed', {
      available: false,
      transaction_id: 'tx-corrupt',
      plan: undefined,
      message: 'Журнал транзакции недоступен; требуется повторное восстановление.',
    }))
    const user = userEvent.setup()
    render(<FullTunnel csrf="csrf-test" />)

    expect(await screen.findByRole('heading', { name: 'Ошибка' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'План сетевых изменений' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Подготовить безопасное включение' })).toBeDisabled()
    const cleanupButton = screen.getByRole('button', { name: 'Отключить и восстановить сеть' })
    expect(cleanupButton).toBeEnabled()
    await user.click(cleanupButton)

    const disableCall = await waitFor(() => {
      const call = fetchMock.mock.calls.find(item => item[0] === '/api/v1/tunnel/disable')
      expect(call).toBeDefined()
      return call
    })
    expect(headers(disableCall).get('X-CSRF-Token')).toBe('csrf-test')
    expect(JSON.parse(String(disableCall?.[1]?.body))).toEqual({})
  })
})
