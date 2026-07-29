<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, setToken } from '../api/client'
import { isAppError } from '../api/errors'
import { useAuth } from '../stores/auth'
import { useToast } from '../stores/toast'
import BrandMark from '../components/BrandMark.vue'
import TurnstileWidget from '../components/TurnstileWidget.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
import InterfaceThemeSwitcher from '../components/InterfaceThemeSwitcher.vue'

const auth = useAuth()
const toast = useToast()
const router = useRouter()

const mode = ref<'login' | 'register' | 'reset'>('login')
const email = ref('')
const password = ref('')
const confirm = ref('')
const verificationCode = ref('')
const totpCode = ref('')
const referralCode = ref(new URLSearchParams(window.location.search).get('ref') || '')
const busy = ref(false)
const sendingCode = ref(false)
const resendAfter = ref(0)
const passwordVisible = ref(false)
const acceptedAgreement = ref(false)
const agreementVisible = ref(false)
const agreementDocumentID = ref('')
const agreementCloseButton = ref<HTMLButtonElement | null>(null)
const agreementDialog = ref<HTMLElement | null>(null)
const agreementBackdropPointerStarted = ref(false)
const turnstileToken = ref('')
const turnstileNonce = ref(0)
const pendingOAuthCode = ref(new URLSearchParams(window.location.search).get('oauth_code') || '')
let cooldownTimer: number | undefined
let agreementReturnFocus: HTMLElement | null = null
let agreementPageLocked = false

