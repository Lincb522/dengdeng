<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { AuditLog, GatewayRuntimePolicy, Group, LegalDocument, SystemSettings } from '../../api/types'
import { defaultReasoningMultipliers, REASONING_OPTIONS } from '../../api/reasoning'

type SettingsPayload = SystemSettings & {
  site_public_url?: string
  smtp_configured?: boolean
  smtp_from_name?: string
  smtp_from?: string
}

type ImageStorageSettings = {
	enabled: boolean; endpoint: string; region: string; bucket: string; access_key_id: string; secret_access_key: string
	access_key_configured: boolean; secret_configured: boolean; prefix: string; force_path_style: boolean
	public_base_url: string; presign_expiry_hours: number; max_download_bytes: number; active: boolean
}

const sections = [
  { id: 'general', label: '站点信息', hint: '名称、说明与运行状态' },
  { id: 'agreement', label: '协议与免责', hint: '登录页确认与文档内容' },
	{ id: 'features', label: '功能开关', hint: '监控、模型与用户功能' },
	{ id: 'security', label: '安全与认证', hint: '验证、会话与访问控制' },
	{ id: 'users', label: '用户默认值', hint: '注册、额度与并发策略' },
  { id: 'gateway', label: '网关与调度', hint: '重试、冷却与故障隔离' },
	{ id: 'email', label: '邮件设置', hint: 'SMTP、模板与测试发送' },
	{ id: 'image', label: '图像存储', hint: '异步任务与对象存储' },
	{ id: 'services', label: '业务设置', hint: '支付、备份、推广与告警' },
  { id: 'operations', label: '运行与审计', hint: '健康探测与操作记录' },
] as const

const activeSection = ref<(typeof sections)[number]['id']>('general')
const loading = ref(true)
const saving = ref(false)
const auditLoading = ref(false)
const runtime = ref<Pick<SettingsPayload, 'site_public_url' | 'smtp_configured' | 'smtp_from_name' | 'smtp_from'>>({})
const registrationSuffixesText = ref('')
const trustedProxiesText = ref('')
const forwardedHeadersText = ref('')
const pageSizeOptionsText = ref('10, 20, 50, 100')
const notificationEmailsText = ref('')
const riskPhrasesText = ref('')
const codexBlacklistText = ref('')
const codexWhitelistText = ref('')
const emailTestRecipient = ref('')
const emailTesting = ref(false)
const groups = ref<Group[]>([])
const emptySecretValues = () => ({
	smtp_password: '', turnstile_secret: '', oauth_linuxdo_secret: '', oauth_dingtalk_secret: '', oauth_wechat_secret: '',
	oauth_oidc_secret: '', oauth_github_secret: '', oauth_google_secret: '',
})
const secretValues = ref<Record<string, string>>(emptySecretValues())
const secretConfigured = ref<Record<string, boolean>>({})
const platforms = [
	{ id: 'openai', label: 'OpenAI' },
	{ id: 'anthropic', label: 'Anthropic' },
	{ id: 'gemini', label: 'Gemini' },
	{ id: 'grok', label: 'Grok' },
] as const
const oauthProviderEntries = [
	{ key: 'linuxdo', label: 'LinuxDO', secret: 'oauth_linuxdo_secret' },
	{ key: 'dingtalk', label: '钉钉', secret: 'oauth_dingtalk_secret' },
	{ key: 'wechat', label: '微信', secret: 'oauth_wechat_secret' },
	{ key: 'oidc', label: 'OIDC', secret: 'oauth_oidc_secret' },
	{ key: 'github', label: 'GitHub', secret: 'oauth_github_secret' },
	{ key: 'google', label: 'Google', secret: 'oauth_google_secret' },
] as const
type OAuthProviderKey = (typeof oauthProviderEntries)[number]['key']
type AuthSourceKey = OAuthProviderKey | 'email'
const oauthProviderDefault = (providerName: string, frontendRedirectURL: string) => ({
	enabled: false,
	provider_name: providerName,
	client_id: '',
	issuer_url: '',
	discovery_url: '',
	authorize_url: '',
	token_url: '',
	userinfo_url: '',
	jwks_url: '',
	scopes: '',
	redirect_url: '',
	frontend_redirect_url: frontendRedirectURL,
	token_auth_method: 'client_secret_post',
	use_pkce: false,
	validate_id_token: false,
	require_verified_email: false,
	allowed_signing_algs: '',
	clock_skew_seconds: 60,
	email_path: 'email',
	id_path: 'sub',
	username_path: 'name',
})

function defaultSystemSettings(): SystemSettings {
	return {
		site_name: 'DengDeng AI · 蹬蹬ai',
		site_subtitle: '统一管理模型接入与用量',
		allow_register: true,
		key_multi_group_enabled: true,
		registration_email_suffixes: [],
		trusted_proxies: [],
		forwarded_client_ip_headers: ['X-Forwarded-For', 'X-Real-IP'],
		init_balance_micro: 0,
		login_agreement: { enabled: true, mode: 'modal', updated_at: '', documents: [] },
		site_customization: {
			logo_url: '', contact_info: '', docs_url: '', home_content: '', backend_mode_enabled: false, hide_ccs_import_button: false,
			table_default_page_size: 20, table_page_size_options: [10, 20, 50, 100], custom_menu_items: [], custom_endpoints: [],
		},
			features: {
				channel_monitor_enabled: true, channel_monitor_interval_seconds: 300, model_plaza_enabled: true,
				risk_control_enabled: false, risk_control_action: 'block', risk_control_blocked_phrases: [], referral_enabled: true, allow_user_view_error_requests: true,
		},
		security: {
			email_verification_enabled: true, password_reset_enabled: true, totp_enabled: true, session_binding_enabled: false,
			step_up_enabled: false, audit_log_retention_days: 180, turnstile_enabled: false, turnstile_site_key: '',
			trust_forwarded_ip: true, forwarded_ip_headers: ['X-Forwarded-For', 'X-Real-IP'],
		},
		user_defaults: { balance_micro: 0, concurrency: 0, rpm_limit: 0, default_subscriptions: [], platform_quotas: {}, auth_source_defaults: {} },
		notifications: {
			balance_low_enabled: false, balance_low_threshold_micro: 0, balance_low_recharge_url: '',
			subscription_expiry_enabled: false, account_quota_enabled: false, account_quota_emails: [],
		},
		email: { host: '', port: 465, username: '', from_name: 'DengDeng AI', from: '', use_tls: true },
		auth_providers: {
			linuxdo: { ...oauthProviderDefault('LinuxDO', '/auth/linuxdo/callback'), authorize_url: 'https://connect.linux.do/oauth2/authorize', token_url: 'https://connect.linux.do/oauth2/token', userinfo_url: 'https://connect.linux.do/api/user', scopes: 'user:read', id_path: 'id', username_path: 'username' },
			dingtalk: { ...oauthProviderDefault('钉钉', '/auth/dingtalk/callback'), authorize_url: 'https://login.dingtalk.com/oauth2/auth', token_url: 'https://api.dingtalk.com/v1.0/oauth2/userAccessToken', userinfo_url: 'https://api.dingtalk.com/v1.0/contact/users/me', scopes: 'openid', id_path: 'unionId', username_path: 'nick' },
			wechat: { ...oauthProviderDefault('微信', '/auth/wechat/callback'), authorize_url: 'https://open.weixin.qq.com/connect/qrconnect', token_url: 'https://api.weixin.qq.com/sns/oauth2/access_token', userinfo_url: 'https://api.weixin.qq.com/sns/userinfo', scopes: 'snsapi_login', id_path: 'unionid', username_path: 'nickname' },
			oidc: { ...oauthProviderDefault('OIDC', '/auth/oidc/callback'), scopes: 'openid email profile', use_pkce: true, validate_id_token: true, require_verified_email: true, allowed_signing_algs: 'RS256,ES256,PS256' },
			github: { ...oauthProviderDefault('GitHub', '/auth/github/callback'), authorize_url: 'https://github.com/login/oauth/authorize', token_url: 'https://github.com/login/oauth/access_token', userinfo_url: 'https://api.github.com/user', scopes: 'read:user user:email', id_path: 'id', username_path: 'login' },
				google: { ...oauthProviderDefault('Google', '/auth/google/callback'), issuer_url: 'https://accounts.google.com', discovery_url: 'https://accounts.google.com/.well-known/openid-configuration', authorize_url: 'https://accounts.google.com/o/oauth2/v2/auth', token_url: 'https://oauth2.googleapis.com/token', userinfo_url: 'https://openidconnect.googleapis.com/v1/userinfo', jwks_url: 'https://www.googleapis.com/oauth2/v3/certs', scopes: 'openid email profile', use_pkce: true, validate_id_token: true, require_verified_email: true, allowed_signing_algs: 'RS256', clock_skew_seconds: 60 },
		},
	}
}

