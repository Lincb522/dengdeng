<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group, ModelPrice, User, UserGroupRate } from '../../api/types'
import { formatMoney } from '../../api/types'

const users = ref<User[]>([])
const groups = ref<Group[]>([])
const prices = ref<ModelPrice[]>([])
const keyword = ref('')
const editing = ref<User | null>(null)
	const groupRates = ref<Record<number, number>>({})
const selectedPriceID = ref(0)
const selectedGroupID = ref(0)
const selectedServiceTier = ref<'standard' | 'fast' | 'flex'>('standard')

const tokenPrices = computed(() => prices.value.filter((price) =>
	price.input_price > 0 || price.output_price > 0 || price.cache_read_price > 0,
))
const selectedPrice = computed(() => tokenPrices.value.find((price) => price.id === selectedPriceID.value) || null)
const compatibleGroups = computed(() => {
	const platform = selectedPrice.value?.platform
	if (!platform) return groups.value
	const matched = groups.value.filter((group) => group.platform === platform || group.platform === 'composite')
	return matched.length ? matched : groups.value
})
const selectedGroup = computed(() => compatibleGroups.value.find((group) => group.id === selectedGroupID.value) || null)

type TokenEstimate = {
	input: number
	output: number
	longOutput: number
	cacheRead: number
	effectiveRate: number
	longContext: boolean
}

const form = ref({
  status: 'active',
  role: 'user',
  rate_multiplier: 1,
	concurrency: 0,
	set_balance_usd: 0,
  password: '',
  note: '',
})

async function load() {
  const q = keyword.value ? `?q=${encodeURIComponent(keyword.value)}` : ''
	const [nextUsers, nextGroups, nextPrices] = await Promise.all([
		api.get<User[]>(`/api/admin/users${q}`),
		api.get<Group[]>('/api/admin/groups'),
		api.get<ModelPrice[]>('/api/admin/prices'),
	])
	users.value = nextUsers
	groups.value = nextGroups
	prices.value = nextPrices
	if (!tokenPrices.value.some((price) => price.id === selectedPriceID.value)) {
		selectedPriceID.value = tokenPrices.value[0]?.id || 0
	}
	syncSelectedGroup()
}
onMounted(load)

function syncSelectedGroup() {
	if (!compatibleGroups.value.some((group) => group.id === selectedGroupID.value)) {
		selectedGroupID.value = compatibleGroups.value[0]?.id || 0
	}
}

watch(selectedPriceID, syncSelectedGroup)

function positiveMultiplier(value: number | undefined, fallback = 1) {
	return Number(value) > 0 ? Number(value) : fallback
}

function serviceTierMultiplier(group: Group) {
	if (selectedServiceTier.value === 'fast') return positiveMultiplier(group.fast_rate_multiplier, 2)
	if (selectedServiceTier.value === 'flex') return positiveMultiplier(group.flex_rate_multiplier, 0.5)
	return 1
}

function tokenCapacity(balanceMicro: number, unitPrice: number, rate: number, threshold = 0, longMultiplier = 1) {
	const ordinaryUnitCost = unitPrice * rate
	if (balanceMicro <= 0 || ordinaryUnitCost <= 0) return 0
	if (threshold <= 0 || longMultiplier <= 0 || longMultiplier === 1) {
		return Math.floor(balanceMicro / ordinaryUnitCost)
	}
	const ordinaryCost = threshold * ordinaryUnitCost
	if (balanceMicro <= ordinaryCost) return Math.floor(balanceMicro / ordinaryUnitCost)
	return threshold + Math.floor((balanceMicro - ordinaryCost) / (ordinaryUnitCost * longMultiplier))
}

function estimateForUser(user: User): TokenEstimate | null {
	const price = selectedPrice.value
	const group = selectedGroup.value
	if (!price || !group) return null
	const groupRate = positiveMultiplier(user.group_rates?.[group.id], positiveMultiplier(group.rate_multiplier))
	const effectiveRate = positiveMultiplier(user.rate_multiplier) * groupRate * serviceTierMultiplier(group)
	const threshold = Math.max(0, Number(group.long_context_threshold) || 0)
	const longContext = threshold > 0
	return {
		input: tokenCapacity(user.balance_micro, price.input_price, effectiveRate, threshold, positiveMultiplier(group.long_context_input_multiplier)),
		output: tokenCapacity(user.balance_micro, price.output_price, effectiveRate),
		longOutput: tokenCapacity(user.balance_micro, price.output_price, effectiveRate * positiveMultiplier(group.long_context_output_multiplier)),
		cacheRead: tokenCapacity(
			user.balance_micro,
			price.cache_read_price,
			effectiveRate * positiveMultiplier(group.cache_read_multiplier),
			threshold,
			positiveMultiplier(group.long_context_cache_multiplier),
		),
		effectiveRate,
		longContext,
	}
}

const tokenEstimates = computed<Record<number, TokenEstimate | null>>(() => Object.fromEntries(
	users.value.map((user) => [user.id, estimateForUser(user)]),
))

