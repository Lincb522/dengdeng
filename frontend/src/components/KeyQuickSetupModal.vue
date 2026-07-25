<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, copyText } from '../api/client'
import { localizedApiError, localizeErrorMessage } from '../api/errors'
import { normalizeReasoningEffort, OFFICIAL_REASONING_EFFORTS, REASONING_OPTIONS } from '../api/reasoning'
import { PLATFORM_LABELS } from '../api/types'
import { useToast } from '../stores/toast'

type ClientID = 'claude' | 'codex' | 'gemini' | 'chatbox' | 'cline' | 'opencode' | 'ccswitch' | 'cherry' | 'nextchat' | 'continue'
type ShellID = 'unix' | 'cmd' | 'powershell' | 'windows'

interface SetupFile {
  path: string
  content: string
  hint?: string
}

const props = defineProps<{ show: boolean; apiKey: string; keyId: number | null; keyName: string; keyPreview: string; platform: string; platforms: string[]; reasoningEffort: string }>()
const emit = defineEmits<{ close: []; rotate: []; forget: []; 'effort-updated': [value: string] }>()
const toast = useToast()

const activeClient = ref<ClientID>('codex')
const activeShell = ref<ShellID>('unix')
const copied = ref('')
const models = ref<string[]>([])
const selectedModel = ref('')
const modelsState = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
const modelsError = ref('')
const workingApiKey = ref('')
const activePlatform = ref(props.platform || 'openai')
// 创建之后也能直接在这里调整默认思考强度，改动即时保存到该密钥。
const reasoningEffort = ref('auto')
const savingEffort = ref(false)

const clientDownloads: Record<ClientID, { label: string; action: string; url: string }> = {
  claude: { label: 'Claude Code', action: '下载 Claude Code', url: 'https://github.com/anthropics/claude-code/releases/latest' },
  codex: { label: 'Codex CLI', action: '下载 Codex CLI', url: 'https://github.com/openai/codex/releases/latest' },
  gemini: { label: 'Gemini CLI', action: '下载 Gemini CLI', url: 'https://github.com/google-gemini/gemini-cli/releases/latest' },
  chatbox: { label: 'Chatbox', action: '下载 Chatbox', url: 'https://github.com/chatboxai/chatbox/releases/latest' },
  cline: { label: 'Cline', action: '打开扩展商店', url: 'https://marketplace.visualstudio.com/items?itemName=saoudrizwan.claude-dev' },
  opencode: { label: 'OpenCode', action: '下载 OpenCode', url: 'https://github.com/anomalyco/opencode/releases/latest' },
  ccswitch: { label: 'CCSwitch', action: '下载 CCSwitch', url: 'https://github.com/farion1231/cc-switch/releases/latest' },
  cherry: { label: 'Cherry Studio', action: '下载 Cherry Studio', url: 'https://github.com/CherryHQ/cherry-studio/releases/latest' },
  nextchat: { label: 'NextChat', action: '下载 NextChat', url: 'https://github.com/ChatGPTNextWeb/NextChat/releases/latest' },
  continue: { label: 'Continue', action: '打开扩展商店', url: 'https://marketplace.visualstudio.com/items?itemName=Continue.continue' },
}
const downloadClients: ClientID[] = [
  'claude',
  'codex',
  'gemini',
  'chatbox',
  'cherry',
  'nextchat',
  'cline',
  'continue',
  'opencode',
  'ccswitch',
]

const origin = computed(() => window.location.origin.replace(/\/$/, ''))
const apiBase = computed(() => `${origin.value}/v1`)
const geminiBase = computed(() => `${origin.value}/v1beta`)
function validDengDengApiKey(value: string) {
  const normalized = value.trim()
  return /^dd-[A-Za-z0-9]{20,}$/.test(normalized) ? normalized : ''
}

