import { isAppError } from './errors'

type ClientErrorSource = 'vue' | 'promise' | 'window' | 'network'

const recentlyReported = new Map<string, number>()
const dedupeWindowMs = 10_000

function safeText(value: string, limit: number) {
  return value
    .replace(/\b(dd-)[A-Za-z0-9_-]{12,}\b/g, '$1[redacted]')
    .replace(/\bBearer\s+[A-Za-z0-9._~-]{12,}\b/gi, 'Bearer [redacted]')
    .replace(/("?(?:access_token|refresh_token|api[_-]?key|password)"?\s*[:=]\s*")[^"]+/gi, '$1[redacted]')
    .slice(0, limit)
}

function errorParts(error: unknown) {
  if (error instanceof Error) {
    return {
      message: safeText(error.message || error.name || 'Unknown browser error', 2048),
      stack: safeText(error.stack || '', 12_000),
    }
  }
  if (typeof error === 'string') return { message: safeText(error, 2048), stack: '' }
  try {
    return { message: safeText(JSON.stringify(error), 2048), stack: '' }
  } catch {
    return { message: 'Unknown browser error', stack: '' }
  }
}

export function reportClientError(error: unknown, source: ClientErrorSource, context = '') {
  const parts = errorParts(error)
  const message = context ? `${context}: ${parts.message}` : parts.message
  const fingerprint = `${source}:${window.location.pathname}:${message}`
  const now = Date.now()
  if ((recentlyReported.get(fingerprint) || 0) > now - dedupeWindowMs) return
  recentlyReported.set(fingerprint, now)
  for (const [key, timestamp] of recentlyReported) {
    if (timestamp < now - dedupeWindowMs) recentlyReported.delete(key)
  }

  const payload = JSON.stringify({
    source,
    error_code: isAppError(error) ? error.code : 'frontend.runtime_error',
    message,
    stack: parts.stack,
    path: window.location.pathname,
    request_id: isAppError(error) ? error.requestId : '',
  })
  void fetch('/api/site-errors', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: payload,
    keepalive: payload.length < 60_000,
  }).catch(() => undefined)
}
