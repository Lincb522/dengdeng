<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../../api/client'
import type { RedeemCode } from '../../api/types'
import { formatMoney } from '../../api/types'
import { useToast } from '../../stores/toast'

const toast = useToast()
const codes = ref<RedeemCode[]>([])
const showForm = ref(false)
const count = ref(10)
const kind = ref<'amount' | 'days' | 'requests'>('amount')
const amountUSD = ref(10)
const entitlementValue = ref(30)
const generated = ref<string[]>([])

async function load() {
  codes.value = await api.get<RedeemCode[]>('/api/admin/redeem-codes')
}
onMounted(load)

async function generate() {
  const payload = {
    count: Number(count.value),
    kind: kind.value,
    amount_micro: kind.value === 'amount' ? Math.round(Number(amountUSD.value) * 1_000_000) : 0,
    value: kind.value === 'amount' ? 0 : Number(entitlementValue.value),
  }
  const result = await withToast(
    () => api.post<{ batch: string; codes: string[] }>('/api/admin/redeem-codes', payload),
    '生成成功',
  )
  if (result) {
    generated.value = result.codes
    await load()
  }
}

async function copyAll() {
  try {
    await copyText(generated.value.join('\n'))
    toast.show('已复制全部', 'success')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

function codeKind(cd: RedeemCode) {
  return cd.kind || 'amount'
}

function kindLabel(cd: RedeemCode) {
  const labels: Record<string, string> = { amount: '按额度', days: '按日', requests: '按次' }
  return labels[codeKind(cd)] || '按额度'
}

function benefitLabel(cd: RedeemCode) {
  const type = codeKind(cd)
  if (type === 'days') return `${cd.value} 天`
  if (type === 'requests') return `${cd.value.toLocaleString()} 次`
  return formatMoney(cd.amount_micro)
}

function closeForm() {
  showForm.value = false
  generated.value = []
}

async function remove(cd: RedeemCode) {
  await withToast(() => api.delete(`/api/admin/redeem-codes/${cd.id}`), '已删除')
  await load()
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>兑换码</h1>
        <p class="mt-1 text-sm text-slate-500">支持按额度、有效天数或调用次数发放</p>
      </div>
      <button class="btn-primary" @click="showForm = true">批量生成</button>
    </div>

    <div class="card overflow-x-auto">
      <table class="table-base">
        <thead>
          <tr>
            <th>兑换码</th>
            <th>类型</th>
            <th class="text-right">权益</th>
            <th>批次</th>
            <th>状态</th>
            <th>使用者</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="cd in codes" :key="cd.id">
            <td class="font-mono text-xs text-slate-300">{{ cd.code }}</td>
            <td><span class="tag-gray">{{ kindLabel(cd) }}</span></td>
            <td class="num text-right text-amber">{{ benefitLabel(cd) }}</td>
            <td class="text-xs text-slate-500">{{ cd.batch }}</td>
            <td>
              <span :class="cd.used_by ? 'tag-gray' : 'tag-green'">{{ cd.used_by ? '已使用' : '未使用' }}</span>
            </td>
            <td class="text-xs text-slate-500">
              {{ cd.used_by_email || '-' }}
              <span v-if="cd.used_at" class="ml-1">{{ new Date(cd.used_at).toLocaleString() }}</span>
            </td>
            <td class="text-right">
              <button v-if="!cd.used_by" class="btn-danger !px-2.5 !py-1 text-xs" @click="remove(cd)">作废</button>
            </td>
          </tr>
          <tr v-if="!codes.length">
            <td colspan="7" class="py-10 text-center text-sm text-slate-500">暂无兑换码</td>
          </tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="closeForm">
        <div class="card w-full max-w-md p-6">
          <template v-if="!generated.length">
            <h3 class="mb-5 text-base font-semibold text-slate-100">批量生成兑换码</h3>
            <div class="space-y-4">
              <div class="grid grid-cols-2 gap-4">
                <div>
                  <label class="label">数量 (1-200)</label>
                  <input v-model.number="count" type="number" min="1" max="200" class="input" />
                </div>
                <div>
                  <label class="label">兑换方式</label>
                  <select v-model="kind" class="input">
                    <option value="amount">按额度</option>
                    <option value="days">按日</option>
                    <option value="requests">按次</option>
                  </select>
                </div>
              </div>
              <div v-if="kind === 'amount'">
                <label class="label">单张额度 (USD)</label>
                <input v-model.number="amountUSD" type="number" step="0.01" min="0.01" class="input" />
              </div>
              <div v-else>
                <label class="label">{{ kind === 'days' ? '有效天数' : '调用次数' }}</label>
                <input v-model.number="entitlementValue" type="number" min="1" :max="kind === 'days' ? 3660 : 10000000" class="input" />
                <p class="mt-2 text-xs text-slate-500">
                  {{ kind === 'days' ? '有效期内调用不扣余额。' : '每次成功调用消耗 1 次；上游调用失败会自动返还。' }}
                </p>
              </div>
              <div class="flex justify-end gap-3 pt-2">
                <button class="btn-ghost" @click="closeForm">取消</button>
                <button class="btn-primary" :disabled="!count || (kind === 'amount' ? !amountUSD : !entitlementValue)" @click="generate">生成</button>
              </div>
            </div>
          </template>
          <template v-else>
            <h3 class="mb-2 text-base font-semibold text-signal-green">已生成 {{ generated.length }} 张</h3>
            <div class="max-h-64 overflow-y-auto rounded-lg border border-ink-600 bg-ink-950 p-3 font-mono text-xs leading-relaxed text-slate-300">
              <div v-for="c in generated" :key="c">{{ c }}</div>
            </div>
            <div class="mt-5 flex justify-end gap-3">
              <button class="btn-primary" @click="copyAll">复制全部</button>
              <button class="btn-ghost" @click="closeForm">完成</button>
            </div>
          </template>
        </div>
      </div>
    </Teleport>
  </div>
</template>