const configuredApiKey = computed(() => validDengDengApiKey(workingApiKey.value))
const invalidApiKeyInput = computed(() => !!workingApiKey.value.trim() && !configuredApiKey.value)
const isOpenAICompatible = computed(() => activePlatform.value === 'openai' || activePlatform.value === 'grok')
const availablePlatforms = computed(() => {
  const values = props.platforms?.length ? props.platforms : [props.platform || 'openai']
  return [...new Set(values.filter(Boolean))]
})
const activeEndpoint = computed(() => {
  if (activeClient.value === 'claude') return origin.value
  if (activeClient.value === 'gemini') return geminiBase.value
  if (activeClient.value === 'ccswitch') return isOpenAICompatible.value ? apiBase.value : origin.value
  return apiBase.value
})

async function changeReasoningEffort(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  const previous = reasoningEffort.value
  if (!props.keyId || value === previous) {
    reasoningEffort.value = value
    return
  }
  savingEffort.value = true
  reasoningEffort.value = value
  try {
    await api.put(`/api/user/keys/${props.keyId}`, { reasoning_effort: value })
    toast.show('默认思考强度已更新', 'success')
    emit('effort-updated', value)
  } catch (error) {
    reasoningEffort.value = previous
    toast.show(error instanceof Error ? localizeErrorMessage(error.message) : '保存失败', 'error')
  } finally {
    savingEffort.value = false
  }
}

function quickSetupStorageKey() {
  return props.keyId ? `dengdeng.quick-setup.key.${props.keyId}` : ''
}

function matchesKeyPreview(value: string) {
	const [prefix, suffix] = (props.keyPreview || '').split('...')
	return !!value && (!prefix || value.startsWith(prefix)) && (!suffix || value.endsWith(suffix))
}

function readRememberedApiKey() {
  const storageKey = quickSetupStorageKey()
  if (!storageKey) return ''
  try {
	const persistent = localStorage.getItem(storageKey) || ''
	if (persistent) {
		if (matchesKeyPreview(persistent)) return persistent
		localStorage.removeItem(storageKey)
	}
	const legacy = sessionStorage.getItem(storageKey) || ''
	if (legacy && matchesKeyPreview(legacy)) {
		localStorage.setItem(storageKey, legacy)
		sessionStorage.removeItem(storageKey)
		return legacy
	}
	if (legacy) sessionStorage.removeItem(storageKey)
	return ''
  } catch {
    return ''
  }
}

function rememberApiKey(value: string) {
  const storageKey = quickSetupStorageKey()
  if (!storageKey) return
  try {
    const normalized = validDengDengApiKey(value)
	if (normalized) {
		localStorage.setItem(storageKey, normalized)
		sessionStorage.removeItem(storageKey)
	} else if (!value.trim()) {
		localStorage.removeItem(storageKey)
		sessionStorage.removeItem(storageKey)
	}
  } catch {
	// A restricted browser can still use the pasted key in memory.
  }
}

function forgetApiKey() {
	const storageKey = quickSetupStorageKey()
	try {
		if (storageKey) {
			localStorage.removeItem(storageKey)
			sessionStorage.removeItem(storageKey)
		}
	} catch { /* storage is optional */ }
	workingApiKey.value = ''
	emit('forget')
	toast.show('已清除本机保存的密钥', 'success')
}

const clientOptions = computed(() => {
  if (activePlatform.value === 'anthropic') {
    return [
      { id: 'claude' as const, label: 'Claude Code' },
      { id: 'codex' as const, label: 'Codex CLI' },
      { id: 'opencode' as const, label: 'OpenCode' },
      { id: 'ccswitch' as const, label: 'CCSwitch' },
    ]
  }
  if (activePlatform.value === 'gemini') {
    return [
      { id: 'gemini' as const, label: 'Gemini CLI' },
      { id: 'opencode' as const, label: 'OpenCode' },
      { id: 'ccswitch' as const, label: 'CCSwitch' },
    ]
  }
  return [
    { id: 'codex' as const, label: 'Codex CLI' },
    { id: 'claude' as const, label: 'Claude Code' },
    { id: 'chatbox' as const, label: 'Chatbox' },
	{ id: 'cherry' as const, label: 'Cherry Studio' },
	{ id: 'nextchat' as const, label: 'NextChat' },
    { id: 'cline' as const, label: 'Cline' },
	{ id: 'continue' as const, label: 'Continue' },
    { id: 'opencode' as const, label: 'OpenCode' },
    { id: 'ccswitch' as const, label: 'CCSwitch' },
  ]
})

