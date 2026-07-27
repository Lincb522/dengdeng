type UnknownRecord = Record<string, unknown>

export interface ResolvedAppError {
  status: number
  code: string
  title: string
  message: string
  action?: string
  requestId?: string
  retryAfter: number
  retryable: boolean
}

interface ErrorCopy {
  title: string
  message: string
  action?: string
  retryable?: boolean
}

export class AppError extends Error {
  status: number
  code: string
  title: string
  action?: string
  requestId?: string
  retryAfter: number
  retryable: boolean

  constructor(error: ResolvedAppError) {
    super(error.message)
    this.name = 'AppError'
    this.status = error.status
    this.code = error.code
    this.title = error.title
    this.action = error.action
    this.requestId = error.requestId
    this.retryAfter = error.retryAfter
    this.retryable = error.retryable
  }
}

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

function payloadString(payload: unknown, key: string) {
  if (!isRecord(payload)) return ''
  const value = payload[key]
  return typeof value === 'string' ? value.trim() : ''
}

function payloadNumber(payload: unknown, key: string) {
  if (!isRecord(payload)) return 0
  const value = Number(payload[key])
  return Number.isFinite(value) && value > 0 ? Math.ceil(value) : 0
}

function retryAfterFromHeaders(headers?: Headers) {
  if (!headers) return 0
  const raw = headers.get('Retry-After') || ''
  const seconds = Number(raw)
  if (Number.isFinite(seconds) && seconds > 0) return Math.ceil(seconds)
  const date = Date.parse(raw)
  return Number.isFinite(date) ? Math.max(0, Math.ceil((date - Date.now()) / 1000)) : 0
}

