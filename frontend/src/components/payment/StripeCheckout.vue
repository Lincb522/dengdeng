<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { loadStripe, type Stripe, type StripeElements } from '@stripe/stripe-js'
import type { PaymentCheckout } from '../../api/types'

const props = defineProps<{ checkout: PaymentCheckout }>()
const emit = defineEmits<{ complete: []; error: [message: string] }>()
const mount = ref<HTMLElement | null>(null)
const loading = ref(true)
const ready = ref(false)
const submitting = ref(false)
let stripe: Stripe | null = null
let elements: StripeElements | null = null
let paymentElement: { unmount: () => void } | null = null

onMounted(async () => {
  try {
    if (!props.checkout.publishable_key || !props.checkout.client_secret) throw new Error('Stripe 订单缺少前端支付参数')
    stripe = await loadStripe(props.checkout.publishable_key)
    if (!stripe) throw new Error('Stripe 收银台加载失败')
    await nextTick()
    if (!mount.value) return
    elements = stripe.elements({ clientSecret: props.checkout.client_secret, appearance: { theme: 'stripe', variables: { borderRadius: '10px' } } })
    const element = elements.create('payment', { layout: 'tabs' })
    element.mount(mount.value)
    element.on('ready', () => { ready.value = true })
    paymentElement = element
  } catch (error) {
    emit('error', error instanceof Error ? error.message : 'Stripe 收银台加载失败')
  } finally { loading.value = false }
})

onBeforeUnmount(() => paymentElement?.unmount())

async function confirm() {
  if (!stripe || !elements || submitting.value) return
  submitting.value = true
  const { error } = await stripe.confirmPayment({ elements, confirmParams: { return_url: `${window.location.origin}/wallet` }, redirect: 'if_required' })
  submitting.value = false
  if (error) { emit('error', error.message || '支付确认失败'); return }
  emit('complete')
}
</script>

<template>
  <div class="mt-4 rounded-lg border border-slate-700 bg-slate-950/40 p-4">
    <div v-if="loading" class="py-6 text-center text-xs text-slate-500">正在加载安全收银台…</div>
    <div ref="mount" :class="{ hidden: loading }"></div>
    <button class="btn-primary mt-5 w-full" :disabled="!ready || submitting" @click="confirm">{{ submitting ? '正在确认…' : '确认支付' }}</button>
  </div>
</template>
