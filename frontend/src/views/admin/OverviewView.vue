<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api/client'
import type { UsageSummary } from '../../api/types'
import { formatMoney, formatTokens } from '../../api/types'
import StatCard from '../../components/StatCard.vue'
import UsageChart from '../../components/UsageChart.vue'

const summary = ref<UsageSummary | null>(null)

onMounted(async () => {
  summary.value = await api.get<UsageSummary>('/api/admin/dashboard')
})
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>运营总览</h1>
        <p>站点的账号池、请求与流水概况。</p>
      </div>
      <span class="tag-amber hidden sm:inline-flex">管理视图</span>
    </div>

    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <StatCard label="注册用户" :value="String(summary?.counts?.users ?? 0)" accent="cyan" />
      <StatCard label="上游账号" :value="String(summary?.counts?.accounts ?? 0)" :sub="`${summary?.counts?.groups ?? 0} 个分组`" />
      <StatCard
        label="今日请求"
        :value="String(summary?.today.requests ?? 0)"
        :sub="`Tokens ${formatTokens((summary?.today.input_tokens ?? 0) + (summary?.today.output_tokens ?? 0))}`"
        accent="amber"
      />
      <StatCard
        label="今日流水"
        :value="formatMoney(summary?.today.cost_micro ?? 0)"
        :sub="`30 天 ${formatMoney(summary?.month.cost_micro ?? 0)}`"
        accent="green"
      />
    </div>

    <div class="mt-6">
      <UsageChart :daily="summary?.daily ?? []" />
    </div>
  </div>
</template>
