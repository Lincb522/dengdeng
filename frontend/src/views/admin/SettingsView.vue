<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { AuditLog, GatewayRuntimePolicy, LegalDocument, SystemSettings } from '../../api/types'
import { defaultReasoningMultipliers, REASONING_OPTIONS } from '../../api/reasoning'

type SettingsPayload = SystemSettings & {
  site_public_url?: string
  smtp_configured?: boolean
  smtp_from_name?: string
  smtp_from?: string
}

const sections = [
  { id: 'general', label: '站点信息', hint: '名称、说明与运行状态' },
  { id: 'access', label: '注册与账户', hint: '注册开关与初始额度' },
  { id: 'agreement', label: '协议与免责', hint: '登录页确认与文档内容' },
  { id: 'gateway', label: '网关与调度', hint: '重试、冷却与故障隔离' },
  { id: 'operations', label: '运行与审计', hint: '健康探测与操作记录' },
] as const

const activeSection = ref<(typeof sections)[number]['id']>('general')
const loading = ref(true)
const saving = ref(false)
const auditLoading = ref(false)
const runtime = ref<Pick<SettingsPayload, 'site_public_url' | 'smtp_configured' | 'smtp_from_name' | 'smtp_from'>>({})
const registrationSuffixesText = ref('')
const form = ref<SystemSettings>({
  site_name: 'DengDeng AI · 蹬蹬ai',
  site_subtitle: '统一管理模型接入与用量',
  allow_register: true,
	registration_email_suffixes: [],
  init_balance_micro: 0,
  login_agreement: { enabled: true, mode: 'modal', updated_at: '', documents: [] },
})
const reasoningEffortFields = REASONING_OPTIONS.filter((item) => item.value !== 'auto')
const runtimePolicy = ref<GatewayRuntimePolicy>({
  max_attempts: 3,
  unauthorized_cooldown_seconds: 600,
  rate_limit_cooldown_seconds: 60,
  upstream_failure_cooldown_seconds: 30,
  network_failure_cooldown_seconds: 15,
  probe_interval_seconds: 300,
  probe_timeout_seconds: 12,
  probe_retention_days: 30,
  probe_concurrency: 4,
	concurrency_wait_milliseconds: 5000,
	concurrency_queue_depth: 256,
  reasoning_effort_multipliers: defaultReasoningMultipliers(),
})
const auditItems = ref<AuditLog[]>([])

const initialBalanceUSD = computed({
  get: () => form.value.init_balance_micro / 1_000_000,
  set: (value: number | string) => {
    const amount = Number(value)
    form.value.init_balance_micro = Number.isFinite(amount) && amount >= 0 ? Math.round(amount * 1_000_000) : 0
  },
})

async function load() {
  loading.value = true
  try {
    const [data, policy] = await Promise.all([
      api.get<SettingsPayload>('/api/admin/settings'),
      api.get<GatewayRuntimePolicy>('/api/admin/runtime-settings'),
    ])
    form.value = {
      site_name: data.site_name,
      site_subtitle: data.site_subtitle,
      allow_register: data.allow_register,
			registration_email_suffixes: data.registration_email_suffixes || [],
      init_balance_micro: data.init_balance_micro,
      login_agreement: {
        enabled: data.login_agreement.enabled,
        mode: data.login_agreement.mode === 'checkbox' ? 'checkbox' : 'modal',
        updated_at: data.login_agreement.updated_at,
        documents: data.login_agreement.documents.map((item) => ({ ...item })),
      },
    }
    runtime.value = {
      site_public_url: data.site_public_url,
      smtp_configured: data.smtp_configured,
      smtp_from_name: data.smtp_from_name,
      smtp_from: data.smtp_from,
    }
		registrationSuffixesText.value = (data.registration_email_suffixes || []).join('\n')
    runtimePolicy.value = { ...policy, reasoning_effort_multipliers: { ...defaultReasoningMultipliers(), ...(policy.reasoning_effort_multipliers || {}) } }
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
    if (activeSection.value === 'gateway' || activeSection.value === 'operations') {
      const saved = await withToast(() => api.put<GatewayRuntimePolicy>('/api/admin/runtime-settings', runtimePolicy.value), '网关运行策略已保存')
      if (saved) {
        runtimePolicy.value = saved
        await loadAudit()
      }
    } else {
			form.value.registration_email_suffixes = registrationSuffixesText.value.split(/[\n,;\s]+/).map((item) => item.trim()).filter(Boolean)
      const saved = await withToast(() => api.put<SystemSettings>('/api/admin/settings', form.value), '系统设置已保存')
      if (saved) {
        form.value = saved
        await load()
      }
    }
  } finally {
    saving.value = false
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
        </template>

        <template v-else-if="activeSection === 'access'">
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
              <h2>新用户初始额度</h2>
              <p>只对之后创建的账户生效，不会改动已有用户的余额。</p>
            </header>
            <label class="settings-field settings-field--compact">
              <span>初始余额（USD）</span>
              <input v-model.number="initialBalanceUSD" type="number" min="0" step="0.01" class="input" />
            </label>
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
                <p>内容会以安全的纯文本格式呈现在独立页面。默认包含服务条款、隐私、使用政策、服务特定条款与免责声明。</p>
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
                <label class="settings-field"><span>正文</span><textarea v-model="doc.content_md" rows="9" class="input settings-document-editor__text" placeholder="支持普通文本与 Markdown 结构。"></textarea></label>
              </article>
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

        <template v-else>
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
