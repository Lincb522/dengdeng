<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, copyText } from '../api/client'
import { localizedApiError, localizeErrorMessage } from '../api/errors'
import { normalizeReasoningEffort, OFFICIAL_REASONING_EFFORTS, REASONING_OPTIONS } from '../api/reasoning'
import { useToast } from '../stores/toast'

type ClientID = 'claude' | 'codex' | 'gemini' | 'chatbox' | 'cline' | 'opencode' | 'ccswitch'
type ShellID = 'unix' | 'cmd' | 'powershell' | 'windows'

interface SetupFile {
  path: string
  content: string
  hint?: string
}

const props = defineProps<{ show: boolean; apiKey: string; keyId: number | null; keyName: string; platform: string; reasoningEffort: string }>()
const emit = defineEmits<{ close: []; rotate: []; 'effort-updated': [value: string] }>()
const toast = useToast()

const activeClient = ref<ClientID>('codex')
const activeShell = ref<ShellID>('unix')
const copied = ref('')
const models = ref<string[]>([])
const selectedModel = ref('')
const modelsState = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
const modelsError = ref('')
const workingApiKey = ref('')
const reasoningEffort = ref('auto')
const savingEffort = ref(false)

const origin = computed(() => window.location.origin.replace(/\/$/, ''))
const apiBase = computed(() => `${origin.value}/v1`)
const geminiBase = computed(() => `${origin.value}/v1beta`)
const configuredApiKey = computed(() => workingApiKey.value.trim())
const activeEndpoint = computed(() => {
  if (activeClient.value === 'claude') return origin.value
  if (activeClient.value === 'gemini') return geminiBase.value
  if (activeClient.value === 'ccswitch') return props.platform === 'openai' ? apiBase.value : origin.value
  return apiBase.value
})

