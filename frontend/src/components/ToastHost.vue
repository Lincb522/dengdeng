<script setup lang="ts">
import { useToast } from '../stores/toast'

const toast = useToast()

const kindLabel: Record<string, string> = {
  success: '成功',
  error: '错误',
  info: '提示',
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-host" aria-live="polite" aria-atomic="false">
      <TransitionGroup
        enter-active-class="transition duration-200"
        enter-from-class="translate-x-4 opacity-0"
        leave-active-class="transition duration-200"
        leave-to-class="opacity-0"
      >
        <article
          v-for="t in toast.toasts"
          :key="t.id"
          class="app-toast"
          :class="`is-${t.kind}`"
          :role="t.kind === 'error' ? 'alert' : 'status'"
        >
          <span class="app-toast__state" aria-hidden="true">{{ t.kind === 'success' ? '✓' : t.kind === 'error' ? '!' : 'i' }}</span>
          <div class="app-toast__content">
            <strong v-if="t.title">{{ t.title }}</strong>
            <span v-else class="sr-only">{{ kindLabel[t.kind] }}</span>
            <p>{{ t.message }}</p>
            <small v-if="t.action">{{ t.action }}</small>
            <code v-if="t.requestId">请求编号 {{ t.requestId }}</code>
          </div>
          <button type="button" class="app-toast__close" aria-label="关闭提示" @click="toast.dismiss(t.id)">×</button>
        </article>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
