<script setup lang="ts">
import { ref } from 'vue'
import { api, withToast, setToken } from '../api/client'
import { useAuth } from '../stores/auth'

const auth = useAuth()
const oldPassword = ref('')
const newPassword = ref('')
const confirm = ref('')
const busy = ref(false)

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
</script>

<template>
  <div class="max-w-2xl">
    <div class="console-page-head">
      <h1>账户设置</h1>
    </div>

    <div class="card mb-6 p-6">
      <h3 class="mb-4 text-sm font-semibold text-slate-200">基本信息</h3>
      <div class="grid grid-cols-2 gap-4 text-sm">
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

    <div class="card p-6">
      <h3 class="mb-4 text-sm font-semibold text-slate-200">修改密码</h3>
      <div class="space-y-4">
        <div>
          <label class="label">当前密码</label>
          <input v-model="oldPassword" type="password" class="input" autocomplete="current-password" />
        </div>
        <div class="grid grid-cols-2 gap-4">
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
  </div>
</template>
