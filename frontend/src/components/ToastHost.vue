<script setup lang="ts">
import { useToast } from '../stores/toast'

const toast = useToast()

const kindClass: Record<string, string> = {
  success: 'border-signal-green/40 text-signal-green',
  error: 'border-signal-red/40 text-signal-red',
  info: 'border-amber/40 text-amber',
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed right-4 top-4 z-[100] flex flex-col gap-2">
      <TransitionGroup
        enter-active-class="transition duration-200"
        enter-from-class="translate-x-4 opacity-0"
        leave-active-class="transition duration-200"
        leave-to-class="opacity-0"
      >
        <div
          v-for="t in toast.toasts"
          :key="t.id"
          class="min-w-[220px] max-w-sm rounded-lg border bg-ink-850/95 px-4 py-3 text-sm shadow-xl backdrop-blur"
          :class="kindClass[t.kind]"
        >
          {{ t.message }}
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>