function formatTokens(tokens: number) {
	if (!Number.isFinite(tokens) || tokens <= 0) return '—'
	const units: Array<[number, string]> = [[1e12, 'T'], [1e9, 'B'], [1e6, 'M'], [1e3, 'K']]
	for (const [size, suffix] of units) {
		if (tokens >= size) {
			const value = tokens / size
			return `${value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1) : value.toFixed(2)}${suffix}`
		}
	}
	return Math.floor(tokens).toLocaleString('zh-CN')
}

function estimateTitle(user: User) {
	const estimate = tokenEstimates.value[user.id]
	const price = selectedPrice.value
	const group = selectedGroup.value
	if (!estimate || !price || !group) return ''
	const tier = selectedServiceTier.value === 'standard' ? '标准' : selectedServiceTier.value === 'fast' ? 'Fast / Priority' : 'Flex'
	return `${price.match} · ${group.name} · ${tier} · 实际综合倍率 ${estimate.effectiveRate.toFixed(4)}x`
}

async function openEdit(u: User) {
  editing.value = u
  form.value = { status: u.status, role: u.role, rate_multiplier: u.rate_multiplier, concurrency: u.concurrency || 0, set_balance_usd: Number((u.balance_micro / 1_000_000).toFixed(6)), password: '', note: u.note || '' }
	groupRates.value = {}
	try {
		const rates = await api.get<UserGroupRate[]>(`/api/admin/users/${u.id}/group-rates`)
		groupRates.value = Object.fromEntries(rates.map((rate) => [rate.group_id, rate.rate_multiplier]))
	} catch {
		editing.value = null
	}
}

function hasGroupRate(groupID: number) {
	return Object.prototype.hasOwnProperty.call(groupRates.value, groupID)
}

function toggleGroupRate(groupID: number, enabled: boolean) {
	if (enabled) groupRates.value[groupID] = 1
	else delete groupRates.value[groupID]
}

function formatRegisteredAt(value: string) {
	const date = new Date(value)
	if (Number.isNaN(date.getTime())) return '—'
	return date.toLocaleString('zh-CN', {
		year: 'numeric',
		month: '2-digit',
		day: '2-digit',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit',
		hour12: false,
	})
}