const form = ref<SystemSettings>(defaultSystemSettings())
const reasoningEffortFields = REASONING_OPTIONS.filter((item) => item.value !== 'auto')
const runtimePolicy = ref<GatewayRuntimePolicy>({
  max_attempts: 3,
  unauthorized_cooldown_seconds: 600,
	  rate_limit_cooldown_seconds: 60,
	  overload_cooldown_seconds: 300,
  upstream_failure_cooldown_seconds: 30,
  network_failure_cooldown_seconds: 15,
  probe_interval_seconds: 300,
  probe_timeout_seconds: 12,
  probe_retention_days: 30,
  probe_concurrency: 4,
	concurrency_wait_milliseconds: 5000,
	concurrency_queue_depth: 256,
	reasoning_effort_multipliers: defaultReasoningMultipliers(),
		fingerprint_unification: true,
		metadata_passthrough: false,
		claude_oauth_system_prompt_injection: true,
		claude_oauth_system_prompt: "You are Claude Code, Anthropic's official CLI for Claude.",
		anthropic_cache_ttl_1h_injection: false,
		rewrite_message_cache_control: false,
		openai_codex_user_agent: 'codex_cli_rs/0.114.0 (Mac OS 15.0.0; arm64) iTerm.app/3.5.14',
		claude_client_gate_enabled: false,
		min_claude_code_version: '', max_claude_code_version: '',
		codex_client_gate_enabled: false,
		min_codex_version: '', max_codex_version: '',
		codex_client_blacklist: [], codex_client_whitelist: [], codex_allow_app_server_clients: true,
})
const auditItems = ref<AuditLog[]>([])
const imageStorage = ref<ImageStorageSettings>({
	enabled: false, endpoint: '', region: 'auto', bucket: '', access_key_id: '', secret_access_key: '',
	access_key_configured: false, secret_configured: false, prefix: 'images/', force_path_style: false,
	public_base_url: '', presign_expiry_hours: 24, max_download_bytes: 32 * 1024 * 1024, active: false,
})

const initialBalanceUSD = computed({
  get: () => form.value.user_defaults.balance_micro / 1_000_000,
  set: (value: number | string) => {
    const amount = Number(value)
		const micro = Number.isFinite(amount) && amount >= 0 ? Math.round(amount * 1_000_000) : 0
		form.value.init_balance_micro = micro
		form.value.user_defaults.balance_micro = micro
  },
})
const balanceLowThresholdUSD = computed({
	get: () => form.value.notifications.balance_low_threshold_micro / 1_000_000,
	set: (value: number | string) => {
		const amount = Math.max(0, Number(value) || 0)
		form.value.notifications.balance_low_threshold_micro = Math.round(amount * 1_000_000)
	},
})

async function load() {
  loading.value = true
  try {
		const [data, policy, storage, groupItems] = await Promise.all([
      api.get<SettingsPayload>('/api/admin/settings'),
      api.get<GatewayRuntimePolicy>('/api/admin/runtime-settings'),
			api.get<ImageStorageSettings>('/api/admin/image-storage'),
			api.get<Group[]>('/api/admin/groups'),
    ])
		const defaults = defaultSystemSettings()
		form.value = {
			...defaults,
			...data,
			key_multi_group_enabled: data.key_multi_group_enabled !== false,
			registration_email_suffixes: data.registration_email_suffixes || [],
			trusted_proxies: data.trusted_proxies || [],
			forwarded_client_ip_headers: data.forwarded_client_ip_headers || ['X-Forwarded-For', 'X-Real-IP'],
				login_agreement: {
					enabled: data.login_agreement?.enabled ?? defaults.login_agreement.enabled,
					mode: data.login_agreement?.mode === 'checkbox' ? 'checkbox' : 'modal',
					updated_at: data.login_agreement?.updated_at || '',
					documents: (data.login_agreement?.documents || []).map((item) => ({ ...item })),
				},
				site_customization: {
					...defaults.site_customization, ...(data.site_customization || {}),
					table_page_size_options: data.site_customization?.table_page_size_options || defaults.site_customization.table_page_size_options,
					custom_menu_items: data.site_customization?.custom_menu_items || [], custom_endpoints: data.site_customization?.custom_endpoints || [],
				},
				features: { ...defaults.features, ...(data.features || {}), risk_control_blocked_phrases: data.features?.risk_control_blocked_phrases || [] },
				security: { ...defaults.security, ...(data.security || {}) },
				user_defaults: {
					...defaults.user_defaults, ...(data.user_defaults || {}),
					default_subscriptions: data.user_defaults?.default_subscriptions || [], platform_quotas: data.user_defaults?.platform_quotas || {},
					auth_source_defaults: Object.fromEntries((['email', ...oauthProviderEntries.map((item) => item.key)] as AuthSourceKey[]).map((source) => {
						const base = defaults.user_defaults.auth_source_defaults[source] || { enabled: true, require_email: true, grant_on_signup: false, grant_on_first_bind: false, balance_micro: 0, concurrency: 0, rpm_limit: 0, default_subscriptions: [], platform_quotas: {} }
						const incoming = data.user_defaults?.auth_source_defaults?.[source] || {}
						return [source, { ...base, ...incoming, default_subscriptions: incoming.default_subscriptions || [], platform_quotas: incoming.platform_quotas || {} }]
					})),
				},
				notifications: { ...defaults.notifications, ...(data.notifications || {}), account_quota_emails: data.notifications?.account_quota_emails || [] },
				email: { ...defaults.email, ...(data.email || {}) },
				auth_providers: Object.fromEntries(oauthProviderEntries.map((item) => [item.key, { ...defaults.auth_providers[item.key], ...(data.auth_providers?.[item.key] || {}) }])) as SystemSettings['auth_providers'],
		}
		secretConfigured.value = data.secret_configured || {}
		secretValues.value = emptySecretValues()
    runtime.value = {
      site_public_url: data.site_public_url,
      smtp_configured: data.smtp_configured,
      smtp_from_name: data.smtp_from_name,
      smtp_from: data.smtp_from,
    }
		registrationSuffixesText.value = (data.registration_email_suffixes || []).join('\n')
		trustedProxiesText.value = (data.trusted_proxies || []).join('\n')
		forwardedHeadersText.value = (data.forwarded_client_ip_headers || []).join('\n')
		pageSizeOptionsText.value = (form.value.site_customization.table_page_size_options || []).join(', ')
			notificationEmailsText.value = (form.value.notifications.account_quota_emails || []).join('\n')
			riskPhrasesText.value = (form.value.features.risk_control_blocked_phrases || []).join('\n')
	    runtimePolicy.value = { ...policy, reasoning_effort_multipliers: { ...defaultReasoningMultipliers(), ...(policy.reasoning_effort_multipliers || {}) } }
			codexBlacklistText.value = (policy.codex_client_blacklist || []).join('\n')
			codexWhitelistText.value = (policy.codex_client_whitelist || []).join('\n')
		imageStorage.value = { ...imageStorage.value, ...storage, access_key_id: '', secret_access_key: '' }
		groups.value = groupItems || []
    await loadAudit()
  } finally {
    loading.value = false
  }
}

async function loadAudit() {
  auditLoading.value = true
  try {
    const response = await api.get<{ items: AuditLog[] }>('/api/admin/audit-logs?limit=80')
    auditItems.value = response.items
  } finally {
    auditLoading.value = false
  }
}

function selectSection(section: (typeof sections)[number]['id']) {
  activeSection.value = section
  if (section === 'operations') void loadAudit()
}

function addDocument() {
  const number = form.value.login_agreement.documents.length + 1
  form.value.login_agreement.documents.push({ id: `document-${number}`, title: '新协议文档', content_md: '' })
}

function removeDocument(index: number) {
  form.value.login_agreement.documents.splice(index, 1)
}

function moveDocument(index: number, direction: -1 | 1) {
  const next = index + direction
  const documents = form.value.login_agreement.documents
  if (next < 0 || next >= documents.length) return
  ;[documents[index], documents[next]] = [documents[next], documents[index]]
}

function updateDocumentID(doc: LegalDocument) {
  doc.id = doc.id.toLowerCase().trim().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '')
}

