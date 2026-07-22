<script setup lang="ts">
import { ref } from 'vue'
import QRCode from 'qrcode'
import { api, withToast, setToken } from '../api/client'
import { useAuth } from '../stores/auth'

const auth = useAuth()
const oldPassword = ref('')
const newPassword = ref('')
const confirm = ref('')
const busy = ref(false)
const totpPassword = ref('')
const totpCode = ref('')
const totpSecret = ref('')
const totpQR = ref('')
const totpBusy = ref(false)

async function changePassword() {
  if (newPassword.value !== confirm.value) return
  busy.value = true
  const res = await withToast(
    () => api.post<{ changed: boolean; token: string }>('/api/user/password', { old_password: oldPassword.value, new_password: newPassword.value }),
    '密码已修改',
  )
  busy.value = false
  if (res) {
    // 后端在改密码后换发了新 token(旧 token 已作废),更新本地会话避免掉线。
    setToken(res.token)
    oldPassword.value = newPassword.value = confirm.value = ''
  }
}

async function setupTOTP() {
  totpBusy.value = true
  try {
    const result = await api.post<{ secret: string; otpauth_url: string }>('/api/user/totp/setup', { password: totpPassword.value })
    totpSecret.value = result.secret
    totpQR.value = await QRCode.toDataURL(result.otpauth_url, { width: 220, margin: 1 })
  } finally {
    totpBusy.value = false
  }
}

async function enableTOTP() {
  const result = await withToast(
    () => api.post<{ enabled: boolean; token: string }>('/api/user/totp/enable', { password: totpPassword.value, secret: totpSecret.value, code: totpCode.value }),
    '验证器已开启',
  )
  if (!result) return
  setToken(result.token)
  totpPassword.value = totpCode.value = totpSecret.value = totpQR.value = ''
  await auth.fetchMe()
}

async function disableTOTP() {
  const result = await withToast(
    () => api.post<{ enabled: boolean; token: string }>('/api/user/totp/disable', { password: totpPassword.value, code: totpCode.value }),
    '验证器已关闭',
  )
  if (!result) return
  setToken(result.token)
  totpPassword.value = totpCode.value = ''
  await auth.fetchMe()
}
</script>

<template>
  <div class="max-w-2xl">
    <div class="console-page-head">
      <h1>账户设置</h1>
    </div>

    <div class="card mb-6 p-6">
      <h3 class="mb-4 text-sm font-semibold text-slate-200">基本信息</h3>
      <div class="grid grid-cols-1 gap-4 text-sm sm:grid-cols-2">
        <div>
          <div class="label">邮箱</div>
          <div class="text-slate-200">{{ auth.user?.email }}</div>
        </div>
        <div>
          <div class="label">角色</div>
          <span :class="auth.user?.role === 'admin' ? 'tag-amber' : 'tag-gray'">{{ auth.user?.role }}</span>
        </div>
        <div>
          <div class="label">注册时间</div>
          <div class="text-slate-400">{{ auth.user ? new Date(auth.user.created_at).toLocaleString() : '' }}</div>
        </div>
      </div>
    </div>

    <div class="card mb-6 p-6">
      <h3 class="mb-4 text-sm font-semibold text-slate-200">修改密码</h3>
      <div class="space-y-4">
        <div>
          <label class="label">当前密码</label>
          <input v-model="oldPassword" type="password" class="input" autocomplete="current-password" />
        </div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label class="label">新密码</label>
            <input v-model="newPassword" type="password" class="input" placeholder="至少 8 位" autocomplete="new-password" />
          </div>
          <div>
            <label class="label">确认新密码</label>
            <input v-model="confirm" type="password" class="input" autocomplete="new-password" />
          </div>
        </div>
        <p v-if="confirm && newPassword !== confirm" class="text-xs text-signal-red">两次输入不一致</p>
        <button
          class="btn-primary"
          :disabled="busy || !oldPassword || newPassword.length < 8 || newPassword !== confirm"
          @click="changePassword"
        >
          保存修改
        </button>
      </div>
    </div>

		<div class="card p-6">
			<div class="mb-4 flex items-center justify-between gap-3">
				<div>
					<h3 class="text-sm font-semibold text-slate-200">验证器</h3>
					<p class="mt-1 text-xs text-slate-500">管理员敏感操作会校验当前会话的 TOTP 状态。</p>
				</div>
				<span :class="auth.user?.totp_enabled ? 'tag-green' : 'tag-gray'">{{ auth.user?.totp_enabled ? '已开启' : '未开启' }}</span>
			</div>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<label class="label">当前密码</label>
					<input v-model="totpPassword" type="password" class="input" autocomplete="current-password" />
				</div>
				<div v-if="auth.user?.totp_enabled || totpSecret">
					<label class="label">6 位验证码</label>
					<input v-model="totpCode" inputmode="numeric" maxlength="6" class="input" autocomplete="one-time-code" />
				</div>
			</div>
			<div v-if="totpSecret" class="mt-4 grid items-center gap-4 rounded-xl border border-slate-800 p-4 sm:grid-cols-[auto_1fr]">
				<img :src="totpQR" alt="TOTP QR Code" class="h-36 w-36 rounded-lg bg-white p-1" />
				<div class="min-w-0">
					<div class="label">手动密钥</div>
					<code class="block break-all text-xs text-slate-300">{{ totpSecret }}</code>
					<button class="btn-primary mt-4" :disabled="totpCode.length !== 6" @click="enableTOTP">验证并开启</button>
				</div>
			</div>
			<div v-else class="mt-4">
				<button v-if="!auth.user?.totp_enabled" class="btn-primary" :disabled="totpBusy || !totpPassword" @click="setupTOTP">生成绑定信息</button>
				<button v-else class="btn-danger" :disabled="!totpPassword || totpCode.length !== 6" @click="disableTOTP">关闭验证器</button>
			</div>
		</div>
  </div>
</template>
