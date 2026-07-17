<script setup lang="ts">
const props = defineProps<{ page: number; size: number; total: number }>()
const emit = defineEmits<{ change: [page: number] }>()

function totalPages() {
  return Math.max(1, Math.ceil(props.total / props.size))
}
</script>

<template>
  <div class="mt-4 flex items-center justify-between text-xs text-slate-500">
    <span class="num">共 {{ total }} 条</span>
    <div class="flex items-center gap-2">
      <button class="btn-ghost !px-3 !py-1" :disabled="page <= 1" @click="emit('change', page - 1)">上一页</button>
      <span class="num px-2">{{ page }} / {{ totalPages() }}</span>
      <button class="btn-ghost !px-3 !py-1" :disabled="page >= totalPages()" @click="emit('change', page + 1)">下一页</button>
    </div>
  </div>
</template>