async function save() {
  saving.value = true
  try {
    if (activeSection.value === 'image') {
		const saved = await withToast(() => api.put<ImageStorageSettings>('/api/admin/image-storage', imageStorage.value), '图像存储已保存')
		if (saved) imageStorage.value = { ...imageStorage.value, ...saved, access_key_id: '', secret_access_key: '' }
			} else if (activeSection.value === 'gateway' || activeSection.value === 'operations') {
			runtimePolicy.value.codex_client_blacklist = codexBlacklistText.value.split(/\n+/).map((item) => item.trim()).filter(Boolean)
			runtimePolicy.value.codex_client_whitelist = codexWhitelistText.value.split(/\n+/).map((item) => item.trim()).filter(Boolean)
	      const saved = await withToast(() => api.put<GatewayRuntimePolicy>('/api/admin/runtime-settings', runtimePolicy.value), '网关运行策略已保存')
      if (saved) {
        runtimePolicy.value = saved
        await loadAudit()
      }
    } else {
			form.value.registration_email_suffixes = registrationSuffixesText.value.split(/[\n,;\s]+/).map((item) => item.trim()).filter(Boolean)
			form.value.trusted_proxies = trustedProxiesText.value.split(/[\n,;\s]+/).map((item) => item.trim()).filter(Boolean)
			form.value.forwarded_client_ip_headers = forwardedHeadersText.value.split(/[\n,;]+/).map((item) => item.trim()).filter(Boolean)
			form.value.site_customization.table_page_size_options = pageSizeOptionsText.value.split(/[\s,;]+/).map(Number).filter((value) => Number.isFinite(value))
				form.value.notifications.account_quota_emails = notificationEmailsText.value.split(/[\n,;\s]+/).map((item) => item.trim()).filter(Boolean)
				form.value.features.risk_control_blocked_phrases = riskPhrasesText.value.split(/\n+/).map((item) => item.trim()).filter(Boolean)
			const values = Object.fromEntries(Object.entries(secretValues.value).filter(([, value]) => value !== ''))
			const payload = { ...form.value, secrets: { values, clear: [] } }
      const saved = await withToast(() => api.put<SystemSettings>('/api/admin/settings', payload), '系统设置已保存')
      if (saved) {
        form.value = saved
        await load()
      }
    }
  } finally {
    saving.value = false
  }
}

async function testImageStorage() {
	await withToast(() => api.post('/api/admin/image-storage/test', {}), '对象存储连接正常')
}

function quotaUSD(platform: string, window: 'daily_micro' | 'weekly_micro' | 'monthly_micro') {
	return (form.value.user_defaults.platform_quotas[platform]?.[window] || 0) / 1_000_000
}

function setQuotaUSD(platform: string, window: 'daily_micro' | 'weekly_micro' | 'monthly_micro', event: Event) {
	const value = Math.max(0, Number((event.target as HTMLInputElement).value) || 0)
	const current = form.value.user_defaults.platform_quotas[platform] || { daily_micro: 0, weekly_micro: 0, monthly_micro: 0 }
	form.value.user_defaults.platform_quotas[platform] = { ...current, [window]: Math.round(value * 1_000_000) }
}

function addDefaultSubscription() {
	const selected = new Set(form.value.user_defaults.default_subscriptions.map((item) => item.group_id))
	const group = groups.value.find((item) => !selected.has(item.id))
	if (group) form.value.user_defaults.default_subscriptions.push({ group_id: group.id, validity_days: 30 })
}

function sourceBalanceUSD(source: AuthSourceKey) {
	return (form.value.user_defaults.auth_source_defaults[source]?.balance_micro || 0) / 1_000_000
}

function setSourceBalanceUSD(source: AuthSourceKey, event: Event) {
	const policy = form.value.user_defaults.auth_source_defaults[source]
	if (policy) policy.balance_micro = Math.round(Math.max(0, Number((event.target as HTMLInputElement).value) || 0) * 1_000_000)
}

function sourceQuotaUSD(source: AuthSourceKey, platform: string, window: 'daily_micro' | 'weekly_micro' | 'monthly_micro') {
	return (form.value.user_defaults.auth_source_defaults[source]?.platform_quotas?.[platform]?.[window] || 0) / 1_000_000
}

function setSourceQuotaUSD(source: AuthSourceKey, platform: string, window: 'daily_micro' | 'weekly_micro' | 'monthly_micro', event: Event) {
	const policy = form.value.user_defaults.auth_source_defaults[source]
	if (!policy) return
	if (!policy.platform_quotas) policy.platform_quotas = {}
	const current = policy.platform_quotas[platform] || { daily_micro: 0, weekly_micro: 0, monthly_micro: 0 }
	policy.platform_quotas[platform] = { ...current, [window]: Math.round(Math.max(0, Number((event.target as HTMLInputElement).value) || 0) * 1_000_000) }
}

function addSourceSubscription(source: AuthSourceKey) {
	const policy = form.value.user_defaults.auth_source_defaults[source]
	if (!policy) return
	const selected = new Set(policy.default_subscriptions.map((item) => item.group_id))
	const group = groups.value.find((item) => !selected.has(item.id))
	if (group) policy.default_subscriptions.push({ group_id: group.id, validity_days: 30 })
}

function addCustomMenuItem() {
	form.value.site_customization.custom_menu_items.push({ id: `menu-${Date.now()}`, name: '', url: '', icon_svg: '', visibility: 'all' })
}

function addCustomEndpoint() {
	form.value.site_customization.custom_endpoints.push({ id: `endpoint-${Date.now()}`, name: '', url: '', description: '' })
}

async function testEmail() {
	emailTesting.value = true
	try {
		await withToast(() => api.post('/api/admin/settings/email/test', { to: emailTestRecipient.value }), '测试邮件已发送')
	} finally {
		emailTesting.value = false
	}
}

onMounted(load)
</script>

