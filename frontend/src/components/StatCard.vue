<script setup lang="ts">
defineProps<{
  label: string
  value: string
  sub?: string
  accent?: 'amber' | 'green' | 'cyan' | 'none'
}>()

const accentClass: Record<string, string> = {
  amber: 'text-amber',
  green: 'text-signal-green',
  cyan: 'text-signal-cyan',
  none: 'text-slate-100',
}
</script>

<template>
  <div class="card metric-card relative overflow-hidden p-5">
    <div class="absolute right-5 top-5 h-2 w-2 rounded-full" :class="accent === 'green' ? 'bg-signal-green' : accent === 'cyan' ? 'bg-signal-cyan' : 'bg-amber'"></div>
    <div class="text-[11px] font-semibold tracking-wide text-slate-500">{{ label }}</div>
    <div class="num mt-3 text-[1.7rem] font-bold tracking-[-0.04em]" :class="accentClass[accent || 'none']">{{ value }}</div>
    <div v-if="sub" class="mt-2 text-xs text-slate-500">{{ sub }}</div>
  </div>
</template>
