<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, copyText } from '../api/client'
import { localizedApiError, localizeErrorMessage } from '../api/errors'
import { buildCCSwitchImportConfig, buildCCSwitchImportLink } from '../api/ccswitch'
import { normalizeReasoningEffort, OFFICIAL_REASONING_EFFORTS, REASONING_OPTIONS } from '../api/reasoning'
import { PLATFORM_LABELS } from '../api/types'
import { useToast } from '../stores/toast'
import AppModal from './AppModal.vue'
import ProviderLogo from './ProviderLogo.vue'
import ProviderSelect from './ProviderSelect.vue'

type ClientID = 'claude' | 'codex' | 'gemini' | 'chatbox' | 'cline' | 'opencode' | 'ccswitch' | 'cherry' | 'nextchat' | 'continue'
type ShellID = 'unix' | 'cmd' | 'powershell' | 'windows'

interface SetupFile {
  path: string
  content: string
  hint?: string
}

const props = defineProps<{ show: boolean; apiKey: string; keyId: number | null; keyName: string; keyPreview: string; platform: string; platforms: string[]; reasoningEffort: string; secretAvailable: boolean; loadingSecret: boolean; allowCcsImport: boolean }>()
const emit = defineEmits<{ close: []; rotate: []; 'secret-saved': [value: string]; 'effort-updated': [value: string] }>()
const toast = useToast()