const shellOptions = computed(() => {
  if (activeClient.value === 'codex') {
    return [
      { id: 'unix' as const, label: 'macOS / Linux' },
      { id: 'windows' as const, label: 'Windows' },
    ]
  }
  if (activeClient.value === 'claude' || activeClient.value === 'gemini') {
    return [
      { id: 'unix' as const, label: 'macOS / Linux' },
      { id: 'cmd' as const, label: 'Windows CMD' },
      { id: 'powershell' as const, label: 'PowerShell' },
    ]
  }
  return []
})

const activeDescription = computed(() => {
  const descriptions: Record<ClientID, string> = {
    claude: activePlatform.value === 'openai'
      ? 'Claude Code 会通过兼容层使用当前 OpenAI / Codex 分组。'
      : '复制终端环境变量；也提供 Claude Code 的持久化 settings.json 文件。',
    codex: activePlatform.value === 'anthropic'
      ? 'Codex CLI 会通过兼容层使用当前 Claude 分组。'
      : 'Codex CLI 需要 config.toml 和 auth.json 两个文件，分别复制到 ~/.codex 目录。',
    gemini: '使用环境变量启动 Gemini CLI；模型列表来自当前密钥所属分组。',
    chatbox: '在 Chatbox 中新建「OpenAI API」提供方，依次填入下面三项。',
    cline: '在 VS Code 的 Cline 设置中选择 OpenAI Compatible，再填入下面三项。',
    opencode: '把 provider 段合并进现有的 opencode.json；不要覆盖已有配置。',
    ccswitch: '通过系统 deeplink 打开 CCSwitch。导入前可先检查下方的配置预览。',
	cherry: '在 Cherry Studio 中新增 OpenAI 兼容服务商，然后填写接口、密钥和模型。',
	nextchat: '在 NextChat 的自定义接口设置中填写 OpenAI 兼容地址和当前密钥。',
	continue: '把 DengDeng AI 模型段合并到 Continue 的 config.yaml，不要覆盖已有模型。',
  }
  return descriptions[activeClient.value]
})

const selectedModelLabel = computed(() => selectedModel.value || '暂未读取到模型')

watch([() => props.show, () => props.platform, () => props.platforms.join(','), () => props.apiKey, () => props.keyId], ([show]) => {
	activePlatform.value = availablePlatforms.value.includes(props.platform) ? props.platform : (availablePlatforms.value[0] || 'openai')
  activeClient.value = clientOptions.value[0]?.id || 'codex'
  activeShell.value = 'unix'
  copied.value = ''
  models.value = []
  selectedModel.value = ''
  modelsState.value = 'idle'
  modelsError.value = ''
  reasoningEffort.value = normalizeReasoningEffort(props.reasoningEffort)
  if (!show) return
  workingApiKey.value = props.apiKey.trim() || readRememberedApiKey()
  if (configuredApiKey.value) void loadModels()
}, { immediate: true })

watch(() => props.reasoningEffort, (value) => {
  reasoningEffort.value = normalizeReasoningEffort(value)
})

watch(workingApiKey, rememberApiKey)

watch(activeClient, () => {
  activeShell.value = 'unix'
})

watch(activePlatform, () => {
	activeClient.value = clientOptions.value[0]?.id || 'codex'
	models.value = []
	selectedModel.value = ''
	modelsState.value = 'idle'
	modelsError.value = ''
	if (props.show && configuredApiKey.value) void loadModels()
})