<template>
  <div class="settings-page">
    <div class="console-page-head settings-page-head">
      <div>
        <h1>系统设置</h1>
        <p>管理站点对外展示、注册策略与登录前需要确认的协议。</p>
      </div>
      <button class="btn-primary" :disabled="loading || saving" @click="save">{{ saving ? '保存中…' : (activeSection === 'gateway' || activeSection === 'operations' ? '保存运行策略' : '保存设置') }}</button>
    </div>

    <div v-if="loading" class="settings-loading">正在读取系统设置…</div>
    <div v-else class="settings-layout">
      <nav class="settings-nav" aria-label="系统设置分区">
        <button v-for="section in sections" :key="section.id" type="button" :class="{ 'is-active': activeSection === section.id }" @click="selectSection(section.id)">
          <strong>{{ section.label }}</strong>
          <span>{{ section.hint }}</span>
        </button>
      </nav>

      <section class="settings-content">
        <template v-if="activeSection === 'general'">
						<section class="settings-section">
            <header>
              <h2>站点展示</h2>
              <p>这些内容会出现在登录页、浏览器标题和控制台导航中。</p>
            </header>
            <div class="settings-form-grid">
	              <label class="settings-field">
                <span>站点名称</span>
                <input v-model="form.site_name" class="input" maxlength="120" />
	              </label>
              <label class="settings-field">
                <span>登录页说明</span>
                <input v-model="form.site_subtitle" class="input" maxlength="240" placeholder="一句简短的服务说明" />
              </label>
            </div>
          </section>

			<section class="settings-section settings-section--quiet">
				<header>
					<h2>品牌与公开入口</h2>
					<p>这些字段会安全地暴露给登录页和公开主页；正文按 Markdown 文本渲染，不执行 HTML。</p>
				</header>
				<div class="settings-form-grid">
					<label class="settings-field"><span>Logo 地址</span><input v-model="form.site_customization.logo_url" class="input" placeholder="/assets/icon.png 或 https://…" /></label>
					<label class="settings-field"><span>文档地址</span><input v-model="form.site_customization.docs_url" class="input" placeholder="https://docs.example.com" /></label>
					<label class="settings-field"><span>联系方式</span><input v-model="form.site_customization.contact_info" class="input" placeholder="QQ群、邮箱或工单地址" /></label>
					<label class="settings-field"><span>默认分页数量</span><input v-model.number="form.site_customization.table_default_page_size" class="input" type="number" min="5" max="500" /></label>
				</div>
				<label class="settings-field settings-field--spaced"><span>可选分页数量</span><input v-model="pageSizeOptionsText" class="input" placeholder="10, 20, 50, 100" /><small>使用逗号分隔，范围 5–500；默认值会自动加入。</small></label>
				<label class="settings-field settings-field--spaced"><span>公开主页内容</span><textarea v-model="form.site_customization.home_content" rows="6" class="input settings-document-editor__text" placeholder="可选 Markdown 内容"></textarea></label>
				<div class="settings-toggle-stack">
					<label class="settings-toggle-row"><span><strong>后台模式</strong><small>公开主页只保留登录入口和模型广场。</small></span><input v-model="form.site_customization.backend_mode_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>隐藏 CCS 导入入口</strong><small>仅隐藏用户界面的导入按钮，不删除已有配置。</small></span><input v-model="form.site_customization.hide_ccs_import_button" type="checkbox" role="switch" /></label>
				</div>
			</section>

			<section class="settings-section settings-section--quiet">
				<header class="settings-section__with-control"><div><h2>自定义菜单</h2><p>为控制台增加外部或站内入口，可分别限制普通用户和管理员。</p></div><button type="button" class="btn-ghost" @click="addCustomMenuItem">添加菜单</button></header>
				<div v-if="!form.site_customization.custom_menu_items.length" class="settings-empty-state">暂无自定义菜单。</div>
				<div v-else class="settings-editor-list">
					<article v-for="(item, index) in form.site_customization.custom_menu_items" :key="item.id" class="settings-inline-card">
						<div class="settings-form-grid settings-form-grid--three">
							<label class="settings-field"><span>名称</span><input v-model="item.name" class="input" /></label>
							<label class="settings-field"><span>地址</span><input v-model="item.url" class="input" placeholder="/path 或 https://…" /></label>
							<label class="settings-field"><span>可见范围</span><select v-model="item.visibility" class="input"><option value="all">全部</option><option value="user">普通用户</option><option value="admin">管理员</option></select></label>
						</div>
						<div class="settings-inline-card__actions"><button type="button" class="is-danger" @click="form.site_customization.custom_menu_items.splice(index, 1)">删除</button></div>
					</article>
				</div>
			</section>

			<section class="settings-section settings-section--quiet">
				<header class="settings-section__with-control"><div><h2>自定义 API 端点</h2><p>在公开帮助与快速配置中展示额外端点说明。</p></div><button type="button" class="btn-ghost" @click="addCustomEndpoint">添加端点</button></header>
				<div v-if="!form.site_customization.custom_endpoints.length" class="settings-empty-state">暂无自定义端点。</div>
				<div v-else class="settings-editor-list">
					<article v-for="(item, index) in form.site_customization.custom_endpoints" :key="item.id" class="settings-inline-card">
						<div class="settings-form-grid settings-form-grid--three">
							<label class="settings-field"><span>名称</span><input v-model="item.name" class="input" /></label>
							<label class="settings-field"><span>地址</span><input v-model="item.url" class="input" /></label>
							<label class="settings-field"><span>说明</span><input v-model="item.description" class="input" /></label>
						</div>
						<div class="settings-inline-card__actions"><button type="button" class="is-danger" @click="form.site_customization.custom_endpoints.splice(index, 1)">删除</button></div>
					</article>
				</div>
			</section>

          <section class="settings-section settings-section--quiet">
            <header>
              <h2>注册邮箱范围</h2>
              <p>留空允许所有有效邮箱。填写后，验证码发送和账户创建都会只接受这些域名及其子域名。</p>
            </header>
            <label class="settings-field">
              <span>允许的邮箱域名</span>
              <textarea v-model="registrationSuffixesText" rows="3" class="input settings-document-editor__text" placeholder="example.com&#10;company.cn"></textarea>
              <small>一行一个，也可用逗号分隔。不要填写邮箱地址；填 example.com 会同时允许 team.example.com。</small>
            </label>
          </section>

          <section class="settings-section settings-section--quiet">
            <header>
              <h2>服务环境</h2>
              <p>部署地址和邮件连接信息由服务器环境变量管理，避免在网页中暴露凭据。</p>
            </header>
            <dl class="settings-status-list">
              <div><dt>公开地址</dt><dd>{{ runtime.site_public_url || '未配置' }}</dd></div>
              <div><dt>邮件验证</dt><dd :class="runtime.smtp_configured ? 'is-ok' : 'is-warn'">{{ runtime.smtp_configured ? '已配置' : '未配置' }}</dd></div>
              <div><dt>发件人</dt><dd>{{ runtime.smtp_from_name || runtime.smtp_from || '使用 SMTP 默认发件人' }}</dd></div>
            </dl>
          </section>

					<section class="settings-section settings-section--quiet">
						<header>
							<h2>真实客户端 IP</h2>
							<p>只会信任下列反向代理传入的 IP 请求头；留空受信代理表示不信任任何代理。</p>
						</header>
						<div class="settings-form-grid">
							<label class="settings-field">
								<span>受信代理 IP / CIDR</span>
								<textarea v-model="trustedProxiesText" rows="4" class="input settings-document-editor__text" placeholder="127.0.0.1&#10;10.0.0.0/8"></textarea>
							</label>
							<label class="settings-field">
								<span>客户端 IP 请求头</span>
								<textarea v-model="forwardedHeadersText" rows="4" class="input settings-document-editor__text" placeholder="X-Forwarded-For&#10;X-Real-IP"></textarea>
							</label>
						</div>
					</section>
        </template>

        <template v-else-if="activeSection === 'features'">
			<section class="settings-section">
				<header><h2>面向用户的功能</h2><p>关闭后同时隐藏入口并停止对应后台行为；已有数据不会删除。</p></header>
				<div class="settings-toggle-stack">
					<label class="settings-toggle-row"><span><strong>渠道健康监控</strong><small>关闭后不再自动或手动探测上游账号。</small></span><input v-model="form.features.channel_monitor_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>公开模型广场</strong><small>控制未登录模型目录和控制台模型广场入口。</small></span><input v-model="form.features.model_plaza_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>推广返利</strong><small>控制推广入口、新绑定和后续佣金结算。</small></span><input v-model="form.features.referral_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>用户查看错误请求</strong><small>允许普通用户在自己的用量明细中查看失败请求与错误摘要。</small></span><input v-model="form.features.allow_user_view_error_requests" type="checkbox" role="switch" /></label>
				</div>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>监控默认值</h2><p>单个运行策略仍可覆盖检测间隔；关闭监控时该值保留。</p></header>
				<label class="settings-field settings-field--compact"><span>默认检测间隔（秒）</span><input v-model.number="form.features.channel_monitor_interval_seconds" class="input" type="number" min="15" max="86400" /></label>
			</section>
				<section class="settings-section settings-section--quiet">
					<header><h2>风控中心</h2><p>按请求正文匹配规则；命中内容只记录规则摘要，不保存完整请求正文。</p></header>
					<label class="settings-toggle-row"><span><strong>启用风控中心</strong><small>开启后才执行已配置的内容审核规则。</small></span><input v-model="form.features.risk_control_enabled" type="checkbox" role="switch" /></label>
					<div class="settings-form-grid settings-fields-spaced">
						<label class="settings-field"><span>命中动作</span><select v-model="form.features.risk_control_action" class="input"><option value="block">拦截请求</option><option value="log">仅记录</option></select><small>“仅记录”适合先观察规则误报。</small></label>
						<label class="settings-field"><span>阻断短语</span><textarea v-model="riskPhrasesText" rows="5" class="input settings-document-editor__text" placeholder="一行一条，不区分大小写"></textarea><small>最多 200 条，每条最多 200 字符。</small></label>
					</div>
				</section>
		</template>

        <template v-else-if="activeSection === 'security'">
			<section class="settings-section">
				<header><h2>账户验证</h2><p>这些开关会直接限制注册、密码恢复和验证器绑定接口。</p></header>
				<div class="settings-toggle-stack">
					<label class="settings-toggle-row"><span><strong>注册邮箱验证</strong><small>开启且 SMTP 可用时，新用户必须使用邮件验证码。</small></span><input v-model="form.security.email_verification_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>忘记密码</strong><small>允许用户通过验证邮件重置密码。</small></span><input v-model="form.security.password_reset_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>TOTP 双因素认证</strong><small>允许账户绑定验证器；已绑定账户不因关闭开关而自动解除。</small></span><input v-model="form.security.totp_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>会话 IP / UA 绑定</strong><small>客户端指纹变化后要求重新登录。</small></span><input v-model="form.security.session_binding_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>敏感操作二次验证</strong><small>导出凭据、备份下载和安全设置变更要求最近完成 TOTP。</small></span><input v-model="form.security.step_up_enabled" type="checkbox" role="switch" /></label>
				</div>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>Cloudflare Turnstile</h2><p>用于登录、注册和验证码发送的机器人防护；私密密钥单独加密保存。</p></header>
				<label class="settings-toggle-row"><span><strong>启用 Turnstile</strong><small>启用前需同时填写站点密钥和私密密钥。</small></span><input v-model="form.security.turnstile_enabled" type="checkbox" role="switch" /></label>
				<div class="settings-form-grid settings-fields-spaced">
					<label class="settings-field"><span>站点密钥</span><input v-model="form.security.turnstile_site_key" class="input" autocomplete="off" /></label>
					<label class="settings-field"><span>私密密钥</span><input v-model="secretValues.turnstile_secret" type="password" class="input" :placeholder="secretConfigured.turnstile_secret ? '已配置，留空不修改' : '尚未配置'" autocomplete="new-password" /></label>
				</div>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>第三方登录</h2><p>启用后会出现在登录页。回调状态、PKCE 和一次性登录码由服务端校验，Client Secret 单独加密保存。</p></header>
				<div class="settings-oauth-list">
					<details v-for="item in oauthProviderEntries" :key="item.key" class="settings-oauth-card">
						<summary><div><strong>{{ item.label }}</strong><small>{{ form.auth_providers[item.key].enabled ? '已启用' : '已关闭' }}</small></div><span>配置</span></summary>
						<label class="settings-toggle-row settings-toggle-row--small"><span><strong>启用 {{ item.label }} 登录</strong></span><input v-model="form.auth_providers[item.key].enabled" type="checkbox" role="switch" /></label>
						<div class="settings-form-grid settings-form-grid--three">
							<label class="settings-field"><span>显示名称</span><input v-model="form.auth_providers[item.key].provider_name" class="input" /></label>
							<label class="settings-field"><span>Client ID</span><input v-model="form.auth_providers[item.key].client_id" class="input" autocomplete="off" /></label>
							<label class="settings-field"><span>Client Secret</span><input v-model="secretValues[item.secret]" type="password" class="input" autocomplete="new-password" :placeholder="secretConfigured[item.secret] ? '已配置，留空不修改' : '尚未配置'" /></label>
							<label class="settings-field"><span>Authorize URL</span><input v-model="form.auth_providers[item.key].authorize_url" class="input" /></label>
							<label class="settings-field"><span>Token URL</span><input v-model="form.auth_providers[item.key].token_url" class="input" /></label>
							<label class="settings-field"><span>UserInfo URL</span><input v-model="form.auth_providers[item.key].userinfo_url" class="input" /></label>
							<label class="settings-field"><span>后端回调地址</span><input v-model="form.auth_providers[item.key].redirect_url" class="input" :placeholder="`${runtime.site_public_url || 'https://站点域名'}/api/auth/oauth/${item.key}/callback`" /></label>
							<label class="settings-field"><span>前端回调路径</span><input v-model="form.auth_providers[item.key].frontend_redirect_url" class="input" /></label>
							<label class="settings-field"><span>Scopes</span><input v-model="form.auth_providers[item.key].scopes" class="input" /></label>
								<label v-if="item.key === 'oidc' || item.key === 'google'" class="settings-field"><span>Issuer URL</span><input v-model="form.auth_providers[item.key].issuer_url" class="input" /></label>
								<label v-if="item.key === 'oidc'" class="settings-field"><span>Discovery URL</span><input v-model="form.auth_providers.oidc.discovery_url" class="input" /></label>
								<label v-if="item.key === 'oidc' || item.key === 'google'" class="settings-field"><span>JWKS URL</span><input v-model="form.auth_providers[item.key].jwks_url" class="input" placeholder="https://.../.well-known/jwks.json" /></label>
								<label v-if="item.key === 'oidc' || item.key === 'google'" class="settings-field"><span>允许签名算法</span><input v-model="form.auth_providers[item.key].allowed_signing_algs" class="input" placeholder="RS256,ES256,PS256" /></label>
							<label class="settings-field"><span>用户 ID 字段</span><input v-model="form.auth_providers[item.key].id_path" class="input" placeholder="sub" /></label>
							<label class="settings-field"><span>邮箱字段</span><input v-model="form.auth_providers[item.key].email_path" class="input" placeholder="email" /></label>
						</div>
							<div class="settings-toggle-stack settings-toggle-stack--compact"><label class="settings-toggle-row"><span><strong>使用 PKCE</strong></span><input v-model="form.auth_providers[item.key].use_pkce" type="checkbox" role="switch" /></label><label v-if="item.key === 'oidc' || item.key === 'google'" class="settings-toggle-row"><span><strong>严格校验 ID Token</strong></span><input v-model="form.auth_providers[item.key].validate_id_token" type="checkbox" role="switch" /></label><label class="settings-toggle-row"><span><strong>要求已验证邮箱</strong></span><input v-model="form.auth_providers[item.key].require_verified_email" type="checkbox" role="switch" /></label></div>
						<div v-if="form.user_defaults.auth_source_defaults[item.key]" class="settings-source-policy">
							<div class="settings-section__with-control"><div><strong>来源专属默认值</strong><p>开启“注册时发放”后覆盖全局新用户默认值。</p></div><button type="button" class="btn-ghost" @click="addSourceSubscription(item.key)">添加订阅</button></div>
							<div class="settings-form-grid settings-form-grid--three">
								<label class="settings-field"><span>初始余额（USD）</span><input :value="sourceBalanceUSD(item.key)" type="number" min="0" step="0.01" class="input" @input="setSourceBalanceUSD(item.key, $event)" /></label>
								<label class="settings-field"><span>并发</span><input v-model.number="form.user_defaults.auth_source_defaults[item.key].concurrency" type="number" min="0" class="input" /></label>
								<label class="settings-field"><span>RPM</span><input v-model.number="form.user_defaults.auth_source_defaults[item.key].rpm_limit" type="number" min="0" class="input" /></label>
							</div>
								<div class="settings-toggle-stack settings-toggle-stack--compact"><label class="settings-toggle-row"><span><strong>注册时发放</strong></span><input v-model="form.user_defaults.auth_source_defaults[item.key].grant_on_signup" type="checkbox" role="switch" /></label><label class="settings-toggle-row"><span><strong>首次绑定时追加发放</strong></span><input v-model="form.user_defaults.auth_source_defaults[item.key].grant_on_first_bind" type="checkbox" role="switch" /></label><label class="settings-toggle-row"><span><strong>必须提供邮箱</strong></span><input v-model="form.user_defaults.auth_source_defaults[item.key].require_email" type="checkbox" role="switch" /></label></div>
								<div v-for="(subscription, subscriptionIndex) in form.user_defaults.auth_source_defaults[item.key].default_subscriptions" :key="`${subscription.group_id}-${subscriptionIndex}`" class="settings-inline-card settings-inline-card--row"><label class="settings-field"><span>分组</span><select v-model.number="subscription.group_id" class="input"><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ group.platform }}</option></select></label><label class="settings-field"><span>有效天数</span><input v-model.number="subscription.validity_days" type="number" min="1" max="36500" class="input" /></label><button type="button" class="settings-remove-button" @click="form.user_defaults.auth_source_defaults[item.key].default_subscriptions.splice(subscriptionIndex, 1)">删除</button></div>
								<details class="settings-source-quotas"><summary>来源专属平台额度</summary><div class="settings-quota-grid"><article v-for="platform in platforms" :key="platform.id" class="settings-quota-card"><strong>{{ platform.label }}</strong><label class="settings-field"><span>每日（USD）</span><input :value="sourceQuotaUSD(item.key, platform.id, 'daily_micro')" type="number" min="0" step="0.01" class="input" @input="setSourceQuotaUSD(item.key, platform.id, 'daily_micro', $event)" /></label><label class="settings-field"><span>每周（USD）</span><input :value="sourceQuotaUSD(item.key, platform.id, 'weekly_micro')" type="number" min="0" step="0.01" class="input" @input="setSourceQuotaUSD(item.key, platform.id, 'weekly_micro', $event)" /></label><label class="settings-field"><span>每月（USD）</span><input :value="sourceQuotaUSD(item.key, platform.id, 'monthly_micro')" type="number" min="0" step="0.01" class="input" @input="setSourceQuotaUSD(item.key, platform.id, 'monthly_micro', $event)" /></label></article></div></details>
						</div>
					</details>
				</div>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>审计与来源地址</h2><p>操作日志和 API Key IP 规则都使用同一套可信代理解析结果。</p></header>
				<div class="settings-form-grid">
					<label class="settings-field"><span>操作日志保留天数</span><input v-model.number="form.security.audit_log_retention_days" class="input" type="number" min="0" max="3650" /><small>0 表示永久保留。</small></label>
					<label class="settings-toggle-row"><span><strong>信任反代客户端 IP</strong><small>只应在源站无法被直接访问时开启。</small></span><input v-model="form.security.trust_forwarded_ip" type="checkbox" role="switch" /></label>
				</div>
			</section>
		</template>

        <template v-else-if="activeSection === 'users'">
          <section class="settings-section">
            <header>
              <h2>注册策略</h2>
              <p>关闭后，登录页会隐藏注册入口，并拒绝新的验证码和注册请求。</p>
            </header>
            <label class="settings-toggle-row">
              <span>
                <strong>允许邮箱注册</strong>
                <small>注册时仍需完成邮箱验证码验证。</small>
              </span>
              <input v-model="form.allow_register" type="checkbox" role="switch" />
            </label>
						</section>

          <section class="settings-section">
            <header>
              <h2>密钥分组策略</h2>
              <p>控制普通用户创建和编辑 API 密钥时能否同时绑定多个分组。</p>
            </header>
            <label class="settings-toggle-row">
              <span>
                <strong>允许密钥绑定多个分组</strong>
                <small>关闭后会立即保留每把密钥的主分组并移除额外绑定，新建和编辑都改为单选。</small>
              </span>
              <input v-model="form.key_multi_group_enabled" type="checkbox" role="switch" />
            </label>
          </section>

          <section class="settings-section">
            <header>
              <h2>新用户初始额度</h2>
              <p>只对之后创建的账户生效，不会改动已有用户的余额。</p>
            </header>
			<div class="settings-form-grid settings-form-grid--three">
				<label class="settings-field"><span>初始余额（USD）</span><input v-model.number="initialBalanceUSD" type="number" min="0" step="0.01" class="input" /></label>
				<label class="settings-field"><span>用户并发上限</span><input v-model.number="form.user_defaults.concurrency" type="number" min="0" max="10000" class="input" /><small>0 表示不限制。</small></label>
				<label class="settings-field"><span>用户每分钟请求数</span><input v-model.number="form.user_defaults.rpm_limit" type="number" min="0" max="1000000" class="input" /><small>独立于单把密钥的 RPM，0 表示不限制。</small></label>
			</div>
          </section>

		<section v-if="form.user_defaults.auth_source_defaults.email" class="settings-section settings-section--quiet">
			<header><h2>邮箱注册专属默认值</h2><p>开启后覆盖上面的全局默认值，只影响之后通过邮箱注册的用户。</p></header>
			<label class="settings-toggle-row"><span><strong>注册时发放</strong><small>关闭时继续使用全局新用户默认值。</small></span><input v-model="form.user_defaults.auth_source_defaults.email.grant_on_signup" type="checkbox" role="switch" /></label>
			<div class="settings-form-grid settings-form-grid--three settings-fields-spaced"><label class="settings-field"><span>初始余额（USD）</span><input :value="sourceBalanceUSD('email')" type="number" min="0" step="0.01" class="input" @input="setSourceBalanceUSD('email', $event)" /></label><label class="settings-field"><span>并发</span><input v-model.number="form.user_defaults.auth_source_defaults.email.concurrency" type="number" min="0" class="input" /></label><label class="settings-field"><span>RPM</span><input v-model.number="form.user_defaults.auth_source_defaults.email.rpm_limit" type="number" min="0" class="input" /></label></div>
		</section>

		<section class="settings-section settings-section--quiet">
			<header class="settings-section__with-control"><div><h2>默认分组订阅</h2><p>注册后自动获得对应分组的限时免余额访问；到期后恢复按余额计费。</p></div><button type="button" class="btn-ghost" :disabled="form.user_defaults.default_subscriptions.length >= groups.length" @click="addDefaultSubscription">添加订阅</button></header>
			<div v-if="!form.user_defaults.default_subscriptions.length" class="settings-empty-state">未配置默认订阅。</div>
			<div v-else class="settings-editor-list">
				<div v-for="(item, index) in form.user_defaults.default_subscriptions" :key="`${item.group_id}-${index}`" class="settings-inline-card settings-inline-card--row">
					<label class="settings-field"><span>分组</span><select v-model.number="item.group_id" class="input"><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ group.platform }}</option></select></label>
					<label class="settings-field"><span>有效天数</span><input v-model.number="item.validity_days" type="number" min="1" max="36500" class="input" /></label>
					<button type="button" class="settings-remove-button" @click="form.user_defaults.default_subscriptions.splice(index, 1)">删除</button>
				</div>
			</div>
		</section>

		<section class="settings-section settings-section--quiet">
			<header><h2>平台用量上限</h2><p>只对之后注册的用户生效；按平台统计实际费用，日/周/月分别使用 24 小时、7 天、30 天固定滚动窗口，0 表示不限制。</p></header>
			<div class="settings-quota-grid">
				<article v-for="platform in platforms" :key="platform.id" class="settings-quota-card">
					<strong>{{ platform.label }}</strong>
					<label class="settings-field"><span>每日（USD）</span><input :value="quotaUSD(platform.id, 'daily_micro')" type="number" min="0" step="0.01" class="input" @input="setQuotaUSD(platform.id, 'daily_micro', $event)" /></label>
					<label class="settings-field"><span>每周（USD）</span><input :value="quotaUSD(platform.id, 'weekly_micro')" type="number" min="0" step="0.01" class="input" @input="setQuotaUSD(platform.id, 'weekly_micro', $event)" /></label>
					<label class="settings-field"><span>每月（USD）</span><input :value="quotaUSD(platform.id, 'monthly_micro')" type="number" min="0" step="0.01" class="input" @input="setQuotaUSD(platform.id, 'monthly_micro', $event)" /></label>
				</article>
			</div>
		</section>
        </template>

        <template v-else-if="activeSection === 'agreement'">
          <section class="settings-section">
            <header class="settings-section__with-control">
              <div>
                <h2>登录前协议确认</h2>
                <p>启用后，登录和注册均需确认最新版本。修改日期或文档内容会要求用户再次同意。</p>
              </div>
              <label class="settings-toggle-row settings-toggle-row--small">
                <span>{{ form.login_agreement.enabled ? '已启用' : '已关闭' }}</span>
                <input v-model="form.login_agreement.enabled" type="checkbox" role="switch" />
              </label>
            </header>

            <div class="settings-form-grid settings-form-grid--agreement">
              <div class="settings-field">
                <span>展示方式</span>
                <div class="settings-choice-group">
                  <button type="button" :class="{ 'is-active': form.login_agreement.mode === 'modal' }" @click="form.login_agreement.mode = 'modal'">弹窗确认</button>
                  <button type="button" :class="{ 'is-active': form.login_agreement.mode === 'checkbox' }" @click="form.login_agreement.mode = 'checkbox'">复选框</button>
                </div>
                <small>{{ form.login_agreement.mode === 'modal' ? '进入登录页后先阅读条款，再解锁表单。' : '协议会显示在登录按钮下方，勾选后可继续。' }}</small>
              </div>
              <label class="settings-field">
                <span>条款更新日期</span>
                <input v-model="form.login_agreement.updated_at" type="date" class="input" />
              </label>
            </div>
          </section>

          <section class="settings-section">
            <header class="settings-section__with-control">
              <div>
                <h2>协议文档</h2>
                <p>内容会以安全的纯文本格式呈现在独立页面。默认包含用户协议、隐私政策、可接受使用政策、服务特定条款、免责声明与开源软件说明。</p>
              </div>
              <button type="button" class="btn-ghost !px-3 !py-1.5 text-xs" @click="addDocument">添加文档</button>
            </header>

            <div class="settings-documents">
              <article v-for="(doc, index) in form.login_agreement.documents" :key="`${doc.id}-${index}`" class="settings-document-editor">
                <div class="settings-document-editor__head">
                  <span>文档 {{ index + 1 }}</span>
                  <div>
                    <button type="button" :disabled="index === 0" @click="moveDocument(index, -1)">上移</button>
                    <button type="button" :disabled="index === form.login_agreement.documents.length - 1" @click="moveDocument(index, 1)">下移</button>
                    <button type="button" class="is-danger" @click="removeDocument(index)">删除</button>
                  </div>
                </div>
                <div class="settings-document-editor__fields">
                  <label class="settings-field"><span>标题</span><input v-model="doc.title" class="input" maxlength="64" placeholder="例如：服务条款" /></label>
                  <label class="settings-field"><span>文档 ID</span><input v-model="doc.id" class="input font-mono" maxlength="64" placeholder="terms" @blur="updateDocumentID(doc)" /></label>
                </div>
                <label class="settings-field"><span>正文</span><textarea v-model="doc.content_md" rows="12" class="input settings-document-editor__text" placeholder="使用普通文本；章节标题可写成“一、适用范围”，无需输入 Markdown 的井号。"></textarea></label>
              </article>
            </div>
          </section>
        </template>

		<template v-else-if="activeSection === 'email'">
			<section class="settings-section">
				<header><h2>SMTP 连接</h2><p>保存后立即生效，不需要重启服务；密码会单独加密保存且不会返回浏览器。</p></header>
				<div class="settings-form-grid settings-form-grid--three">
					<label class="settings-field"><span>服务器</span><input v-model="form.email.host" class="input" placeholder="smtp.example.com" /></label>
					<label class="settings-field"><span>端口</span><input v-model.number="form.email.port" type="number" min="1" max="65535" class="input" /></label>
					<label class="settings-field"><span>用户名</span><input v-model="form.email.username" class="input" autocomplete="off" /></label>
					<label class="settings-field"><span>密码 / 授权码</span><input v-model="secretValues.smtp_password" type="password" class="input" autocomplete="new-password" :placeholder="secretConfigured.smtp_password ? '已配置，留空不修改' : '尚未配置'" /></label>
					<label class="settings-field"><span>发件人名称</span><input v-model="form.email.from_name" class="input" /></label>
					<label class="settings-field"><span>发件邮箱</span><input v-model="form.email.from" type="email" class="input" placeholder="no-reply@example.com" /></label>
				</div>
				<label class="settings-toggle-row"><span><strong>使用 TLS 直连</strong><small>465 通常开启；587 通常关闭后自动使用 STARTTLS。</small></span><input v-model="form.email.use_tls" type="checkbox" role="switch" /></label>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>发送测试</h2><p>请先保存连接配置，再向指定邮箱发送一封品牌样式测试邮件。</p></header>
				<div class="settings-test-row"><input v-model="emailTestRecipient" type="email" class="input" placeholder="收件邮箱" /><button type="button" class="btn-ghost" :disabled="emailTesting || !emailTestRecipient" @click="testEmail">{{ emailTesting ? '发送中…' : '发送测试邮件' }}</button></div>
			</section>
			<section class="settings-section settings-section--quiet">
				<header><h2>通知策略</h2><p>控制余额、订阅和上游额度提醒的触发条件。</p></header>
				<div class="settings-toggle-stack">
					<label class="settings-toggle-row"><span><strong>低余额提醒</strong><small>用户余额低于阈值时发送提醒。</small></span><input v-model="form.notifications.balance_low_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>订阅到期提醒</strong><small>限时订阅临近到期时发送提醒。</small></span><input v-model="form.notifications.subscription_expiry_enabled" type="checkbox" role="switch" /></label>
					<label class="settings-toggle-row"><span><strong>上游额度提醒</strong><small>账号额度刷新后触发运营通知。</small></span><input v-model="form.notifications.account_quota_enabled" type="checkbox" role="switch" /></label>
				</div>
				<div class="settings-form-grid settings-fields-spaced">
					<label class="settings-field"><span>低余额阈值（USD）</span><input v-model.number="balanceLowThresholdUSD" type="number" min="0" step="0.01" class="input" /></label>
					<label class="settings-field"><span>充值地址</span><input v-model="form.notifications.balance_low_recharge_url" class="input" /></label>
				</div>
				<label class="settings-field settings-field--spaced"><span>运营通知邮箱</span><textarea v-model="notificationEmailsText" rows="3" class="input settings-document-editor__text" placeholder="一行一个邮箱"></textarea></label>
			</section>
		</template>

					<template v-else-if="activeSection === 'image'">
					<section class="settings-section">
						<header class="settings-section__with-control">
							<div><h2>S3 兼容对象存储</h2><p>启用后开放 <code>/v1/images/generations/async</code>，结果上传后只保存链接。</p></div>
							<label class="settings-toggle-row settings-toggle-row--small"><span>{{ imageStorage.active ? '运行中' : imageStorage.enabled ? '配置未完整' : '已关闭' }}</span><input v-model="imageStorage.enabled" type="checkbox" role="switch" /></label>
						</header>
						<div class="settings-form-grid">
							<label class="settings-field"><span>Endpoint</span><input v-model="imageStorage.endpoint" class="input" placeholder="https://...r2.cloudflarestorage.com" /></label>
							<label class="settings-field"><span>Region</span><input v-model="imageStorage.region" class="input" placeholder="auto" /></label>
							<label class="settings-field"><span>Bucket</span><input v-model="imageStorage.bucket" class="input" /></label>
							<label class="settings-field"><span>对象前缀</span><input v-model="imageStorage.prefix" class="input" placeholder="images/" /></label>
							<label class="settings-field"><span>Access Key ID</span><input v-model="imageStorage.access_key_id" class="input" :placeholder="imageStorage.access_key_configured ? '已配置，留空不修改' : ''" autocomplete="off" /></label>
							<label class="settings-field"><span>Secret Access Key</span><input v-model="imageStorage.secret_access_key" type="password" class="input" :placeholder="imageStorage.secret_configured ? '已配置，留空不修改' : ''" autocomplete="new-password" /></label>
							<label class="settings-field"><span>公开访问地址（可选）</span><input v-model="imageStorage.public_base_url" class="input" placeholder="https://img.example.com" /></label>
							<label class="settings-field"><span>预签名有效期（小时）</span><input v-model.number="imageStorage.presign_expiry_hours" type="number" min="1" max="168" class="input" /></label>
						</div>
						<div class="mt-4 flex flex-wrap items-center gap-4">
							<label class="flex items-center gap-2 text-sm text-slate-300"><input v-model="imageStorage.force_path_style" type="checkbox" /> 路径风格（MinIO）</label>
							<button type="button" class="btn-ghost" @click="testImageStorage">测试连接</button>
						</div>
					</section>
				</template>

        <template v-else-if="activeSection === 'gateway'">
          <section class="settings-section">
            <header>
              <h2>故障切换</h2>
              <p>只对可重试的上游错误生效。每次请求会按优先级和最近使用时间选择账号，达到次数或账号耗尽后才返回错误。</p>
            </header>
            <div class="settings-form-grid settings-form-grid--three">
              <label class="settings-field">
                <span>单次请求最大尝试次数</span>
                <input v-model.number="runtimePolicy.max_attempts" class="input" type="number" min="1" max="8" />
                <small>范围 1–8。仅在上游网络错误、429 或 5xx 时切换候选账号。</small>
              </label>
              <label class="settings-field">
                <span>未授权冷却（秒）</span>
                <input v-model.number="runtimePolicy.unauthorized_cooldown_seconds" class="input" type="number" min="30" max="86400" />
                <small>401 / 403 后暂停该账号，避免持续发送无效凭据。</small>
              </label>
              <label class="settings-field">
                <span>限流冷却（秒）</span>
                <input v-model.number="runtimePolicy.rate_limit_cooldown_seconds" class="input" type="number" min="5" max="3600" />
                <small>429 后暂时避开该账号，给上游恢复窗口。</small>
              </label>
            </div>
          </section>

          <section class="settings-section settings-section--quiet">
            <header>
              <h2>异常恢复</h2>
              <p>这些值决定账号发生临时错误后多久重新参与调度。不会改写账号状态或凭据。</p>
            </header>
            <div class="settings-form-grid settings-form-grid--three">
	              <label class="settings-field"><span>上游 5xx 冷却（秒）</span><input v-model.number="runtimePolicy.upstream_failure_cooldown_seconds" class="input" type="number" min="5" max="3600" /></label>
	              <label class="settings-field"><span>过载冷却（秒）</span><input v-model.number="runtimePolicy.overload_cooldown_seconds" class="input" type="number" min="30" max="86400" /><small>上游 529 后单独冷却，不与普通 5xx 共用。</small></label>
              <label class="settings-field"><span>网络错误冷却（秒）</span><input v-model.number="runtimePolicy.network_failure_cooldown_seconds" class="input" type="number" min="1" max="3600" /></label>
            </div>
          </section>

						<section class="settings-section">
							<header>
								<h2>并发保护</h2>
							<p>用户、密钥或上游账号达到并发上限后进入有界等待；超时或队列满时返回 429，避免请求堆积拖垮网关。</p>
						</header>
						<div class="settings-form-grid settings-form-grid--three">
							<label class="settings-field"><span>最长等待（毫秒）</span><input v-model.number="runtimePolicy.concurrency_wait_milliseconds" class="input" type="number" min="100" max="60000" step="100" /><small>范围 100–60000，覆盖客户端槽和上游账号槽。</small></label>
							<label class="settings-field"><span>最大等待请求数</span><input v-model.number="runtimePolicy.concurrency_queue_depth" class="input" type="number" min="1" max="10000" step="1" /><small>超过后立即返回 429，并带 Retry-After。</small></label>
						</div>
						</section>

			<section class="settings-section settings-section--quiet">
				<header><h2>OAuth 请求转发</h2><p>控制订阅账号的客户端身份和 Anthropic 缓存处理，保存后立即应用到新请求。</p></header>
					<div class="settings-toggle-stack">
						<label class="settings-toggle-row"><span><strong>统一客户端指纹</strong><small>为共享 OAuth 账号使用统一的官方客户端请求头；关闭后透传客户端 UA 与 Stainless 头。</small></span><input v-model="runtimePolicy.fingerprint_unification" type="checkbox" role="switch" /></label>
						<label class="settings-toggle-row"><span><strong>Responses metadata 透传</strong><small>向 Codex OAuth 上游保留客户端 metadata；关闭时继续剥离不兼容字段。</small></span><input v-model="runtimePolicy.metadata_passthrough" type="checkbox" role="switch" /></label>
						<label class="settings-toggle-row"><span><strong>Claude OAuth System 注入</strong><small>为非 Claude Code 请求补齐订阅凭据需要的 Claude Code 身份块。</small></span><input v-model="runtimePolicy.claude_oauth_system_prompt_injection" type="checkbox" role="switch" /></label>
						<label class="settings-toggle-row"><span><strong>Anthropic 缓存 1h TTL</strong><small>把请求中已有的 ephemeral 缓存断点显式改为 1 小时，不会凭空新增断点。</small></span><input v-model="runtimePolicy.anthropic_cache_ttl_1h_injection" type="checkbox" role="switch" /></label>
						<label class="settings-toggle-row"><span><strong>重写消息缓存断点</strong><small>清理旧断点，并在最后一条消息和倒数第二条用户消息上放置稳定断点。</small></span><input v-model="runtimePolicy.rewrite_message_cache_control" type="checkbox" role="switch" /></label>
					</div>
					<div class="settings-form-grid settings-fields-spaced"><label class="settings-field"><span>Claude System 身份块</span><textarea v-model="runtimePolicy.claude_oauth_system_prompt" rows="3" class="input settings-document-editor__text"></textarea></label><label class="settings-field"><span>Codex OAuth User-Agent</span><input v-model="runtimePolicy.openai_codex_user_agent" class="input" /><small>保存后只影响统一指纹模式的新请求。</small></label></div>
				</section>

				<section class="settings-section">
					<header><h2>客户端版本门禁</h2><p>默认关闭。开启后按真实请求头校验版本与客户端信号，不符合条件的请求在进入调度前直接拒绝。</p></header>
					<div class="settings-toggle-stack"><label class="settings-toggle-row"><span><strong>Claude Code 版本门禁</strong></span><input v-model="runtimePolicy.claude_client_gate_enabled" type="checkbox" role="switch" /></label><label class="settings-toggle-row"><span><strong>Codex CLI 版本与指纹门禁</strong></span><input v-model="runtimePolicy.codex_client_gate_enabled" type="checkbox" role="switch" /></label><label class="settings-toggle-row"><span><strong>允许 Codex app-server</strong></span><input v-model="runtimePolicy.codex_allow_app_server_clients" type="checkbox" role="switch" /></label></div>
					<div class="settings-form-grid settings-form-grid--four settings-fields-spaced"><label class="settings-field"><span>Claude 最低版本</span><input v-model="runtimePolicy.min_claude_code_version" class="input" placeholder="2.1.0" /></label><label class="settings-field"><span>Claude 最高版本</span><input v-model="runtimePolicy.max_claude_code_version" class="input" placeholder="留空不限制" /></label><label class="settings-field"><span>Codex 最低版本</span><input v-model="runtimePolicy.min_codex_version" class="input" placeholder="0.114.0" /></label><label class="settings-field"><span>Codex 最高版本</span><input v-model="runtimePolicy.max_codex_version" class="input" placeholder="留空不限制" /></label></div>
					<div class="settings-form-grid settings-fields-spaced"><label class="settings-field"><span>Codex 指纹黑名单</span><textarea v-model="codexBlacklistText" rows="4" class="input settings-document-editor__text" placeholder="一行一个 UA / Originator 片段"></textarea></label><label class="settings-field"><span>Codex 指纹白名单</span><textarea v-model="codexWhitelistText" rows="4" class="input settings-document-editor__text" placeholder="留空不限制；一行一个片段"></textarea></label></div>
				</section>

          <section class="settings-section">
            <header>
              <h2>思考强度计费 Reasoning Effort Billing</h2>
              <p>按本次请求实际生效的档位计费：客户端显式值优先，其次是密钥默认值；自动 Auto 按模型默认值和 1x 计算。倍率范围 0.1–10。</p>
            </header>
            <div class="settings-form-grid settings-form-grid--three">
              <label v-for="field in reasoningEffortFields" :key="field.value" class="settings-field">
                <span>{{ field.label }}</span>
                <input v-model.number="runtimePolicy.reasoning_effort_multipliers[field.value]" class="input" type="number" min="0.1" max="10" step="0.05" />
                <small>在模型 Token 价格、用户倍率和分组倍率之上叠加。</small>
              </label>
            </div>
          </section>
        </template>

		<template v-else-if="activeSection === 'services'">
			<section class="settings-section">
				<header><h2>业务模块</h2><p>敏感凭据与复杂规则保留在各自页面管理，避免集中设置误覆盖。</p></header>
				<div class="settings-service-grid">
					<router-link to="/admin/payment" class="settings-service-card"><strong>支付中心</strong><span>渠道、订单、对账与记账本</span></router-link>
					<router-link to="/admin/backups" class="settings-service-card"><strong>数据备份</strong><span>定时策略、保留与下载</span></router-link>
					<router-link to="/admin/referrals" class="settings-service-card"><strong>推广与分账</strong><span>推广码、佣金与直接打款</span></router-link>
					<router-link to="/admin/alerts" class="settings-service-card"><strong>告警策略</strong><span>规则、事件、静默与渠道探测</span></router-link>
					<router-link to="/admin/monitoring" class="settings-service-card"><strong>运行中心</strong><span>请求、服务器与错误监控</span></router-link>
					<router-link to="/admin/proxies" class="settings-service-card"><strong>代理配置</strong><span>独立代理池与连通性测试</span></router-link>
				</div>
			</section>
		</template>

        <template v-else-if="activeSection === 'operations'">
          <section class="settings-section">
            <header>
              <h2>账号健康探测</h2>
              <p>探测使用模型列表或 OAuth 的纯传输检查，不发起生成请求，因此不会为了监控消耗上游额度。</p>
            </header>
            <div class="settings-form-grid settings-form-grid--four">
              <label class="settings-field"><span>探测间隔（秒）</span><input v-model.number="runtimePolicy.probe_interval_seconds" class="input" type="number" min="30" max="86400" /></label>
              <label class="settings-field"><span>单次超时（秒）</span><input v-model.number="runtimePolicy.probe_timeout_seconds" class="input" type="number" min="2" max="120" /></label>
              <label class="settings-field"><span>并发数</span><input v-model.number="runtimePolicy.probe_concurrency" class="input" type="number" min="1" max="32" /></label>
              <label class="settings-field"><span>记录保留（天）</span><input v-model.number="runtimePolicy.probe_retention_days" class="input" type="number" min="1" max="365" /></label>
            </div>
          </section>

          <section class="settings-section settings-section--quiet">
            <header class="settings-section__with-control">
              <div>
                <h2>管理员操作记录</h2>
                <p>记录设置和后续敏感操作的操作者、时间与来源地址；不写入令牌、密码或请求正文。</p>
              </div>
              <button type="button" class="btn-ghost !px-3 !py-1.5 text-xs" :disabled="auditLoading" @click="loadAudit">{{ auditLoading ? '刷新中…' : '刷新' }}</button>
            </header>
            <div v-if="auditLoading" class="settings-empty-state">正在读取记录…</div>
            <div v-else-if="!auditItems.length" class="settings-empty-state">暂时没有已记录的管理员操作。</div>
            <div v-else class="settings-audit-list">
              <article v-for="item in auditItems" :key="item.id" class="settings-audit-row">
                <div>
                  <strong>{{ item.action }}</strong>
                  <span>{{ item.detail || '未提供摘要' }}</span>
                </div>
                <small>{{ item.actor_email || '系统' }} · {{ item.source_ip || '—' }} · {{ new Date(item.created_at).toLocaleString() }}</small>
              </article>
            </div>
          </section>
        </template>
      </section>
    </div>
  </div>
</template>