const agreement = computed(() => auth.loginAgreement)
const agreementRequired = computed(() => agreement.value.enabled && agreement.value.documents.length > 0)
const canContinue = computed(() => !agreementRequired.value || acceptedAgreement.value)
const turnstileReady = computed(() => !auth.security.turnstile_enabled || !!turnstileToken.value)
const activeAgreementDocument = computed(() => agreement.value.documents.find((item) => item.id === agreementDocumentID.value) || agreement.value.documents[0])
type AgreementBlock = { kind: 'heading' | 'paragraph'; text: string }
const activeAgreementBlocks = computed<AgreementBlock[]>(() => {
  const content = activeAgreementDocument.value?.content_md || ''
  const blocks = content
    .replace(/\r\n?/g, '\n')
    .split(/\n{2,}/)
    .map((raw) => {
      const lines = raw.split('\n').map((line) => line.replace(/^\s*#{1,6}\s*/, '').trimEnd())
      const text = lines.join('\n').trim()
      const markdownHeading = /^\s*#{1,6}\s+/.test(raw)
      const plainHeading = !text.includes('\n') && /^(特别提示|重要提示|附则|[一二三四五六七八九十百]+、)/.test(text)
      return text ? { kind: markdownHeading || plainHeading ? 'heading' as const : 'paragraph' as const, text } : null
    })
    .filter((block): block is AgreementBlock => block !== null)

  if (blocks[0]?.kind === 'heading' && activeAgreementDocument.value && blocks[0].text.includes(activeAgreementDocument.value.title)) {
    blocks.shift()
  }
  return blocks
})

watch(
  () => agreement.value.revision,
  () => {
    acceptedAgreement.value = false
    agreementDocumentID.value = agreement.value.documents[0]?.id || ''
    if (agreementRequired.value && agreement.value.mode === 'modal') agreementVisible.value = true
  },
)
watch(agreementVisible, async (visible) => {
  if (!visible) {
    unlockAgreementPage()
    await nextTick()
    if (agreementReturnFocus?.isConnected) agreementReturnFocus.focus()
    agreementReturnFocus = null
    return
  }
  agreementReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  lockAgreementPage()
  if (!agreementDocumentID.value) agreementDocumentID.value = agreement.value.documents[0]?.id || ''
  await nextTick()
  agreementCloseButton.value?.focus()
})

function lockAgreementPage() {
  if (agreementPageLocked) return
  agreementPageLocked = true
  document.body.dataset.modalOpenCount = String(Number(document.body.dataset.modalOpenCount || 0) + 1)
  document.body.classList.add('has-app-modal')
}

function unlockAgreementPage() {
  if (!agreementPageLocked) return
  agreementPageLocked = false
  const nextCount = Math.max(0, Number(document.body.dataset.modalOpenCount || 1) - 1)
  document.body.dataset.modalOpenCount = String(nextCount)
  if (!nextCount) document.body.classList.remove('has-app-modal')
}

function beginCooldown(seconds: number) {
  resendAfter.value = seconds
  if (cooldownTimer) window.clearInterval(cooldownTimer)
  cooldownTimer = window.setInterval(() => {
    resendAfter.value -= 1
    if (resendAfter.value <= 0 && cooldownTimer) {
      window.clearInterval(cooldownTimer)
      cooldownTimer = undefined
    }
  }, 1000)
}

function requireAgreement(): boolean {
  if (canContinue.value) return true
  if (agreement.value.mode === 'modal') agreementVisible.value = true
  toast.show('请先阅读并同意相关协议', 'error')
  return false
}

function acceptAgreement() {
  acceptedAgreement.value = true
  agreementVisible.value = false
}

function rejectAgreement() {
  acceptedAgreement.value = false
  agreementVisible.value = false
}

function handleAgreementPointerDown(event: PointerEvent) {
  agreementBackdropPointerStarted.value = event.target === event.currentTarget
}

function handleAgreementPointerUp(event: PointerEvent) {
  const shouldClose = agreementBackdropPointerStarted.value && event.target === event.currentTarget
  agreementBackdropPointerStarted.value = false
  if (shouldClose) rejectAgreement()
}

function handleAgreementKeydown(event: KeyboardEvent) {
  if (!agreementVisible.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    rejectAgreement()
    return
  }
  if (event.key !== 'Tab' || !agreementDialog.value) return
  const focusable = [...agreementDialog.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )].filter((element) => element.offsetParent !== null)
  if (!focusable.length) {
    event.preventDefault()
    agreementDialog.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

async function sendVerificationCode() {
  if (!requireAgreement()) return
  if (!email.value) {
    toast.show('请先填写邮箱', 'error')
    return
  }
  sendingCode.value = true
  try {
		const result = await api.post<{ resend_after: number }>('/api/auth/register/code', { email: email.value.trim(), turnstile_token: turnstileToken.value })
    beginCooldown(result.resend_after || 60)
    toast.show('验证码已发送', 'success')
  } catch (e) {
    toast.showError(e, '验证码发送失败')
  } finally {
    sendingCode.value = false
		turnstileToken.value = ''
		turnstileNonce.value += 1
  }
}

async function sendResetCode() {
	if (!email.value) {
		toast.show('请先填写邮箱', 'error')
		return
	}
	sendingCode.value = true
	try {
		const result = await api.post<{ resend_after: number }>('/api/auth/password-reset/code', { email: email.value.trim(), turnstile_token: turnstileToken.value })
		beginCooldown(result.resend_after || 60)
		toast.show('验证码已发送', 'success')
	} catch (e) {
		toast.showError(e, '验证码发送失败')
	} finally {
		sendingCode.value = false
		turnstileToken.value = ''
		turnstileNonce.value += 1
	}
}

onMounted(async () => {
  window.addEventListener('keydown', handleAgreementKeydown)
  await auth.loadPublicSettings()
	const oauthError = new URLSearchParams(window.location.search).get('oauth_error')
	if (oauthError) toast.show(oauthError, 'error')
	if (pendingOAuthCode.value) await completeOAuth()
  if (agreementRequired.value && agreement.value.mode === 'modal') agreementVisible.value = true
})

async function startOAuth(provider: string) {
	if (!requireAgreement() || !turnstileReady.value) return
	busy.value = true
	try {
		const result = await api.post<{ authorization_url: string }>(`/api/auth/oauth/${provider}/start`, { terms_revision: agreement.value.revision, turnstile_token: turnstileToken.value })
		window.location.assign(result.authorization_url)
	} catch (e) {
		toast.showError(e, '第三方登录失败')
		busy.value = false
		turnstileToken.value = ''
		turnstileNonce.value += 1
	}
}

async function completeOAuth() {
	if (!pendingOAuthCode.value) return
	busy.value = true
	try {
		const result = await api.post<{ token: string }>('/api/auth/oauth/exchange', { code: pendingOAuthCode.value, totp_code: totpCode.value.trim() })
		setToken(result.token)
		await auth.fetchMe()
		pendingOAuthCode.value = ''
		await router.push('/dashboard')
	} catch (e) {
		toast.showError(e, '第三方登录确认失败')
	} finally {
		busy.value = false
	}
}

onBeforeUnmount(() => {
  if (cooldownTimer) window.clearInterval(cooldownTimer)
  window.removeEventListener('keydown', handleAgreementKeydown)
  unlockAgreementPage()
})

async function submit() {
	if (!email.value || !password.value || (mode.value !== 'reset' && !requireAgreement())) return
	if (mode.value === 'register' || mode.value === 'reset') {
    if (password.value.length < 8) {
      toast.show('密码至少 8 位', 'error')
      return
    }
    if (password.value !== confirm.value) {
      toast.show('两次输入的密码不一致', 'error')
      return
    }
		if ((mode.value === 'reset' || auth.registrationVerification) && !/^\d{6}$/.test(verificationCode.value.trim())) {
      toast.show('请输入 6 位邮箱验证码', 'error')
      return
    }
  }
  busy.value = true
  try {
		if (mode.value === 'login') {
			await auth.login(email.value, password.value, agreement.value.revision, totpCode.value.trim(), turnstileToken.value)
		} else if (mode.value === 'register') {
			await auth.register(email.value, password.value, verificationCode.value.trim(), agreement.value.revision, referralCode.value.trim(), turnstileToken.value)
		} else {
			await api.post('/api/auth/password-reset', { email: email.value.trim(), code: verificationCode.value.trim(), password: password.value, turnstile_token: turnstileToken.value })
			mode.value = 'login'
			password.value = ''
			confirm.value = ''
			verificationCode.value = ''
			toast.show('密码已重置，请重新登录', 'success')
			return
    }
    router.push('/dashboard')
  } catch (e) {
    if (mode.value === 'login' && isAppError(e) && e.code === 'auth.terms_required') {
      await auth.loadPublicSettings()
      acceptedAgreement.value = false
      agreementVisible.value = agreementRequired.value
    }
    toast.showError(e, mode.value === 'login' ? '登录失败' : mode.value === 'register' ? '注册失败' : '密码重置失败')
  } finally {
    busy.value = false
		turnstileToken.value = ''
		turnstileNonce.value += 1
  }
}
</script>

<template>
  <div class="login-shell">
    <main class="login-frame login-frame--simple">
      <aside class="login-visual">
        <div class="login-visual-brand">
          <BrandMark :size="54" />
          <div><strong>{{ auth.siteName }}</strong><span>蹬蹬ai</span></div>
        </div>
        <div class="login-visual-stage" aria-hidden="true">
          <span class="login-visual-node is-openai">OpenAI</span>
          <span class="login-visual-node is-claude">Claude</span>
          <span class="login-visual-node is-gemini">Gemini</span>
          <span class="login-visual-node is-image">Image</span>
          <span class="login-visual-route is-route-a"></span>
          <span class="login-visual-route is-route-b"></span>
          <span class="login-visual-center"><BrandMark :size="40" /></span>
        </div>
        <p class="login-visual-foot">API · CLI · IMAGE</p>
      </aside>

      <section class="login-panel" aria-labelledby="login-title">
        <div class="login-brand-lockup login-brand-lockup--mobile" :aria-label="auth.siteName">
          <BrandMark :size="42" />
          <div>
            <strong>{{ auth.siteName }}</strong>
            <span>蹬蹬ai</span>
          </div>
        </div>

        <header class="login-panel-header">
          <h1 id="login-title">{{ mode === 'login' ? '欢迎回来' : mode === 'register' ? '创建账户' : '重置密码' }}</h1>
        </header>

        <div class="login-tabs" :class="{ 'login-tabs--single': !auth.allowRegister }" role="tablist" aria-label="账户操作">
          <button type="button" role="tab" :aria-selected="mode === 'login' || mode === 'reset'" :class="{ 'is-active': mode === 'login' || mode === 'reset' }" @click="mode = 'login'">登录</button>
          <button v-if="auth.allowRegister" type="button" role="tab" :aria-selected="mode === 'register'" :class="{ 'is-active': mode === 'register' }" @click="mode = 'register'">注册</button>
        </div>

		<div v-if="auth.oauthProviders.length && mode === 'login'" class="login-oauth-grid">
			<button v-for="provider in auth.oauthProviders" :key="provider.id" type="button" :disabled="busy || !canContinue || !turnstileReady" @click="startOAuth(provider.id)">{{ provider.name }}</button>
		</div>
        <div v-if="auth.oauthProviders.length && mode === 'login'" class="login-divider"><span>或使用邮箱</span></div>

        <form class="login-form" @submit.prevent="submit">
          <div class="login-field">
            <label for="login-email">邮箱</label>
            <input id="login-email" v-model="email" type="email" placeholder="you@example.com" autocomplete="email" />
          </div>

			<div v-if="(mode === 'register' && auth.registrationVerification) || mode === 'reset'" class="login-field">
            <label for="verification-code">邮箱验证码</label>
            <div class="login-code-row">
              <input id="verification-code" v-model="verificationCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="6 位数字" />
				<button type="button" class="login-code-button" :disabled="sendingCode || resendAfter > 0 || !turnstileReady || (mode !== 'reset' && !canContinue)" @click="mode === 'reset' ? sendResetCode() : sendVerificationCode()">
                {{ sendingCode ? '发送中' : resendAfter > 0 ? `${resendAfter}s 后重发` : '发送验证码' }}
              </button>
            </div>
          </div>

          <div class="login-field">
            <label for="login-password">密码</label>
            <div class="login-password-wrap">
              <input id="login-password" v-model="password" :type="passwordVisible ? 'text' : 'password'" placeholder="至少 8 位" :autocomplete="mode === 'login' ? 'current-password' : 'new-password'" />
              <button type="button" class="login-password-toggle" :aria-label="passwordVisible ? '隐藏密码' : '显示密码'" @click="passwordVisible = !passwordVisible">
                {{ passwordVisible ? '隐藏' : '显示' }}
              </button>
            </div>
          </div>

					<div v-if="mode === 'login'" class="login-field">
						<label for="totp-code">验证器验证码</label>
						<input id="totp-code" v-model="totpCode" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="未开启时留空" />
					</div>
			<button v-if="pendingOAuthCode" type="button" class="login-submit" :disabled="busy" @click="completeOAuth">完成第三方登录</button>

			<div v-if="mode === 'register' || mode === 'reset'" class="login-field">
            <label for="confirm-password">确认密码</label>
            <input id="confirm-password" v-model="confirm" type="password" placeholder="再输入一次" autocomplete="new-password" />
          </div>

          <div v-if="mode === 'register'" class="login-field">
            <label for="referral-code">推广码（选填）</label>
            <input id="referral-code" v-model="referralCode" type="text" placeholder="例如 DD-XXXXXXXXXX" autocomplete="off" maxlength="32" />
          </div>

			<TurnstileWidget v-if="auth.security.turnstile_enabled && auth.security.turnstile_site_key" :key="turnstileNonce" v-model="turnstileToken" :site-key="auth.security.turnstile_site_key" />

			<button type="submit" class="login-submit" :disabled="busy || !turnstileReady || (mode !== 'reset' && !canContinue)">
			{{ busy ? '请稍候…' : mode === 'login' ? '进入控制台' : mode === 'register' ? '创建账户' : '确认重置' }}
          </button>
			<button v-if="mode === 'login' && auth.security.password_reset_enabled" type="button" class="login-agreement-open" @click="mode = 'reset'; verificationCode = ''; confirm = ''">忘记密码</button>
			<button v-else-if="mode === 'reset'" type="button" class="login-agreement-open" @click="mode = 'login'">返回登录</button>

			<div v-if="mode !== 'reset' && agreementRequired && agreement.mode === 'checkbox'" class="login-agreement-checkbox">
            <input id="login-agreement-consent" v-model="acceptedAgreement" type="checkbox" />
            <label for="login-agreement-consent">
              我已阅读并同意
              <template v-for="(doc, index) in agreement.documents" :key="doc.id">
                <RouterLink :to="`/legal/${doc.id}`" target="_blank" rel="noopener">{{ doc.title }}</RouterLink><span v-if="index < agreement.documents.length - 1">、</span>
              </template>
            </label>
          </div>
			<button v-else-if="mode !== 'reset' && agreementRequired" type="button" class="login-agreement-open" @click="agreementVisible = true">查看并同意服务协议</button>
        </form>
      </section>
    </main>
	<ThemeToggle class="theme-toggle-float" />
	<InterfaceThemeSwitcher class="interface-theme-switcher-float" />

    <Teleport to="body">
      <div
        v-if="agreementVisible && agreementRequired"
        class="agreement-backdrop"
        role="presentation"
        @pointercancel="agreementBackdropPointerStarted = false"
        @pointerdown="handleAgreementPointerDown"
        @pointerup="handleAgreementPointerUp"
      >
        <section ref="agreementDialog" class="agreement-dialog" role="dialog" aria-modal="true" aria-labelledby="agreement-title" tabindex="-1">
          <header class="agreement-dialog__head">
            <div class="agreement-dialog__brand">
              <BrandMark :size="32" />
              <div>
                <h2 id="agreement-title">服务协议</h2>
                <p>{{ agreement.updated_at ? `更新日期 ${agreement.updated_at}` : '请阅读后继续' }}</p>
              </div>
            </div>
            <button ref="agreementCloseButton" type="button" class="agreement-dialog__close" aria-label="关闭协议" @click="rejectAgreement">×</button>
          </header>
          <div class="agreement-dialog__workspace">
            <nav class="agreement-dialog__nav" aria-label="协议目录">
              <button
                v-for="doc in agreement.documents"
                :key="doc.id"
                type="button"
                :class="{ 'is-active': activeAgreementDocument?.id === doc.id }"
                :aria-current="activeAgreementDocument?.id === doc.id ? 'page' : undefined"
                @click="agreementDocumentID = doc.id"
              >
                <span>{{ doc.title }}</span><i aria-hidden="true">›</i>
              </button>
            </nav>
            <article v-if="activeAgreementDocument" class="agreement-dialog__document">
              <header>
                <h3>{{ activeAgreementDocument.title }}</h3>
                <RouterLink :to="`/legal/${activeAgreementDocument.id}`" target="_blank" rel="noopener">独立打开 ↗</RouterLink>
              </header>
              <div class="agreement-dialog__content">
                <template v-for="(block, index) in activeAgreementBlocks" :key="`${block.kind}-${index}`">
                  <h4 v-if="block.kind === 'heading'">{{ block.text }}</h4>
                  <p v-else>{{ block.text }}</p>
                </template>
              </div>
            </article>
          </div>
          <footer class="agreement-dialog__actions">
            <p>同意后可继续登录或注册。</p>
            <div>
              <button type="button" class="agreement-reject" @click="rejectAgreement">退出</button>
              <button type="button" class="agreement-accept" @click="acceptAgreement">同意并继续</button>
            </div>
          </footer>
        </section>
      </div>
    </Teleport>
  </div>
</template>