async function loadModels() {
  const apiKey = configuredApiKey.value
  if (!apiKey) {
    models.value = []
    selectedModel.value = ''
    modelsState.value = invalidApiKeyInput.value ? 'error' : 'idle'
    modelsError.value = invalidApiKeyInput.value ? '请输入完整的 dd- 密钥；浏览器可能自动填入了登录密码' : ''
    return
  }
  modelsState.value = 'loading'
  modelsError.value = ''
  try {
    const response = await fetch(`${apiBase.value}/models?platform=${encodeURIComponent(activePlatform.value)}`, {
      headers: { Authorization: `Bearer ${apiKey}` },
    })
    const payload = await response.json().catch(() => null)
		if (!response.ok) throw new Error(localizedApiError(response.status, payload))
    const items = Array.isArray(payload?.data) ? payload.data : []
    models.value = items.map((item: { id?: unknown }) => typeof item?.id === 'string' ? item.id : '').filter(Boolean)
    if (!models.value.includes(selectedModel.value)) selectedModel.value = models.value[0] || ''
    modelsState.value = 'ready'
  } catch (error) {
    models.value = []
    selectedModel.value = ''
		modelsError.value = error instanceof Error ? localizeErrorMessage(error.message) : '读取模型失败，请稍后重试'
    modelsState.value = 'error'
  }
}

function shellLine(unix: string, cmd: string, powershell: string) {
  if (activeShell.value === 'cmd') return cmd
  if (activeShell.value === 'powershell') return powershell
  return unix
}

function quotedModelEnv(name: string) {
  if (!selectedModel.value) return ''
  return shellLine(
    `\nexport ${name}="${selectedModel.value}"`,
    `\nset ${name}=${selectedModel.value}`,
    `\n$env:${name}="${selectedModel.value}"`,
  )
}

function codexConfigToml(model: string) {
  const effort = reasoningEffort.value
  const reasoningLine = OFFICIAL_REASONING_EFFORTS.includes(effort as (typeof OFFICIAL_REASONING_EFFORTS)[number])
    ? `\nmodel_reasoning_effort = "${effort}"`
    : ''
  return `model_provider = "dengdeng"
model = "${model}"
review_model = "${model}"
cli_auth_credentials_store = "file"${reasoningLine}

[model_providers.dengdeng]
name = "DengDeng AI"
base_url = "${apiBase.value}"
wire_api = "responses"
requires_openai_auth = true`
}

