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

export interface UploadRequest<T> {
  promise: Promise<T>
  cancel: () => void
}

export function uploadFile<T>(
  path: string,
  body: FormData,
  onProgress: (percent: number) => void,
): UploadRequest<T> {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<T>((resolve, reject) => {
    xhr.open('POST', `/api/v1${path}`)
    xhr.withCredentials = true
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress(Math.min(99, Math.round((event.loaded / event.total) * 100)))
      }
    }
    xhr.onload = () => {
      let payload: ApiEnvelope<T>
      try {
        payload = JSON.parse(xhr.responseText) as ApiEnvelope<T>
      } catch {
        reject(new ApiError('服务器返回了无法识别的响应'))
        return
      }
      if (xhr.status < 200 || xhr.status >= 300 || !payload.success) {
        if (xhr.status === 401 && !path.includes('/auth/')) window.dispatchEvent(new CustomEvent('feather:unauthorized'))
        reject(new ApiError(payload.error?.message || '上传失败，请稍后重试', payload.error?.code, payload.request_id))
        return
      }
      resolve(payload.data)
    }
    xhr.onerror = () => reject(new ApiError('网络连接中断，请检查网络后重试', 'NETWORK_ERROR'))
    xhr.onabort = () => reject(new ApiError('上传已取消', 'UPLOAD_ABORTED'))
    xhr.send(body)
  })
  return { promise, cancel: () => xhr.abort() }
}
