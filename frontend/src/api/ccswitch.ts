export interface CCSwitchImportOptions {
  origin: string
  apiKey: string
  platform: string
  keyName: string
  model?: string
}

export const CCSWITCH_USAGE_SCRIPT = `({
  request: {
    url: "{{baseUrl}}/v1/usage",
    method: "GET",
    headers: { "Authorization": "Bearer {{apiKey}}" }
  },
  extractor: function(response) {
    const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
    const unit = response?.unit ?? response?.quota?.unit ?? "USD";
    return {
      isValid: response?.is_active ?? response?.isValid ?? true,
      remaining,
      unit
    };
  }
})`

function utf8Base64(value: string) {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return window.btoa(binary)
}

export function buildCCSwitchImportConfig(options: CCSwitchImportOptions) {
  const origin = options.origin.replace(/\/$/, '')
  const apiBase = `${origin}/v1`
  const isOpenAICompatible = options.platform === 'openai' || options.platform === 'grok'

  return {
    resource: 'provider',
    app: options.platform === 'anthropic' ? 'claude' : options.platform === 'gemini' ? 'gemini' : 'codex',
    name: `DengDeng AI · ${options.keyName || 'API Key'}`,
    homepage: origin,
    endpoint: isOpenAICompatible ? apiBase : origin,
    apiKey: options.apiKey,
    model: options.model || undefined,
    configFormat: 'json',
    usageEnabled: true,
    usageBaseUrl: origin,
    usageApiKey: options.apiKey,
    usageScript: utf8Base64(CCSWITCH_USAGE_SCRIPT),
    usageAutoInterval: 30,
    enabled: true,
  }
}

export function buildCCSwitchImportLink(options: CCSwitchImportOptions) {
  const params = new URLSearchParams()
  Object.entries(buildCCSwitchImportConfig(options)).forEach(([key, value]) => {
    if (value !== undefined) params.set(key, String(value))
  })
  return `ccswitch://v1/import?${params.toString()}`
}
