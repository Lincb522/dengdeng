type UnknownRecord = Record<string, unknown>

function isRecord(value: unknown): value is UnknownRecord {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function nestedMessage(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (!isRecord(value)) return ''
  for (const key of ['message', 'detail', 'error_description', 'description', 'reason']) {
    if (typeof value[key] === 'string' && value[key].trim()) return value[key].trim()
  }
  for (const key of ['error', 'errors']) {
    const nested = nestedMessage(value[key])
    if (nested) return nested
  }
  return ''
}

function messageFromPayload(payload: unknown): string {
  const direct = nestedMessage(payload)
  if (direct) return direct
  if (typeof payload !== 'string') return ''
  const raw = payload.trim()
  if (!raw) return ''
  try {
    const parsed = JSON.parse(raw) as unknown
    return nestedMessage(parsed) || raw
  } catch {
    // Some upstream messages prefix a JSON body with a HTTP status. Try the
    // JSON portion before falling back to the original text.
    const start = raw.indexOf('{')
    if (start >= 0) {
      try {
        const parsed = JSON.parse(raw.slice(start)) as unknown
        const nested = nestedMessage(parsed)
        if (nested) return nested
      } catch { /* plain text */ }
    }
    return raw
  }
}

const exactChinese: Record<string, string> = {
  'no available upstream account in this group': '该分组暂时没有可用的上游账号',
  'invalid request': '提交的信息有误，请检查后重试',
  'invalid JSON body': '请求内容不是有效的 JSON',
  'group not found': '所选分组不存在',
  'account not found': '该上游账号不存在',
  'key not found': '该 API 密钥不存在',
  'user not found': '该用户不存在',
  'model is required': '请选择模型',
  'missing API key': '缺少 API 密钥',
  'unauthorized': '认证失败，请检查登录状态或密钥',
  'forbidden': '没有执行此操作的权限',
}

function fallbackForStatus(status: number) {
  if (status === 0) return '网络连接失败，请检查网络后重试'
  if (status === 400) return '提交的信息有误，请检查后重试'
  if (status === 401) return '登录已失效，请重新登录'
  if (status === 403) return '没有执行此操作的权限'
  if (status === 404) return '请求的内容不存在或已被移除'
  if (status === 409) return '当前状态已变化，请刷新后重试'
  if (status === 413) return '请求内容过大，请减少上下文或附件后重试'
  if (status === 429) return '请求过于频繁，请稍后再试'
  if (status >= 500) return '服务暂时不可用，请稍后重试'
  return '操作未完成，请稍后重试'
}

export function localizeErrorMessage(raw: string, status = 0): string {
  const source = raw.replace(/\s+/g, ' ').trim()
  if (!source) return fallbackForStatus(status)
  if (/[㐀-鿿]/.test(source)) return source
  const normalized = source.toLowerCase()
  if (exactChinese[normalized]) return exactChinese[normalized]
  if (normalized.includes('payload too large') || normalized.includes('request entity too large')) return '请求内容过大，请减少上下文或附件后重试'
  if (normalized.includes('insufficient') && normalized.includes('quota')) return '上游账号额度不足，请更换账号或稍后重试'
  if (normalized.includes('rate limit') || normalized.includes('too many requests')) return '上游请求过于频繁，账号正在冷却'
  if (normalized.includes('invalid api key') || normalized.includes('incorrect api key')) return '上游 API 密钥无效，请检查账号配置'
	if (normalized.includes('oauth access token expired') || normalized.includes('token expired')) return 'OAuth 凭据已过期，请重新授权或导入'
  if (normalized.includes('token') && (normalized.includes('expired') || normalized.includes('invalid'))) return '上游登录凭据已失效，请重新授权或导入'
	if (normalized.includes('credential or endpoint returned')) return '上游凭据或接口地址校验未通过'
	if (normalized.includes('account health probe returned')) return '健康检查发现账号状态异常'
	if (normalized.includes('upstream returned')) return '上游服务返回异常状态'
	if (normalized.includes('connection refused') || normalized.includes('connection reset') || normalized.includes('connection error')) return '无法连接上游服务，请检查网络、代理或接口地址'
  if (normalized.includes('context_length') || normalized.includes('maximum context')) return '上下文超过模型允许的长度，请缩短输入后重试'
  if (normalized.includes('network error') || normalized.includes('failed to fetch') || normalized.includes('load failed')) return '网络连接失败，请检查网络或服务地址'
  if (normalized.includes('timeout') || normalized.includes('deadline exceeded')) return '请求超时，请稍后重试'
  if (normalized.includes('no available upstream account')) return '该分组暂时没有可用的上游账号'
  // Provider-specific English is kept out of normal toasts. The full text is
  // still available in the account diagnostic panel for administrators.
  return fallbackForStatus(status)
}

export function localizedApiError(status: number, payload: unknown): string {
  return localizeErrorMessage(messageFromPayload(payload), status)
}

export function summarizeProviderError(raw: string, max = 96): string {
  const summary = localizeErrorMessage(messageFromPayload(raw))
  return summary.length > max ? `${summary.slice(0, max - 1)}…` : summary
}