function formatWait(seconds: number) {
  if (seconds < 60) return `${Math.max(1, seconds)} 秒`
  const minutes = Math.ceil(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.ceil(minutes / 60)
  return `${hours} 小时`
}

const errorCopy: Record<string, ErrorCopy> = {
  'network.offline': { title: '网络连接失败', message: '无法连接服务器。', action: '请检查网络和服务地址后重试。', retryable: true },
  'client.runtime': { title: '页面运行异常', message: '当前页面没有正常完成操作。', action: '请刷新页面；如果仍然出现，请提供请求编号或截图。', retryable: true },
  'frontend.runtime_error': { title: '页面运行异常', message: '当前页面没有正常完成操作。', action: '请刷新页面；如果仍然出现，请提供请求编号或截图。', retryable: true },
  'client.chunk_load': { title: '页面资源加载失败', message: '站点可能刚刚更新。', action: '请刷新页面以加载最新版本。', retryable: true },
  'request.invalid': { title: '提交内容有误', message: '请检查填写内容后重试。' },
  'request.invalid_value': { title: '参数取值不受支持', message: '请求中包含当前接口不支持的参数或取值。', action: '请检查接口格式、字段类型和可选值。' },
  'request.invalid_json': { title: '请求格式错误', message: '提交内容不是有效的 JSON。' },
  'request.too_large': { title: '请求内容过大', message: '上下文或附件超过服务器允许的大小。', action: '请缩短上下文或减少附件后重试。' },
  'request.content_rejected': { title: '请求内容未通过上游检查', message: '上游拒绝处理当前输入内容。', action: '请调整输入内容后重试。' },
  'request.rate_limited': { title: '操作过于频繁', message: '当前请求已被暂时限制。', retryable: true },
  'request.concurrency_limited': { title: '请求正在排队', message: '当前并发已满。', action: '请等待其他请求结束后重试。', retryable: true },
  'auth.invalid_credentials': { title: '登录失败', message: '邮箱或密码不正确。', action: '请重新输入，连续错误 5 次会暂时锁定登录。' },
  'auth.too_many_attempts': { title: '暂时无法登录', message: '密码错误次数过多。', action: '请确认密码，或使用“忘记密码”重置。', retryable: true },
  'auth.totp_invalid': { title: '验证码不正确', message: '请输入当前有效的身份验证器验证码。' },
  'auth.account_disabled': { title: '账户已停用', message: '该账户目前无法登录。', action: '请联系管理员处理。' },
  'auth.session_expired': { title: '登录已失效', message: '当前会话已过期。', action: '请重新登录。' },
  'auth.session_changed': { title: '登录环境已变化', message: '为保护账户，本次会话已结束。', action: '请在当前设备重新登录。' },
  'auth.required': { title: '需要登录', message: '请登录后继续操作。' },
  'auth.terms_required': { title: '需要确认协议', message: '请阅读并同意最新协议后继续。' },
  'auth.email_registered': { title: '邮箱已注册', message: '该邮箱已经有账户。', action: '请直接登录或重置密码。' },
  'auth.verification_code_invalid': { title: '验证码无效', message: '验证码错误或已过期。', action: '请重新获取验证码。' },
  'auth.password_reset_invalid': { title: '无法重置密码', message: '重置验证码错误或已过期。', action: '请重新获取验证码。' },
  'auth.code_rate_limited': { title: '验证码已发送', message: '请不要重复发送。', retryable: true },
  'auth.oauth_failed': { title: '第三方登录失败', message: '授权没有完成或登录凭据已失效。', action: '请重新发起授权。', retryable: true },
  'permission.denied': { title: '没有权限', message: '当前账户不能执行此操作。' },
  'permission.admin_required': { title: '仅管理员可用', message: '请使用管理员账户操作。' },
  'permission.step_up_required': { title: '需要二次验证', message: '请先完成身份验证器确认。' },
  'resource.not_found': { title: '内容不存在', message: '目标可能已被删除或移动。' },
  'resource.conflict': { title: '状态已发生变化', message: '当前操作与最新数据冲突。', action: '请刷新页面后重试。', retryable: true },
  'resource.disabled': { title: '功能未启用', message: '当前功能或资源已被管理员停用。' },
  'api_key.missing': { title: '缺少 API 密钥', message: '请填写密钥后重试。' },
  'api_key.invalid': { title: 'API 密钥无效', message: '密钥不存在或内容不完整。', action: '请重新复制完整密钥。' },
  'api_key.disabled': { title: 'API 密钥已停用', message: '请启用密钥或更换其他密钥。' },
  'api_key.expired': { title: 'API 密钥已过期', message: '请调整有效期或创建新密钥。' },
  'api_key.ip_denied': { title: '当前 IP 不允许访问', message: '该密钥配置了 IP 白名单或黑名单。' },
  'api_key.rate_limited': { title: '密钥请求过于频繁', message: '该密钥已达到每分钟请求上限。', retryable: true },
  'api_key.not_found': { title: '密钥不存在', message: '该 API 密钥可能已经被删除。' },
  'api_key.secret_unavailable': { title: '无法查看完整密钥', message: '该旧密钥尚未保存可恢复的密文。', action: '请补入一次原密钥或重新生成。' },
  'api_key.secret_mismatch': { title: '密钥不匹配', message: '输入内容与当前密钥不一致。' },
  'user.rate_limited': { title: '账户请求过于频繁', message: '账户已达到每分钟请求上限。', retryable: true },
  'group.not_found': { title: '分组不存在', message: '所选分组可能已经被删除。' },
  'group.disabled': { title: '分组已停用', message: '请选择其他开放分组。' },
  'account.not_found': { title: '上游账号不存在', message: '该账号可能已经被删除。' },
  'model.unsupported': { title: '当前账号不支持该模型', message: '所选模型与上游账号、套餐或接口不兼容。', action: '请更换模型、上游账号或分组。' },
  'quota.unavailable': { title: '额度不可用', message: '账户、密钥或上游额度不足。', action: '请检查余额和额度设置。' },
  'upstream.unavailable': { title: '暂无可用上游账号', message: '所选分组当前没有可用账号。', action: '请稍后重试或切换分组。', retryable: true },
  'upstream.busy': { title: '上游账号繁忙', message: '可用账号当前并发已满。', action: '请稍后重试。', retryable: true },
  'upstream.authentication_failed': { title: '上游账号认证失败', message: '上游账号的登录状态、令牌或接口权限不可用。', action: '请刷新账号凭据、重新登录或更换账号。', retryable: true },
  'upstream.rate_limited': { title: '上游请求受限', message: '上游账号正在冷却。', action: '请等待额度窗口恢复或切换账号。', retryable: true },
  'upstream.failed': { title: '上游服务异常', message: '上游没有正常完成请求。', action: '请稍后重试；管理员可在用量明细查看错误链路。', retryable: true },
  'upstream.empty_response': { title: '上游返回了空响应', message: '请求已到达上游，但没有收到可用的回复内容。', action: '请重试；如果持续出现，请检查账号和协议转换配置。', retryable: true },
  'upstream.invalid_response': { title: '上游响应格式异常', message: '上游返回的内容无法正常解析。', action: '请检查代理、协议转换和上游服务状态。', retryable: true },
  'payment.order_not_found': { title: '订单不存在', message: '订单可能已过期或已被清理。' },
  'payment.disabled': { title: '支付暂不可用', message: '在线支付渠道尚未启用。' },
  'payment.failed': { title: '支付操作失败', message: '订单没有正常创建或核验。', action: '请不要重复付款，先刷新订单状态。', retryable: true },
  'email.unavailable': { title: '邮件发送失败', message: '邮件服务暂时不可用。', action: '请稍后重试或联系管理员。', retryable: true },
  'proxy.failed': { title: '代理连接失败', message: '代理地址、认证信息或网络不可用。' },
  'referral.invalid': { title: '推广码不可用', message: '推广码无效、已停用或已经绑定。' },
  'backup.failed': { title: '备份操作失败', message: '数据库备份没有正常完成。', action: '请检查存储空间和备份配置。', retryable: true },
  'update.failed': { title: '版本更新失败', message: '更新任务没有正常完成。', action: '请查看更新记录或回滚到上一版本。', retryable: true },
  'service.unavailable': { title: '服务暂不可用', message: '相关服务尚未配置完成或正在维护。', retryable: true },
  'server.internal': { title: '服务器处理失败', message: '服务器没有正常完成操作。', action: '请稍后重试；如持续出现，请提供请求编号。', retryable: true },
  'operation.failed': { title: '操作未完成', message: '请检查当前状态后重试。', retryable: true },
}

const exactLegacyCodes: Record<string, string> = {
  'no available upstream account in this group': 'upstream.unavailable',
  'no available upstream account in the selected groups': 'upstream.unavailable',
  'invalid request': 'request.invalid',
  'invalid json body': 'request.invalid_json',
  'incorrect email or password': 'auth.invalid_credentials',
  'too many failed attempts, try again later': 'auth.too_many_attempts',
  'missing api key': 'api_key.missing',
  'invalid api key': 'api_key.invalid',
  'api key disabled': 'api_key.disabled',
  'api key expired': 'api_key.expired',
  'api key source ip is not allowed': 'api_key.ip_denied',
  'api key rate limit reached': 'api_key.rate_limited',
  'user rate limit reached': 'user.rate_limited',
  'concurrency limit reached; retry later': 'request.concurrency_limited',
  'upstream accounts are busy; retry later': 'upstream.busy',
  'account disabled': 'auth.account_disabled',
  'latest terms must be accepted': 'auth.terms_required',
  'key not found': 'api_key.not_found',
  'group not found': 'group.not_found',
  'account not found': 'account.not_found',
  'payment order not found': 'payment.order_not_found',
}

function fallbackCode(status: number, raw: string) {
  const normalized = raw.toLowerCase()
  if (exactLegacyCodes[normalized]) return exactLegacyCodes[normalized]
  if (normalized.includes('too many failed')) return 'auth.too_many_attempts'
  if (normalized.includes('incorrect email') || normalized.includes('incorrect password')) return 'auth.invalid_credentials'
  if (normalized.includes('payload too large') || normalized.includes('request entity too large')) return 'request.too_large'
  if (normalized.includes('context_length') || normalized.includes('maximum context')) return 'request.too_large'
  if (normalized.includes('invalid value') || normalized.includes('supported values are') || normalized.includes('invalid tool parameters')) return 'request.invalid_value'
  if (normalized.includes('not supported') || normalized.includes('unsupported model') || normalized.includes('model does not exist')) return 'model.unsupported'
  if (normalized.includes('empty or malformed response') || normalized.includes('empty response') || normalized.includes('without output') || normalized.includes('without an image result')) return 'upstream.empty_response'
  if (normalized.includes('invalid json') || normalized.includes('non-json response') || normalized.includes('response conversion failed')) return 'upstream.invalid_response'
  if (normalized.includes('content policy') || normalized.includes('safety policy') || normalized.includes('content_filter')) return 'request.content_rejected'
  if (normalized.includes('insufficient permissions') || normalized.includes('missing_scope')) return 'permission.denied'
  if (normalized.includes('insufficient') && (normalized.includes('quota') || normalized.includes('balance'))) return 'quota.unavailable'
  if (normalized.includes('rate limit') || normalized.includes('too many requests')) return 'upstream.rate_limited'
  if (normalized.includes('concurrency') || normalized.includes('busy')) return 'request.concurrency_limited'
  if (normalized.includes('invalid api key') || normalized.includes('incorrect api key')) return 'api_key.invalid'
  if (normalized.includes('oauth') || normalized.includes('token expired')) return 'auth.oauth_failed'
  if (normalized.includes('no available upstream account')) return 'upstream.unavailable'
  if (normalized.includes('upstream')) return 'upstream.failed'
  if (normalized.includes('payment') || normalized.includes('order')) return 'payment.failed'
  if (normalized.includes('email') && (normalized.includes('send') || normalized.includes('service'))) return 'email.unavailable'
  if (normalized.includes('proxy') || normalized.includes('connection refused') || normalized.includes('connection reset')) return 'proxy.failed'
  if (normalized.includes('timeout') || normalized.includes('deadline exceeded')) return 'upstream.failed'
  if (normalized.includes('failed to fetch') || normalized.includes('network error') || normalized.includes('load failed')) return 'network.offline'
  if (normalized.includes('dynamically imported module') || normalized.includes('loading chunk')) return 'client.chunk_load'
  if (status === 400) return 'request.invalid'
  if (status === 401) return 'auth.required'
  if (status === 403) return 'permission.denied'
  if (status === 404) return 'resource.not_found'
  if (status === 409) return 'resource.conflict'
  if (status === 413) return 'request.too_large'
  if (status === 429) return 'request.rate_limited'
  if (status === 502) return 'upstream.failed'
  if (status === 503) return 'service.unavailable'
  if (status >= 500) return 'server.internal'
  if (status === 0) return 'network.offline'
  return 'operation.failed'
}

export function resolveApiError(status: number, payload: unknown, headers?: Headers): ResolvedAppError {
  const raw = messageFromPayload(payload).replace(/\s+/g, ' ').trim()
  const backendCode = payloadString(payload, 'error_code')
  const code = backendCode || fallbackCode(status, raw)
  const copy = errorCopy[code] || errorCopy[fallbackCode(status, raw)] || errorCopy['operation.failed']
  const retryAfter = payloadNumber(payload, 'retry_after_seconds') || retryAfterFromHeaders(headers)
  const requestId = payloadString(payload, 'request_id') || headers?.get('X-Request-ID') || undefined
  let message = copy.message
  let action = copy.action

  if (retryAfter > 0) {
    if (code === 'auth.too_many_attempts') message = `密码错误次数过多，请 ${formatWait(retryAfter)}后重试。`
    else if (code === 'auth.code_rate_limited') message = `验证码已发送，请 ${formatWait(retryAfter)}后再试。`
    else if (code.endsWith('rate_limited')) message = `当前请求已被限制，请 ${formatWait(retryAfter)}后重试。`
  }

  // Existing Chinese backend messages are operator-authored and often contain
  // useful domain context. Keep them unless the error is an internal 5xx.
  if (raw && /[㐀-鿿]/.test(raw) && status < 500 && !backendCode) message = raw

  return {
    status,
    code,
    title: copy.title,
    message,
    action,
    requestId,
    retryAfter,
    retryable: Boolean(copy.retryable || retryAfter > 0 || status >= 500),
  }
}

export function localizeErrorMessage(raw: string, status = 0): string {
  return resolveApiError(status, { message: raw }).message
}

export function localizedApiError(status: number, payload: unknown): string {
  return resolveApiError(status, payload).message
}

export function summarizeProviderError(raw: string, max = 96): string {
  const summary = resolveApiError(0, { message: messageFromPayload(raw) }).message
  return summary.length > max ? `${summary.slice(0, max - 1)}…` : summary
}

export function isAppError(error: unknown): error is AppError {
  return error instanceof AppError
}