const currentFiles = computed<SetupFile[]>(() => {
  const key = configuredApiKey.value
  const model = selectedModel.value

  if (activeClient.value === 'claude') {
    const terminalPath = activeShell.value === 'cmd' ? 'Command Prompt' : activeShell.value === 'powershell' ? 'PowerShell' : 'Terminal'
    const terminal = shellLine(
      `export ANTHROPIC_BASE_URL="${origin.value}"\nexport ANTHROPIC_AUTH_TOKEN="${key}"\nexport CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1${quotedModelEnv('ANTHROPIC_MODEL')}\n\nclaude`,
      `set ANTHROPIC_BASE_URL=${origin.value}\nset ANTHROPIC_AUTH_TOKEN=${key}\nset CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1${quotedModelEnv('ANTHROPIC_MODEL')}\n\nclaude`,
      `$env:ANTHROPIC_BASE_URL="${origin.value}"\n$env:ANTHROPIC_AUTH_TOKEN="${key}"\n$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1${quotedModelEnv('ANTHROPIC_MODEL')}\n\nclaude`,
    )
    const settingsPath = activeShell.value === 'unix' ? '~/.claude/settings.json' : '%USERPROFILE%\\.claude\\settings.json'
    const env: Record<string, string> = {
      ANTHROPIC_BASE_URL: origin.value,
      ANTHROPIC_AUTH_TOKEN: key,
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    }
    if (model) env.ANTHROPIC_MODEL = model
    return [
      { path: terminalPath, content: terminal, hint: 'Base URL 末尾不要加 /v1；仅对当前终端生效。' },
      { path: settingsPath, content: JSON.stringify({ env }, null, 2), hint: 'Base URL 末尾不要加 /v1；与已有 settings.json 的 env 合并。' },
    ]
  }

  if (activeClient.value === 'codex') {
    const configDir = activeShell.value === 'windows' ? '%USERPROFILE%\\.codex' : '~/.codex'
    const files: SetupFile[] = [
      {
        path: `${configDir}/config.toml`,
        hint: '如果已有 config.toml，请仅合并 DengDeng AI provider 段。',
        content: codexConfigToml(model),
      },
      {
        path: `${configDir}/auth.json`,
        content: JSON.stringify({ OPENAI_API_KEY: key }, null, 2),
      },
    ]
    return files
  }

  if (activeClient.value === 'gemini') {
    return [{
      path: activeShell.value === 'cmd' ? 'Command Prompt' : activeShell.value === 'powershell' ? 'PowerShell' : 'Terminal',
      content: shellLine(
        `export GOOGLE_GEMINI_BASE_URL="${geminiBase.value}"\nexport GEMINI_API_KEY="${key}"${quotedModelEnv('GEMINI_MODEL')}\n\ngemini`,
        `set GOOGLE_GEMINI_BASE_URL=${geminiBase.value}\nset GEMINI_API_KEY=${key}${quotedModelEnv('GEMINI_MODEL')}\n\ngemini`,
        `$env:GOOGLE_GEMINI_BASE_URL="${geminiBase.value}"\n$env:GEMINI_API_KEY="${key}"${quotedModelEnv('GEMINI_MODEL')}\n\ngemini`,
      ),
    }]
  }

  if (activeClient.value === 'chatbox') {
    return [{
      path: 'Chatbox → 设置 → 模型提供方 → OpenAI API',
      content: `API Host: ${apiBase.value}\nAPI Key: ${key}\n模型: ${model}`,
      hint: 'API Host 保留 /v1，不要填写 /v1/models。',
    }]
  }

  if (activeClient.value === 'cline') {
    return [{
      path: 'VS Code → Cline → API Configuration',
      content: `API Provider: OpenAI Compatible\nBase URL: ${apiBase.value}\nAPI Key: ${key}\nModel ID: ${model}`,
      hint: '保存后可在 Cline 中发送一条短消息测试连接。',
    }]
  }

  if (activeClient.value === 'opencode') {
    const provider = activePlatform.value === 'anthropic' ? 'anthropic' : activePlatform.value === 'gemini' ? 'google' : 'openai'
    const baseURL = activePlatform.value === 'anthropic' ? apiBase.value : activePlatform.value === 'gemini' ? geminiBase.value : apiBase.value
    return [{
      path: 'opencode.json',
      hint: '将此 provider 合并到已有文件；模型 ID 使用当前分组的可用模型。',
      content: JSON.stringify({ provider: { [provider]: { options: { baseURL, apiKey: key } } } }, null, 2),
    }]
  }

	if (activeClient.value === 'cherry') {
		return [{
			path: 'Cherry Studio → 设置 → 模型服务 → 添加提供商',
			hint: '提供商请选择 OpenAI Compatible；API 地址保留 /v1。',
			content: `提供商: OpenAI Compatible\n名称: DengDeng AI\nAPI 地址: ${apiBase.value}\nAPI 密钥: ${key}\n模型 ID: ${model}`,
		}]
	}

	if (activeClient.value === 'nextchat') {
		return [{
			path: 'NextChat → 设置 → 自定义接口',
			hint: '接口地址使用完整 /v1 地址；模型名称与模型列表保持一致。',
			content: `接口地址: ${apiBase.value}\nAPI Key: ${key}\n自定义模型: ${model}`,
		}]
	}

	if (activeClient.value === 'continue') {
		return [{
			path: '~/.continue/config.yaml',
			hint: '将 models 中的这一项合并到现有配置；不要覆盖其他模型。',
			content: `name: DengDeng AI\nversion: 1.0.0\nschema: v1\nmodels:\n  - name: DengDeng AI · ${model || '选择模型'}\n    provider: openai\n    model: ${model}\n    apiBase: ${apiBase.value}\n    apiKey: ${key}`,
		}]
	}

  return []
})

