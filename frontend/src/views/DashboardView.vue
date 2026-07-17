<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { UsageSummary } from '../api/types'
import { formatMoney, formatTokens } from '../api/types'
import { useAuth } from '../stores/auth'
import StatCard from '../components/StatCard.vue'
import UsageChart from '../components/UsageChart.vue'

const auth = useAuth()
const summary = ref<UsageSummary | null>(null)
const locationOrigin = window.location.origin

onMounted(async () => {
  summary.value = await api.get<UsageSummary>('/api/user/usage/summary')
  await auth.fetchMe()
})
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>总览</h1>
        <p>你的调用、余额与最近消费都在这里。</p>
      </div>
      <span class="tag-green hidden sm:inline-flex">账号在线</span>
    </div>

    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <StatCard label="账户余额" :value="formatMoney(auth.user?.balance_micro ?? 0)" accent="green" />
      <StatCard
        label="今日请求"
        :value="String(summary?.today.requests ?? 0)"
        :sub="`消费 ${formatMoney(summary?.today.cost_micro ?? 0)}`"
        accent="amber"
      />
      <StatCard
        label="今日 Tokens"
        :value="formatTokens((summary?.today.input_tokens ?? 0) + (summary?.today.output_tokens ?? 0))"
        :sub="`入 ${formatTokens(summary?.today.input_tokens ?? 0)} / 出 ${formatTokens(summary?.today.output_tokens ?? 0)}`"
        accent="cyan"
      />
      <StatCard
        label="30 天消费"
        :value="formatMoney(summary?.month.cost_micro ?? 0)"
        :sub="`${summary?.month.requests ?? 0} 次请求`"
      />
    </div>

    <div class="mt-6">
      <UsageChart :daily="summary?.daily ?? []" />
    </div>

    <div class="card mt-6 p-6">
      <h3 class="mb-3 text-sm font-semibold text-slate-200">快速接入</h3>
      <div class="space-y-3 text-sm text-slate-400">
        <p>1. 在「API 密钥」页选择分组并创建密钥(以 <code class="rounded bg-ink-700 px-1.5 py-0.5 font-mono text-xs text-amber">dd-</code> 开头)。</p>
        <p>2. 把客户端的 Base URL 指向本站,填入你的密钥即可:</p>
        <pre class="overflow-x-auto rounded-lg border border-ink-600 bg-ink-950 p-4 font-mono text-xs leading-relaxed text-slate-300">
# Claude Code
export ANTHROPIC_BASE_URL="{{ locationOrigin }}"
export ANTHROPIC_AUTH_TOKEN="dd-xxx"

# OpenAI SDK
base_url = "{{ locationOrigin }}/v1"

# Gemini
{{ locationOrigin }}/v1beta/models/gemini-2.5-pro:generateContent</pre>
      </div>
    </div>
  </div>
</template>