const activeClient = ref<ClientID>('codex')
const activeShell = ref<ShellID>('unix')
const activeStep = ref<1 | 2 | 3>(1)
const activeFileIndex = ref(0)
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
const savingSecret = ref(false)
const secretStored = ref(props.secretAvailable)

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
const allDownloadClients: ClientID[] = [
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
const downloadClients = computed(() => allDownloadClients.filter((client) => props.allowCcsImport || client !== 'ccswitch'))

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

async function saveRecoveredSecret(showMessage = true) {
	if (!props.keyId || !configuredApiKey.value || secretStored.value || savingSecret.value) return !!secretStored.value
	savingSecret.value = true
	try {
		await api.put(`/api/user/keys/${props.keyId}/secret`, { plain: configuredApiKey.value })
		secretStored.value = true
		emit('secret-saved', configuredApiKey.value)
		if (showMessage) toast.show('密钥已保存到账号', 'success')
		return true
	} catch (error) {
		if (showMessage) toast.show(error instanceof Error ? localizeErrorMessage(error.message) : '保存密钥失败', 'error')
		return false
	} finally {
		savingSecret.value = false
	}
}

const clientOptions = computed(() => {
  if (activePlatform.value === 'anthropic') {
    const items: Array<{ id: ClientID; label: string }> = [
      { id: 'claude' as const, label: 'Claude Code' },
      { id: 'codex' as const, label: 'Codex CLI' },
      { id: 'opencode' as const, label: 'OpenCode' },
      { id: 'ccswitch' as const, label: 'CCSwitch' },
    ]
    return items.filter((item) => props.allowCcsImport || item.id !== 'ccswitch')
  }
  if (activePlatform.value === 'gemini') {
    const items: Array<{ id: ClientID; label: string }> = [
      { id: 'gemini' as const, label: 'Gemini CLI' },
      { id: 'opencode' as const, label: 'OpenCode' },
      { id: 'ccswitch' as const, label: 'CCSwitch' },
    ]
    return items.filter((item) => props.allowCcsImport || item.id !== 'ccswitch')
  }
  const items: Array<{ id: ClientID; label: string }> = [
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
  return items.filter((item) => props.allowCcsImport || item.id !== 'ccswitch')
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
    gemini: '使用环境变量启动 Gemini CLI；API Key 渠道从实际上游读取模型。',
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

watch([() => props.show, () => props.platform, () => props.platforms.join(','), () => props.apiKey, () => props.keyId, () => props.secretAvailable], ([show]) => {
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
  activeStep.value = 1
	workingApiKey.value = props.apiKey.trim()
	secretStored.value = props.secretAvailable
	if (configuredApiKey.value) void loadModels()
}, { immediate: true })

watch(() => props.reasoningEffort, (value) => {
  reasoningEffort.value = normalizeReasoningEffort(value)
})

watch(activeClient, () => {
  activeShell.value = 'unix'
  activeFileIndex.value = 0
})

watch(activeShell, () => {
  activeFileIndex.value = 0
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
	if (!secretStored.value && !(await saveRecoveredSecret(false))) {
		modelsState.value = 'error'
		modelsError.value = '请先保存有效的 dd- 密钥'
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

const ccSwitchOptions = computed(() => ({
  origin: origin.value,
  apiKey: configuredApiKey.value,
  platform: activePlatform.value,
  keyName: props.keyName,
  model: selectedModel.value || undefined,
}))
const ccSwitchConfig = computed(() => buildCCSwitchImportConfig(ccSwitchOptions.value))
const ccSwitchLink = computed(() => buildCCSwitchImportLink(ccSwitchOptions.value))

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

function setActiveStep(step: 1 | 2 | 3) {
  if (step > 1 && !configuredApiKey.value) return
  activeStep.value = step
}

function nextStep() {
  if (!configuredApiKey.value) return
  if (activeStep.value === 1) activeStep.value = 2
  else if (activeStep.value === 2) activeStep.value = 3
}

</script>

<template>
  <AppModal
    :open="show"
    title="密钥快速配置"
    :description="keyName"
    width="setup"
    :busy="savingEffort || savingSecret || loadingSecret"
    @close="emit('close')"
  >
    <div class="key-setup-flow">
      <nav class="key-setup-progress" aria-label="配置步骤">
        <button type="button" :class="{ 'is-active': activeStep === 1, 'is-complete': activeStep > 1 }" @click="setActiveStep(1)"><span>1</span><strong>密钥</strong></button>
        <button type="button" :disabled="!configuredApiKey" :class="{ 'is-active': activeStep === 2, 'is-complete': activeStep > 2 }" @click="setActiveStep(2)"><span>2</span><strong>模型</strong></button>
        <button type="button" :disabled="!configuredApiKey" :class="{ 'is-active': activeStep === 3 }" @click="setActiveStep(3)"><span>3</span><strong>客户端</strong></button>
      </nav>

      <section v-if="activeStep === 1" class="key-setup-step">
        <header><span>1</span><div><strong>确认密钥与平台</strong><small>核对接入信息后再读取模型。</small></div></header>
        <div class="key-setup-summary">
          <div class="key-setup-secret key-setup-secret--full"><span>API 密钥</span><input v-model="workingApiKey" class="key-setup-key-input" type="text" name="dengdeng-api-token" autocomplete="one-time-code" autocapitalize="none" inputmode="text" spellcheck="false" data-1p-ignore="true" data-lpignore="true" data-bwignore="true" data-form-type="other" :disabled="loadingSecret" :placeholder="loadingSecret ? '正在读取密钥…' : '粘贴已有密钥'" /><div class="key-setup-secret-actions"><button class="btn-ghost" :disabled="!configuredApiKey" @click="copy(configuredApiKey, 'key')">{{ copied === 'key' ? '已复制' : '复制' }}</button><button v-if="configuredApiKey && !secretStored" class="btn-primary" :disabled="savingSecret" @click="saveRecoveredSecret()">{{ savingSecret ? '保存中…' : '保存到账号' }}</button><small v-else-if="secretStored">账号已保存</small></div></div>
          <div class="key-setup-secret"><span>接口地址</span><code :title="activeEndpoint">{{ activeEndpoint }}</code><button class="btn-ghost" @click="copy(activeEndpoint, 'endpoint')">{{ copied === 'endpoint' ? '已复制' : '复制' }}</button></div>
		  <div v-if="availablePlatforms.length > 1" class="key-setup-secret"><span>接入平台</span><ProviderSelect v-model="activePlatform" :platforms="availablePlatforms" class="key-setup-effort" aria-label="接入平台" /><small>{{ availablePlatforms.length }} 个</small></div>
		  <div v-else class="key-setup-secret"><span>接入平台</span><strong class="provider-inline-label"><ProviderLogo :platform="activePlatform" size="sm" />{{ PLATFORM_LABELS[activePlatform] || activePlatform }}</strong><small>当前</small></div>
		  <div v-if="availablePlatforms.includes('openai')" class="key-setup-secret"><span>思考强度</span><select class="input key-setup-effort" aria-label="思考强度" :value="reasoningEffort" :disabled="savingEffort || !keyId" @change="changeReasoningEffort"><option v-for="option in REASONING_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option></select><small>{{ savingEffort ? '保存中…' : 'Effort' }}</small></div>
        </div>
		<p v-if="invalidApiKeyInput" class="key-setup-status is-error" role="alert">输入内容不是有效的 dd- 密钥；浏览器可能自动填入了登录密码。</p>
      </section>

      <section v-else-if="activeStep === 2 && configuredApiKey" class="key-setup-step">
        <header><span>2</span><div><strong>验证并选择模型</strong><small>API Key 渠道从实际上游读取模型。</small></div></header>
        <div class="key-setup-model-row">
          <label><span>模型</span><select v-model="selectedModel" class="input" :disabled="modelsState === 'loading' || !models.length"><option v-if="!models.length" value="">{{ modelsState === 'loading' ? '正在读取模型…' : '暂无模型' }}</option><option v-for="model in models" :key="model" :value="model">{{ model }}</option></select></label>
          <button class="btn-ghost" :disabled="modelsState === 'loading'" @click="loadModels">{{ modelsState === 'loading' ? '验证中…' : '验证并刷新' }}</button>
        </div>
        <p v-if="modelsState === 'ready' && models.length" class="key-setup-status is-ok provider-inline-label" role="status"><ProviderLogo :platform="activePlatform" size="sm" />验证成功，{{ PLATFORM_LABELS[activePlatform] || activePlatform }} 可用 {{ models.length }} 个模型。</p>
        <p v-else-if="modelsState === 'ready'" class="key-setup-status is-empty" role="status">密钥有效，但当前分组没有返回模型，请检查分组和模型配置。</p>
        <p v-else-if="modelsState === 'error'" class="key-setup-status is-error" role="alert">{{ modelsError }}</p>
        <p v-else-if="modelsState === 'idle'" class="key-setup-status">点击“验证并刷新”读取模型。</p>
      </section>

      <section v-else-if="activeStep === 3 && configuredApiKey" class="key-setup-step">
        <header><span>3</span><div><strong>选择客户端并复制配置</strong><small>配置会随平台、模型和系统选项实时更新。</small></div></header>
        <div class="key-setup-tabs" role="tablist" aria-label="客户端"><button v-for="item in clientOptions" :key="item.id" :class="{ 'is-active': activeClient === item.id }" role="tab" :aria-selected="activeClient === item.id" @click="activeClient = item.id">{{ item.label }}</button></div>
        <div v-if="shellOptions.length" class="key-setup-subtabs"><button v-for="item in shellOptions" :key="item.id" :class="{ 'is-active': activeShell === item.id }" @click="activeShell = item.id">{{ item.label }}</button></div>
        <p class="key-setup-hint">{{ activeDescription }}</p>
        <template v-if="activeClient !== 'ccswitch'">
          <div v-if="currentFiles.length > 1" class="key-setup-file-tabs" role="tablist" aria-label="配置文件"><button v-for="(file, index) in currentFiles" :key="file.path" type="button" role="tab" :aria-selected="activeFileIndex === index" :class="{ 'is-active': activeFileIndex === index }" :title="file.path" @click="activeFileIndex = index">{{ file.path }}</button></div>
          <div v-if="currentFiles[activeFileIndex]" class="key-setup-code"><div><span :title="currentFiles[activeFileIndex].path">{{ currentFiles[activeFileIndex].path }}</span><button @click="copy(currentFiles[activeFileIndex].content, `${activeClient}-${activeFileIndex}`)">{{ copied === `${activeClient}-${activeFileIndex}` ? '已复制' : '复制配置' }}</button></div><p v-if="currentFiles[activeFileIndex].hint">{{ currentFiles[activeFileIndex].hint }}</p><pre>{{ currentFiles[activeFileIndex].content }}</pre></div>
        </template>
        <template v-else>
          <div class="key-setup-ccswitch"><strong>导入到 CCSwitch</strong><p>将导入 {{ ccSwitchConfig.app }} 配置，模型为 {{ selectedModelLabel }}；用量查询不消耗上游额度。</p><div class="key-setup-ccswitch-actions"><button class="btn-primary" @click="openCCSwitch">一键导入 CCS</button><button class="btn-ghost" @click="copy(ccSwitchLink, 'ccswitch-link')">{{ copied === 'ccswitch-link' ? '已复制' : '复制导入链接' }}</button></div></div>
          <div class="key-setup-code"><div><span>导入配置预览</span><button @click="copy(JSON.stringify(ccSwitchConfig, null, 2), 'ccswitch-config')">{{ copied === 'ccswitch-config' ? '已复制' : '复制 JSON' }}</button></div><pre>{{ JSON.stringify(ccSwitchConfig, null, 2) }}</pre></div>
        </template>
      </section>

      <div v-if="activeStep === 1 && !configuredApiKey" class="key-setup-empty"><strong>粘贴已有密钥即可继续</strong><p>密钥会加密保存到账号，换设备登录后仍可查看和复制。</p><button class="btn-danger" @click="emit('rotate')">找不到原密钥，重新生成</button></div>

      <details v-if="activeStep === 3 && configuredApiKey" class="modal-disclosure key-setup-downloads">
        <summary><span><strong>客户端下载</strong><small>Claude、Codex、Gemini、Chatbox 等 {{ downloadClients.length }} 个工具</small></span></summary>
        <div class="modal-disclosure__body"><div class="key-setup-download-list"><a v-for="client in downloadClients" :key="client" :href="clientDownloads[client].url" target="_blank" rel="noopener noreferrer"><strong>{{ clientDownloads[client].label }}</strong><small>{{ clientDownloads[client].action }}</small><span aria-hidden="true">↗</span></a></div></div>
      </details>
    </div>
    <template #footer>
      <p class="key-setup-footer-note">配置含密钥，请勿转发或提交到仓库。</p>
      <button v-if="activeStep > 1" type="button" class="btn-ghost" @click="setActiveStep(activeStep === 3 ? 2 : 1)">上一步</button>
      <button v-if="activeStep < 3" type="button" class="btn-primary" :disabled="!configuredApiKey" @click="nextStep">下一步</button>
      <button v-else type="button" class="btn-primary" @click="emit('close')">完成</button>
    </template>
  </AppModal>
</template>
