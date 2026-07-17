<script setup lang="ts">
defineProps<{ size?: number }>()
</script>

<template>
  <svg :width="size || 32" :height="size || 32" viewBox="0 0 64 64" fill="none">
    <rect width="64" height="64" rx="18" fill="#30261E" />
    <circle cx="48" cy="16" r="9" fill="#E8B35B" opacity=".9" />
    <path
      d="M18 44 L32 16 L46 44"
      stroke="#FFF8EB"
      stroke-width="6"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <circle cx="32" cy="38" r="4" fill="#E8B35B" />
  </svg>
</template>