async function changeReasoningEffort(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  const previous = reasoningEffort.value
  if (!props.keyId || value === previous) {
    reasoningEffort.value = value
    return
  }
  reasoningEffort.value = value
  savingEffort.value = true
  try {
    await api.put(`/api/user/keys/${props.keyId}`, { reasoning_effort: value })
    toast.show('思考强度已更新', 'success')
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

function readRememberedApiKey() {
  const storageKey = quickSetupStorageKey()
  if (!storageKey) return ''
  try {
    return sessionStorage.getItem(storageKey) || ''
  } catch {
    return ''
  }
}

function rememberApiKey(value: string) {
  const storageKey = quickSetupStorageKey()
  if (!storageKey) return
  try {
    if (value.trim()) sessionStorage.setItem(storageKey, value.trim())
    else sessionStorage.removeItem(storageKey)
  } catch {
    // A restricted browser session can still use the pasted key in memory.
  }
}

const clientOptions = computed(() => {
  if (props.platform === 'anthropic') {
    return [
      { id: 'claude' as const, label: 'Claude Code' },
      { id: 'codex' as const, label: 'Codex CLI' },
      { id: 'opencode' as const, label: 'OpenCode' },
      { id: 'ccswitch' as const, label: 'CCSwitch' },
    ]
  }
  if (props.platform === 'gemini') {
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
    { id: 'cline' as const, label: 'Cline' },
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
    claude: props.platform === 'openai'
      ? 'Claude Code 会通过兼容层使用当前 OpenAI / Codex 分组。'
      : '复制终端环境变量；也提供 Claude Code 的持久化 settings.json 文件。',
    codex: props.platform === 'anthropic'
      ? 'Codex CLI 会通过兼容层使用当前 Claude 分组。'
      : 'Codex CLI 需要 config.toml 和 auth.json 两个文件，分别复制到 ~/.codex 目录。',
    gemini: '使用环境变量启动 Gemini CLI；模型列表来自当前密钥所属分组。',
    chatbox: '在 Chatbox 中新建「OpenAI API」提供方，依次填入下面三项。',
    cline: '在 VS Code 的 Cline 设置中选择 OpenAI Compatible，再填入下面三项。',
    opencode: '把 provider 段合并进现有的 opencode.json；不要覆盖已有配置。',
    ccswitch: '通过系统 deeplink 打开 CCSwitch。导入前可先检查下方的配置预览。',
  }
  return descriptions[activeClient.value]
})

const selectedModelLabel = computed(() => selectedModel.value || '暂未读取到模型')

watch([() => props.show, () => props.platform, () => props.apiKey, () => props.keyId], ([show]) => {
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

async function loadModels() {
  const apiKey = configuredApiKey.value
  if (!apiKey) {
    models.value = []
    selectedModel.value = ''
    modelsState.value = 'idle'
    modelsError.value = ''
    return
  }
  modelsState.value = 'loading'
  modelsError.value = ''
  try {
    const response = await fetch(`${apiBase.value}/models`, {
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
    const provider = props.platform === 'anthropic' ? 'anthropic' : props.platform === 'gemini' ? 'google' : 'openai'
    const baseURL = props.platform === 'anthropic' ? apiBase.value : props.platform === 'gemini' ? geminiBase.value : apiBase.value
    return [{
      path: 'opencode.json',
      hint: '将此 provider 合并到已有文件；模型 ID 使用当前分组的可用模型。',
      content: JSON.stringify({ provider: { [provider]: { options: { baseURL, apiKey: key } } } }, null, 2),
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
  const app = props.platform === 'anthropic' ? 'claude' : props.platform === 'gemini' ? 'gemini' : 'codex'
  const endpoint = props.platform === 'openai' ? apiBase.value : origin.value
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
          <div class="key-setup-secret"><span>API 密钥</span><input v-model="workingApiKey" class="key-setup-key-input" type="password" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="粘贴已有密钥" /><button class="btn-ghost !px-2 !py-1 text-xs" :disabled="!configuredApiKey" @click="copy(configuredApiKey, 'key')">{{ copied === 'key' ? '已复制' : '复制' }}</button></div>
          <div class="key-setup-secret"><span>接口地址</span><code>{{ activeEndpoint }}</code><button class="btn-ghost !px-2 !py-1 text-xs" @click="copy(activeEndpoint, 'endpoint')">{{ copied === 'endpoint' ? '已复制' : '复制' }}</button></div>
			<div v-if="platform === 'openai'" class="key-setup-secret"><span>思考强度 Effort</span><select class="input key-setup-effort" :value="reasoningEffort" :disabled="savingEffort || !keyId" @change="changeReasoningEffort"><option v-for="option in REASONING_OPTIONS" :key="option.value" :value="option.value">{{ option.label }}</option></select><span v-if="savingEffort" class="text-[10px] text-slate-500">保存中…</span></div>
        </div>

        <div v-if="configuredApiKey" class="key-setup-model-row">
          <label><span>模型</span><select v-model="selectedModel" class="input" :disabled="modelsState === 'loading' || !models.length"><option v-if="!models.length" value="">{{ modelsState === 'loading' ? '正在读取模型…' : '暂无模型' }}</option><option v-for="model in models" :key="model" :value="model">{{ model }}</option></select></label>
          <button class="btn-ghost !px-3 !py-2 text-xs" :disabled="modelsState === 'loading'" @click="loadModels">{{ modelsState === 'loading' ? '检测中…' : '检测密钥并刷新模型' }}</button>
        </div>
        <template v-if="configuredApiKey">
          <p v-if="modelsState === 'ready'" class="key-setup-status is-ok">密钥验证成功，当前分组可用 {{ models.length }} 个模型。</p>
          <p v-else-if="modelsState === 'error'" class="key-setup-status is-error">{{ modelsError }}</p>

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
        <div v-else class="key-setup-empty"><strong>粘贴已有密钥即可继续</strong><p>服务端不会保存密钥明文。输入后仅暂存在当前浏览器标签页，刷新页面仍可继续配置；关闭标签页后会自动清除。</p><button class="btn-danger !px-3 !py-2 text-xs" @click="emit('rotate')">找不到原密钥，重新生成</button></div>
        <p class="key-setup-warning">配置内容含密钥。请不要截图、转发或提交到代码仓库。</p>
      </div>
    </div>
  </Teleport>
</template>
