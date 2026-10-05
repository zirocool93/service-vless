export type User = { username: string; role?: string }
export type Session = { authenticated: boolean; user?: User; csrfToken?: string }
export type Connection = {
  id: string
  name: string
  kind: string
  subscription_id?: string
  enabled: boolean
  favorite: boolean
  stale?: boolean
  latency_ms?: number
  last_error?: string
}
export type ConnectionStatus = { state: string; message?: string; active_node_id?: string; exit_ip?: string }
export type Subscription = { id: string; name: string; url: string; enabled: boolean; update_interval: number }

export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message) }
}

let unauthorizedHandler: (() => void) | undefined
export function setUnauthorizedHandler(handler?: () => void) { unauthorizedHandler = handler }

export async function api<T>(path: string, options: RequestInit = {}, csrfToken?: string): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  headers.set('Accept', 'application/json')
  if (csrfToken && options.method && !['GET', 'HEAD', 'OPTIONS'].includes(options.method.toUpperCase())) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const response = await fetch(path, { ...options, headers, credentials: 'same-origin' })
  if (response.status === 401 && !path.endsWith('/auth/login') && !path.endsWith('/auth/session')) unauthorizedHandler?.()
  const body = response.status === 204 ? null : await response.json().catch(() => null) as unknown
  if (!response.ok) {
    const message = body && typeof body === 'object' && 'message' in body && typeof body.message === 'string'
      ? body.message : body && typeof body === 'object' && 'error' in body && typeof body.error === 'string'
        ? body.error : `Ошибка API (${response.status})`
    throw new ApiError(message, response.status)
  }
  return body as T
}
