import type { ApiEnvelope } from './types'

let csrfToken = sessionStorage.getItem('feather_csrf') || ''

export function setCSRF(value: string) {
  csrfToken = value
  if (value) sessionStorage.setItem('feather_csrf', value)
  else sessionStorage.removeItem('feather_csrf')
}

export class ApiError extends Error {
  constructor(message: string, public code = 'REQUEST_FAILED', public requestId = '') {
    super(message)
  }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  if (csrfToken && options.method && !['GET', 'HEAD'].includes(options.method)) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(`/api/v1${path}`, { ...options, headers, credentials: 'same-origin' })
  let payload: ApiEnvelope<T>
  try {
    payload = await response.json()
  } catch {
    throw new ApiError('服务器返回了无法识别的响应')
  }
  if (!response.ok || !payload.success) {
    if (response.status === 401 && !path.includes('/auth/')) window.dispatchEvent(new CustomEvent('feather:unauthorized'))
    throw new ApiError(payload.error?.message || '请求失败，请稍后重试', payload.error?.code, payload.request_id)
  }
  return payload.data
}

export const postJSON = <T>(path: string, data?: unknown) => api<T>(path, { method: 'POST', body: data ? JSON.stringify(data) : undefined })
export const putJSON = <T>(path: string, data: unknown) => api<T>(path, { method: 'PUT', body: JSON.stringify(data) })
export const deleteJSON = <T>(path: string) => api<T>(path, { method: 'DELETE' })
