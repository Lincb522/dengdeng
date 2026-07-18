<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import { useAuth } from '../stores/auth'
import { useToast } from '../stores/toast'
import BrandMark from '../components/BrandMark.vue'

const auth = useAuth()
const toast = useToast()
const router = useRouter()

const mode = ref<'login' | 'register'>('login')
const email = ref('')
const password = ref('')
const confirm = ref('')
const verificationCode = ref('')
const referralCode = ref(new URLSearchParams(window.location.search).get('ref') || '')
const busy = ref(false)
const sendingCode = ref(false)
const resendAfter = ref(0)
const passwordVisible = ref(false)
const acceptedAgreement = ref(false)
const agreementVisible = ref(false)
let cooldownTimer: number | undefined

const agreement = computed(() => auth.loginAgreement)
const agreementRequired = computed(() => agreement.value.enabled && agreement.value.documents.length > 0)
const canContinue = computed(() => !agreementRequired.value || acceptedAgreement.value)

watch(
  () => agreement.value.revision,
  () => {
    acceptedAgreement.value = false
    if (agreementRequired.value && agreement.value.mode === 'modal') agreementVisible.value = true
  },
)

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

async function sendVerificationCode() {
  if (!requireAgreement()) return
  if (!email.value) {
    toast.show('请先填写邮箱', 'error')
    return
  }
  sendingCode.value = true
  try {
    const result = await api.post<{ resend_after: number }>('/api/auth/register/code', { email: email.value.trim() })
    beginCooldown(result.resend_after || 60)
    toast.show('验证码已发送', 'success')
  } catch (e) {
    toast.show(e instanceof Error ? e.message : '发送失败', 'error')
  } finally {
    sendingCode.value = false
  }
}

onMounted(async () => {
  await auth.loadPublicSettings()
  if (agreementRequired.value && agreement.value.mode === 'modal') agreementVisible.value = true
})

onBeforeUnmount(() => {
  if (cooldownTimer) window.clearInterval(cooldownTimer)
})

async function submit() {
  if (!email.value || !password.value || !requireAgreement()) return
  if (mode.value === 'register') {
    if (password.value.length < 8) {
      toast.show('密码至少 8 位', 'error')
      return
    }
    if (password.value !== confirm.value) {
      toast.show('两次输入的密码不一致', 'error')
      return
    }
    if (auth.registrationVerification && !/^\d{6}$/.test(verificationCode.value.trim())) {
      toast.show('请输入 6 位邮箱验证码', 'error')
      return
    }
  }
  busy.value = true
  try {
    if (mode.value === 'login') {
      await auth.login(email.value, password.value, agreement.value.revision)
    } else {
      await auth.register(email.value, password.value, verificationCode.value.trim(), agreement.value.revision, referralCode.value.trim())
    }
    router.push('/dashboard')
  } catch (e) {
    toast.show(e instanceof Error ? e.message : '操作失败', 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-shell">
    <main class="login-frame login-frame--simple">
      <section class="login-panel" aria-labelledby="login-title">
        <div class="login-brand-lockup" :aria-label="auth.siteName">
          <BrandMark :size="42" />
          <div>
            <strong>{{ auth.siteName }}</strong>
            <span>蹬蹬ai</span>
          </div>
        </div>

        <header class="login-panel-header">
          <h1 id="login-title">{{ mode === 'login' ? '欢迎回来' : '创建账户' }}</h1>
          <p>{{ mode === 'login' ? '使用你的邮箱继续' : '验证邮箱后即可使用' }}</p>
        </header>

        <div class="login-tabs" :class="{ 'login-tabs--single': !auth.allowRegister }" role="tablist" aria-label="账户操作">
          <button type="button" role="tab" :aria-selected="mode === 'login'" :class="{ 'is-active': mode === 'login' }" @click="mode = 'login'">登录</button>
          <button v-if="auth.allowRegister" type="button" role="tab" :aria-selected="mode === 'register'" :class="{ 'is-active': mode === 'register' }" @click="mode = 'register'">注册</button>
        </div>

        <form class="login-form" @submit.prevent="submit">
          <div class="login-field">
            <label for="login-email">邮箱</label>
            <input id="login-email" v-model="email" type="email" placeholder="you@example.com" autocomplete="email" />
          </div>

          <div v-if="mode === 'register' && auth.registrationVerification" class="login-field">
            <label for="verification-code">邮箱验证码</label>
            <div class="login-code-row">
              <input id="verification-code" v-model="verificationCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="6 位数字" />
              <button type="button" class="login-code-button" :disabled="sendingCode || resendAfter > 0 || !canContinue" @click="sendVerificationCode">
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

          <div v-if="mode === 'register'" class="login-field">
            <label for="confirm-password">确认密码</label>
            <input id="confirm-password" v-model="confirm" type="password" placeholder="再输入一次" autocomplete="new-password" />
          </div>

          <div v-if="mode === 'register'" class="login-field">
            <label for="referral-code">推广码（选填）</label>
            <input id="referral-code" v-model="referralCode" type="text" placeholder="例如 DD-XXXXXXXXXX" autocomplete="off" maxlength="32" />
          </div>

          <button type="submit" class="login-submit" :disabled="busy || !canContinue">
            {{ busy ? '请稍候…' : mode === 'login' ? '进入控制台' : '创建账户' }}
          </button>

          <div v-if="agreementRequired && agreement.mode === 'checkbox'" class="login-agreement-checkbox">
            <input id="login-agreement-consent" v-model="acceptedAgreement" type="checkbox" />
            <label for="login-agreement-consent">
              我已阅读并同意
              <template v-for="(doc, index) in agreement.documents" :key="doc.id">
                <RouterLink :to="`/legal/${doc.id}`" target="_blank" rel="noopener">{{ doc.title }}</RouterLink><span v-if="index < agreement.documents.length - 1">、</span>
              </template>
            </label>
          </div>
          <button v-else-if="agreementRequired" type="button" class="login-agreement-open" @click="agreementVisible = true">查看并同意服务协议</button>
        </form>
      </section>
    </main>

    <Teleport to="body">
      <div v-if="agreementVisible && agreementRequired" class="agreement-backdrop" role="presentation">
        <section class="agreement-dialog" role="dialog" aria-modal="true" aria-labelledby="agreement-title">
          <header class="agreement-dialog__head">
            <div class="agreement-dialog__seal" aria-hidden="true">✓</div>
            <div>
              <h2 id="agreement-title">使用前请确认</h2>
              <p v-if="agreement.updated_at">条款更新于 {{ agreement.updated_at }}</p>
              <p v-else>请阅读以下协议后继续</p>
            </div>
          </header>
          <div class="agreement-dialog__body">
            <p class="agreement-dialog__intro">继续登录或注册，即表示你已了解服务边界与使用责任。</p>
            <RouterLink v-for="doc in agreement.documents" :key="doc.id" :to="`/legal/${doc.id}`" target="_blank" rel="noopener" class="agreement-document-link">
              <span>{{ doc.title }}</span><span aria-hidden="true">↗</span>
            </RouterLink>
          </div>
          <footer class="agreement-dialog__actions">
            <button type="button" class="agreement-reject" @click="rejectAgreement">暂不继续</button>
            <button type="button" class="agreement-accept" @click="acceptAgreement">已阅读，同意继续</button>
          </footer>
        </section>
      </div>
    </Teleport>
  </div>
</template>
