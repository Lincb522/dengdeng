<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ siteKey: string; modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const container = ref<HTMLElement | null>(null)
let widgetID: string | number | undefined

type TurnstileAPI = {
	render: (target: HTMLElement, options: Record<string, unknown>) => string | number
	remove: (id: string | number) => void
}

declare global {
	interface Window { turnstile?: TurnstileAPI }
}

function renderWidget() {
	if (!container.value || !props.siteKey || !window.turnstile || widgetID !== undefined) return
	widgetID = window.turnstile.render(container.value, {
		sitekey: props.siteKey,
		theme: document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light',
		callback: (token: string) => emit('update:modelValue', token),
		'expired-callback': () => emit('update:modelValue', ''),
		'error-callback': () => emit('update:modelValue', ''),
	})
}

async function load() {
	await nextTick()
	if (window.turnstile) {
		renderWidget()
		return
	}
	let script = document.querySelector<HTMLScriptElement>('script[data-dd-turnstile]')
	if (!script) {
		script = document.createElement('script')
		script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
		script.async = true
		script.defer = true
		script.dataset.ddTurnstile = '1'
		document.head.appendChild(script)
	}
	script.addEventListener('load', renderWidget, { once: true })
}

function removeWidget() {
	if (widgetID !== undefined && window.turnstile) window.turnstile.remove(widgetID)
	widgetID = undefined
	emit('update:modelValue', '')
}

watch(() => props.siteKey, () => { removeWidget(); void load() })
onMounted(load)
onBeforeUnmount(removeWidget)
</script>

<template><div ref="container" class="turnstile-widget" aria-label="人机验证"></div></template>
