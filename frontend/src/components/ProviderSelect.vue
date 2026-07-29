<script setup lang="ts">
import { computed } from 'vue'
import { PLATFORM_LABELS } from '../api/types'
import ProviderLogo from './ProviderLogo.vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  modelValue: string
  platforms?: string[]
  includeAll?: boolean
  allLabel?: string
  disabled?: boolean
}>(), {
  platforms: () => ['openai', 'anthropic', 'gemini', 'grok'],
  includeAll: false,
  allLabel: '全部平台',
  disabled: false,
})

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const selectedLabel = computed(() => props.modelValue ? (PLATFORM_LABELS[props.modelValue] || props.modelValue) : props.allLabel)

function update(event: Event) {
  emit('update:modelValue', (event.target as HTMLSelectElement).value)
}
</script>

<template>
  <div class="provider-select">
    <ProviderLogo v-if="modelValue" :platform="modelValue" size="sm" />
    <span v-else class="provider-select__all" aria-hidden="true">ALL</span>
    <select
      v-bind="$attrs"
      class="input provider-select__control"
      :value="modelValue"
      :disabled="disabled"
      :aria-label="String($attrs['aria-label'] || selectedLabel)"
      @change="update"
    >
      <option v-if="includeAll" value="">{{ allLabel }}</option>
      <option v-for="item in platforms" :key="item" :value="item">{{ PLATFORM_LABELS[item] || item }}</option>
    </select>
  </div>
</template>
