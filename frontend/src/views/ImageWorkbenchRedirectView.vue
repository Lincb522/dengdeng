<script setup lang="ts">
import { onMounted } from 'vue'

onMounted(() => {
  window.location.replace('/image-workbench/canvas?mode=recent')
})
</script>

<template>
  <main class="grid min-h-screen place-items-center text-sm text-slate-500">正在打开图像工作台…</main>
</template>
