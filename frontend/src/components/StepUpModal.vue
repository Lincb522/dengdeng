<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, setToken } from '../api/client'
import { useToast } from '../stores/toast'
import AppModal from './AppModal.vue'

const props = defineProps<{
  open: boolean
  totpEnabled: boolean
}>()

const emit = defineEmits<{
  close: []
  verified: []
}>()

const toast = useToast()
const password = ref('')
const code = ref('')
const busy = ref(false)

watch(() => props.open, (open) => {
  if (!open) {
    password.value = ''
    code.value = ''
  }
})

async function verify() {
  if (!password.value || (props.totpEnabled && code.value.length !== 6) || busy.value) return
  busy.value = true
  try {
    const result = await api.post<{ token: string }>('/api/user/step-up', {
      password: password.value,
      code: props.totpEnabled ? code.value : '',
    })
    setToken(result.token)
    emit('verified')
  } catch (error) {
    toast.showError(error, '验证失败，请检查密码和验证码')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AppModal
    :open="open"
    title="确认本人操作"
    description="验证通过后，15 分钟内可以继续敏感操作。"
    width="simple"
    :busy="busy"
    initial-focus="input"
    @close="emit('close')"
  >
    <form class="modal-form" @submit.prevent="verify">
      <label class="modal-field">
        <span class="label">当前密码</span>
        <input
          v-model="password"
          type="password"
          class="input"
          autocomplete="current-password"
          required
        />
      </label>
      <label v-if="totpEnabled" class="modal-field">
        <span class="label">身份验证器验证码</span>
        <input
          v-model.trim="code"
          class="input"
          inputmode="numeric"
          pattern="[0-9]{6}"
          maxlength="6"
          autocomplete="one-time-code"
          required
        />
      </label>
    </form>
    <template #footer>
      <button type="button" class="btn-ghost" :disabled="busy" @click="emit('close')">取消</button>
      <button
        type="button"
        class="btn-primary"
        :disabled="busy || !password || (totpEnabled && code.length !== 6)"
        @click="verify"
      >
        {{ busy ? '验证中…' : '确认' }}
      </button>
    </template>
  </AppModal>
</template>