const ccSwitchUsageScript = `({
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

const ccSwitchConfig = computed(() => {
  const app = activePlatform.value === 'anthropic' ? 'claude' : activePlatform.value === 'gemini' ? 'gemini' : 'codex'
  const endpoint = isOpenAICompatible.value ? apiBase.value : origin.value
  return {
    resource: 'provider',
    app,
    name: `DengDeng AI · ${props.keyName || 'API Key'}`,
    homepage: origin.value,
    endpoint,
    apiKey: configuredApiKey.value,
    model: selectedModel.value || undefined,
    configFormat: 'json',
    usageEnabled: true,
    // CCSwitch treats the provider endpoint and the usage-query base URL as
    // separate values. Codex needs /v1 for relay traffic, while the custom
    // script below appends /v1/usage itself.
    usageBaseUrl: origin.value,
    usageApiKey: configuredApiKey.value,
    usageScript: window.btoa(ccSwitchUsageScript),
    usageAutoInterval: 30,
    enabled: true,
  }
})

const ccSwitchLink = computed(() => {
  const params = new URLSearchParams()
  Object.entries(ccSwitchConfig.value).forEach(([key, value]) => {
    if (value !== undefined) params.set(key, String(value))
  })
  return `ccswitch://v1/import?${params.toString()}`
})