async function save() {
  if (!editing.value) return
  const body: Record<string, unknown> = {
    status: form.value.status,
    role: form.value.role,
    rate_multiplier: Number(form.value.rate_multiplier),
		concurrency: Math.max(0, Math.floor(Number(form.value.concurrency) || 0)),
    note: form.value.note,
  }
  body.set_balance_micro = Math.round(Math.max(0, Number(form.value.set_balance_usd) || 0) * 1_000_000)
  if (form.value.password) body.password = form.value.password
	const rates = Object.entries(groupRates.value)
		.map(([groupID, rateMultiplier]) => ({ group_id: Number(groupID), rate_multiplier: Number(rateMultiplier) }))
		.filter((item) => item.group_id > 0 && item.rate_multiplier > 0)
	const ok = await withToast(async () => {
		await api.put(`/api/admin/users/${editing.value!.id}`, body)
		return api.put(`/api/admin/users/${editing.value!.id}/group-rates`, { rates })
	}, '已保存')
  if (ok !== null) {
    editing.value = null
    await load()
  }
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>用户管理</h1>
      </div>
      <div class="flex gap-2">
        <input v-model="keyword" class="input !w-56" placeholder="按邮箱搜索" @keyup.enter="load" />
        <button class="btn-ghost" @click="load">搜索</button>
      </div>
    </div>

	<div class="card mb-4 p-3">
	  <div class="grid gap-3 md:grid-cols-[minmax(180px,1.4fr)_minmax(160px,1fr)_minmax(130px,.7fr)_auto] md:items-end">
		<label>
		  <span class="label">Token 换算模型</span>
		  <select v-model.number="selectedPriceID" class="input">
			<option v-for="price in tokenPrices" :key="price.id" :value="price.id">{{ price.match }}</option>
		  </select>
		</label>
		<label>
		  <span class="label">计费分组</span>
		  <select v-model.number="selectedGroupID" class="input">
			<option v-for="group in compatibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option>
		  </select>
		</label>
		<label>
		  <span class="label">服务档位</span>
		  <select v-model="selectedServiceTier" class="input">
			<option value="standard">标准</option>
			<option value="fast">Fast / Priority</option>
			<option value="flex">Flex</option>
		  </select>
		</label>
		<div class="pb-2 text-xs text-slate-500">按用户余额和实际倍率换算</div>
	  </div>
	</div>

    <div class="card overflow-x-auto">
      <table v-responsive-table class="table-base">
        <thead>
          <tr>
            <th>邮箱</th>
            <th>角色</th>
            <th>状态</th>
            <th class="text-right">余额 / 可用 Token</th>
            <th class="text-right">倍率</th>
						<th class="text-right">并发</th>
            <th>备注</th>
            <th>注册时间</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td class="font-medium text-slate-200">{{ u.email }}</td>
            <td><span :class="u.role === 'admin' ? 'tag-amber' : 'tag-gray'">{{ u.role }}</span></td>
            <td><span :class="u.status === 'active' ? 'tag-green' : 'tag-red'">{{ u.status === 'active' ? '正常' : '封禁' }}</span></td>
            <td class="num text-right" :class="u.balance_micro > 0 ? 'text-signal-green' : 'text-signal-red'" :title="estimateTitle(u)">
              <div>{{ formatMoney(u.balance_micro) }}</div>
			  <div v-if="tokenEstimates[u.id]" class="mt-1 whitespace-nowrap text-[10px] font-normal leading-4 text-slate-500">
				<div>输入 {{ formatTokens(tokenEstimates[u.id]!.input) }} · 输出 {{ formatTokens(tokenEstimates[u.id]!.output) }}</div>
				<div>缓存 {{ formatTokens(tokenEstimates[u.id]!.cacheRead) }}<template v-if="tokenEstimates[u.id]!.longContext"> · 长输出 {{ formatTokens(tokenEstimates[u.id]!.longOutput) }}</template></div>
			  </div>
            </td>
            <td class="num text-right">x{{ u.rate_multiplier }}</td>
						<td class="num text-right">{{ u.concurrency > 0 ? u.concurrency : '不限' }}</td>
            <td class="max-w-[160px] truncate text-xs text-slate-500" :title="u.note">{{ u.note }}</td>
            <td class="whitespace-nowrap text-xs text-slate-500">{{ formatRegisteredAt(u.created_at) }}</td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(u)">管理</button>
            </td>
          </tr>
          <tr v-if="!users.length">
						<td colspan="9" class="py-10 text-center text-sm text-slate-500">暂无用户</td>
          </tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="editing" class="legacy-modal-backdrop fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="editing = null">
        <div class="card w-full max-w-2xl p-6">
          <h3 class="mb-1 text-base font-semibold text-slate-100">管理用户</h3>
          <p class="mb-5 text-xs text-slate-500">{{ editing.email }}</p>
          <div class="space-y-4">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">状态</label>
                <select v-model="form.status" class="input">
                  <option value="active">正常</option>
                  <option value="disabled">封禁</option>
                </select>
              </div>
              <div>
                <label class="label">角色</label>
                <select v-model="form.role" class="input">
                  <option value="user">user</option>
                  <option value="admin">admin</option>
                </select>
              </div>
            </div>
						<div>
							<label class="label">用户并发上限</label>
							<input v-model.number="form.concurrency" type="number" min="0" max="10000" step="1" class="input" placeholder="0 = 不限制" />
							<p class="mt-1 text-xs text-slate-500">该用户所有密钥共享此上限；密钥还可以设置更小的独立上限。</p>
						</div>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">计费倍率</label>
                <input v-model.number="form.rate_multiplier" type="number" step="0.1" min="0.1" class="input" />
              </div>
              <div>
                <label class="label">当前余额（USD）</label>
                <input v-model.number="form.set_balance_usd" type="number" min="0" step="0.01" class="input" />
                <p class="mt-1 text-xs text-slate-500">保存后余额会直接设置为此金额。</p>
              </div>
            </div>
            <div>
              <label class="label">重置密码(留空不改)</label>
              <input v-model="form.password" type="text" class="input font-mono" placeholder="至少 8 位" />
            </div>
            <div>
              <label class="label">备注</label>
              <input v-model="form.note" class="input" />
            </div>
				<div class="rounded-xl border border-slate-800 bg-slate-950/35 p-4">
					<div class="mb-1 flex items-center justify-between gap-3">
						<label class="label !mb-0">分组专属倍率</label>
						<span class="text-[11px] text-slate-500">优先于分组默认倍率</span>
					</div>
					<p class="mb-3 text-xs leading-5 text-slate-500">未勾选的分组沿用默认倍率；用户通用倍率仍会一起生效。</p>
					<div v-if="groups.length" class="max-h-52 divide-y divide-slate-800/80 overflow-y-auto rounded-lg border border-slate-800">
						<div v-for="g in groups" :key="g.id" class="flex items-center gap-3 px-3 py-2.5">
							<label class="flex min-w-0 flex-1 items-center gap-2 text-sm text-slate-300">
								<input :checked="hasGroupRate(g.id)" type="checkbox" class="h-4 w-4 accent-amber" @change="toggleGroupRate(g.id, ($event.target as HTMLInputElement).checked)" />
								<span class="truncate">{{ g.name }}</span>
								<span class="text-xs text-slate-500">默认 x{{ g.rate_multiplier }}</span>
							</label>
							<input v-model.number="groupRates[g.id]" :disabled="!hasGroupRate(g.id)" type="number" min="0.01" max="1000" step="0.1" class="input !w-24 !py-1.5 disabled:cursor-not-allowed disabled:opacity-45" />
						</div>
					</div>
					<p v-else class="text-xs text-slate-500">暂无可配置分组</p>
				</div>
            <div class="flex justify-end gap-3 pt-2">
              <button class="btn-ghost" @click="editing = null">取消</button>
              <button class="btn-primary" @click="save">保存</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
