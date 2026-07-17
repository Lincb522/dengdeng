<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { PaymentCheckout } from '../../api/types'

const props = defineProps<{ checkout: PaymentCheckout; currency: string }>()
const emit = defineEmits<{ error: [message: string] }>()
const loading = ref(true)

onMounted(async () => {
  try {
    if (!props.checkout.intent_id || !props.checkout.client_secret) throw new Error('Airwallex 订单缺少支付参数')
    const airwallex = await import('@airwallex/components-sdk')
    const sdk = await airwallex.init({ env: props.checkout.payment_env === 'prod' ? 'prod' : 'demo', enabledElements: ['payments'], locale: navigator.language.startsWith('zh') ? 'zh' : 'en' })
    if (!sdk.payments) throw new Error('Airwallex 收银台加载失败')
    const redirect = sdk.payments.redirectToCheckout({ intent_id: props.checkout.intent_id, client_secret: props.checkout.client_secret, currency: props.currency, country_code: props.checkout.country_code || 'CN', successUrl: `${window.location.origin}/wallet` })
    if (typeof redirect === 'string' && redirect) window.location.assign(redirect)
  } catch (error) {
    emit('error', error instanceof Error ? error.message : 'Airwallex 收银台加载失败')
  } finally { loading.value = false }
})
</script>

<template><div class="mt-4 rounded-lg border border-slate-700 bg-slate-950/40 p-5 text-center text-xs text-slate-500">{{ loading ? '正在跳转至安全收银台…' : '收银台没有返回跳转地址' }}</div></template>