async function copy(value: string, id: string) {
  try {
    await copyText(value)
    copied.value = id
    toast.show('已复制到剪贴板', 'success')
    window.setTimeout(() => { if (copied.value === id) copied.value = '' }, 1600)
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

function openCCSwitch() {
  if (configuredApiKey.value) window.location.assign(ccSwitchLink.value)
}

</script>

<template>
  <Teleport to="body">
    <div v-if="show" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="emit('close')">
      <div class="card key-setup-modal">
        <div class="key-setup-head"><div><p>密钥快速配置</p><h3>{{ keyName }}</h3></div><button class="btn-ghost !px-2 !py-1 text-xs" @click="emit('close')">关闭</button></div>

        <div class="key-setup-summary">
          <div class="key-setup-secret"><span>API 密钥</span><input v-model="workingApiKey" class="key-setup-key-input" type="text" name="dengdeng-api-token" autocomplete="one-time-code" autocapitalize="none" inputmode="text" spellcheck="false" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-form-type="other" placeholder="粘贴已有密钥" /><div class="key-setup-secret-actions"><button class="btn-ghost" :disabled="!configuredApiKey" @click="copy(configuredApiKey, 'key')">{{ copied === 'key' ? '已复制' : '复制' }}</button><button v-if="configuredApiKey" class="btn-ghost is-danger" @click="forgetApiKey">清除本机</button></div></div>
          <div class="key-setup-secret"><span>接口地址</span><code>{{ activeEndpoint }}</code><button class="btn-ghost !px-2 !py-1 text-xs" @click="copy(activeEndpoint, 'endpoint')">{{ copied === 'endpoint' ? '已复制' : '复制' }}</button></div>
			<div v-if="availablePlatforms.length > 1" class="key-setup-secret"><span>接入平台</span><select v-model="activePlatform" class="input key-setup-effort" aria-label="接入平台"><option v-for="item in availablePlatforms" :key="item" :value="item">{{ PLATFORM_LABELS[item] || item }}</option></select><span class="text-[10px] text-slate-500">{{ availablePlatforms.length }} 个</span></div>
			<div v-if="availablePlatforms.includes('openai')" class="key-setup-secret"><span>思考强度 Effort</span><select class="input key-setup-effort" aria-label="思考强度 Effort" :value="reasoningEffort" :disabled="savingEffort || !keyId" @change="changeReasoningEffort"><option v-for="option in REASONING_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option></select><span v-if="savingEffort" class="text-[10px] text-slate-500">保存中…</span></div>
        </div>
		<p v-if="invalidApiKeyInput" class="key-setup-status is-error">输入内容不是有效的 dd- 密钥；浏览器可能自动填入了登录密码。</p>

        <div v-if="configuredApiKey" class="key-setup-model-row">
          <label><span>模型</span><select v-model="selectedModel" class="input" :disabled="modelsState === 'loading' || !models.length"><option v-if="!models.length" value="">{{ modelsState === 'loading' ? '正在读取模型…' : '暂无模型' }}</option><option v-for="model in models" :key="model" :value="model">{{ model }}</option></select></label>
          <button class="btn-ghost !px-3 !py-2 text-xs" :disabled="modelsState === 'loading'" @click="loadModels">{{ modelsState === 'loading' ? '检测中…' : '检测密钥并刷新模型' }}</button>
        </div>
        <template v-if="configuredApiKey">
          <p v-if="modelsState === 'ready'" class="key-setup-status is-ok">密钥验证成功，{{ PLATFORM_LABELS[activePlatform] || activePlatform }} 分组可用 {{ models.length }} 个模型。</p>
          <p v-else-if="modelsState === 'error'" class="key-setup-status is-error">{{ modelsError }}</p>

          <section class="key-setup-downloads" aria-labelledby="key-setup-downloads-title">
            <header>
              <div>
                <strong id="key-setup-downloads-title">工具下载</strong>
                <small>下载入口始终完整显示；下方配置模板会按当前分组协议筛选。</small>
              </div>
              <span>{{ downloadClients.length }} 个</span>
            </header>
            <div class="key-setup-download-list">
              <a v-for="client in downloadClients" :key="client" :href="clientDownloads[client].url" target="_blank" rel="noopener noreferrer">
                <strong>{{ clientDownloads[client].label }}</strong>
                <small>{{ clientDownloads[client].action }}</small>
                <span aria-hidden="true">↗</span>
              </a>
            </div>
          </section>

          <div class="key-setup-config-head">
            <div><strong>快速配置</strong><small>仅显示当前 {{ PLATFORM_LABELS[activePlatform] || activePlatform }} 分组可以直接使用的工具。</small></div>
            <span>{{ clientOptions.length }} 个</span>
          </div>
          <div class="key-setup-tabs"><button v-for="item in clientOptions" :key="item.id" :class="{ 'is-active': activeClient === item.id }" @click="activeClient = item.id">{{ item.label }}</button></div>
          <div v-if="shellOptions.length" class="key-setup-subtabs"><button v-for="item in shellOptions" :key="item.id" :class="{ 'is-active': activeShell === item.id }" @click="activeShell = item.id">{{ item.label }}</button></div>

          <p class="key-setup-hint">{{ activeDescription }}</p>
          <template v-if="activeClient !== 'ccswitch'">
            <div v-for="(file, index) in currentFiles" :key="file.path" class="key-setup-code"><div><span>{{ file.path }}</span><button @click="copy(file.content, `${activeClient}-${index}`)">{{ copied === `${activeClient}-${index}` ? '已复制' : '复制配置' }}</button></div><p v-if="file.hint">{{ file.hint }}</p><pre>{{ file.content }}</pre></div>
          </template>
          <template v-else>
            <div class="key-setup-ccswitch"><strong>导入到 CCSwitch</strong><p>将导入 {{ ccSwitchConfig.app }} 配置，模型为 {{ selectedModelLabel }}。CCSwitch 会每 30 分钟查询一次密钥余额、总额度与已用额度；查询不计入 API 调用，也不消耗上游额度。</p><div class="key-setup-ccswitch-actions"><button class="btn-primary" @click="openCCSwitch">打开 CCSwitch 导入</button><button class="btn-ghost text-xs" @click="copy(ccSwitchLink, 'ccswitch-link')">{{ copied === 'ccswitch-link' ? '导入链接已复制' : '复制导入链接' }}</button></div></div>
            <div class="key-setup-code"><div><span>导入配置预览</span><button @click="copy(JSON.stringify(ccSwitchConfig, null, 2), 'ccswitch-config')">{{ copied === 'ccswitch-config' ? '已复制' : '复制 JSON' }}</button></div><pre>{{ JSON.stringify(ccSwitchConfig, null, 2) }}</pre></div>
          </template>
        </template>
		<div v-else class="key-setup-empty"><strong>粘贴已有密钥即可继续</strong><p>服务端只保存单向哈希。输入后会保存在当前浏览器本机，刷新、关闭标签页或重启浏览器后仍可显示和复制；可随时点击“清除本机”。</p><button class="btn-danger !px-3 !py-2 text-xs" @click="emit('rotate')">找不到原密钥，重新生成</button></div>
        <p class="key-setup-warning">配置内容含密钥。请不要截图、转发或提交到代码仓库。</p>
      </div>
    </div>
  </Teleport>
</template>
