// 轻量 fetch 封装:统一鉴权头、错误提示与 401 跳转。
import { useToast } from '../stores/toast'
import { AppError, resolveApiError } from './errors'
import { reportClientError } from './errorReporting'

export { AppError as ApiError } from './errors'

const TOKEN_KEY = 'dd_token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`

  let resp: Response
  try {
    resp = await fetch(path, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch {
    const error = new AppError(resolveApiError(0, null))
    reportClientError(error, 'network', `${method} ${path}`)
    throw error
  }

  let payload: any = null
  try {
    payload = await resp.json()
  } catch {
    /* non-JSON response */
  }

  if (!resp.ok) {
    const error = resolveApiError(resp.status, payload, resp.headers)
    const sessionInvalid = error.code === 'auth.required' || error.code === 'auth.session_expired' || error.code === 'auth.session_changed'
    if (resp.status === 401 && sessionInvalid && !path.startsWith('/api/auth')) {
      clearToken()
      window.location.href = '/login'
    }
    throw new AppError(error)
  }
  return payload?.data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  delete: <T>(path: string) => request<T>('DELETE', path),
}

/** Copy with a legacy fallback for local HTTP deployments and embedded browsers. */
export async function copyText(text: string): Promise<void> {
  if (!text) throw new Error('没有可复制的内容')

  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  textarea.remove()
  if (!copied) throw new Error('浏览器不允许写入剪贴板')
}

/** Downloads an authenticated console resource without exposing a bearer token
 * in a URL. Used for sensitive server-side database snapshots. */
export async function downloadFile(path: string, fallbackName: string): Promise<void> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  let response: Response
  try {
    response = await fetch(path, { headers })
  } catch {
    const error = new AppError(resolveApiError(0, null))
    reportClientError(error, 'network', `GET ${path}`)
    throw error
  }
  if (!response.ok) {
    let payload: unknown = null
    try { payload = await response.json() } catch { /* ignore non-JSON response */ }
    throw new AppError(resolveApiError(response.status, payload, response.headers))
  }
  const disposition = response.headers.get('Content-Disposition') || ''
  const match = disposition.match(/filename="?([^";]+)"?/i)
  const filename = match?.[1] || fallbackName
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 包一层带 toast 的调用,用于按钮操作。 */
export async function withToast<T>(fn: () => Promise<T>, success?: string): Promise<T | null> {
  const toast = useToast()
  try {
    const result = await fn()
    if (success) toast.show(success, 'success')
    return result
  } catch (e) {
		toast.showError(e, '操作失败，请稍后重试')
    return null
  }
}
