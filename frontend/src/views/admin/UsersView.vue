<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group, User, UserGroupRate } from '../../api/types'
import { formatMoney } from '../../api/types'

const users = ref<User[]>([])
const groups = ref<Group[]>([])
const keyword = ref('')
const editing = ref<User | null>(null)
	const groupRates = ref<Record<number, number>>({})

const form = ref({
  status: 'active',
  role: 'user',
  rate_multiplier: 1,
  add_balance_usd: 0,
  password: '',
  note: '',
})

async function load() {
  const q = keyword.value ? `?q=${encodeURIComponent(keyword.value)}` : ''
	const [nextUsers, nextGroups] = await Promise.all([
		api.get<User[]>(`/api/admin/users${q}`),
		api.get<Group[]>('/api/admin/groups'),
	])
	users.value = nextUsers
	groups.value = nextGroups
}
onMounted(load)

async function openEdit(u: User) {
  editing.value = u
  form.value = { status: u.status, role: u.role, rate_multiplier: u.rate_multiplier, add_balance_usd: 0, password: '', note: u.note || '' }
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

async function save() {
  if (!editing.value) return
  const body: Record<string, unknown> = {
    status: form.value.status,
    role: form.value.role,
    rate_multiplier: Number(form.value.rate_multiplier),
    note: form.value.note,
  }
  if (form.value.add_balance_usd) {
    body.add_balance_micro = Math.round(Number(form.value.add_balance_usd) * 1_000_000)
  }
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

    <div class="card overflow-x-auto">
      <table class="table-base">
        <thead>
          <tr>
            <th>邮箱</th>
            <th>角色</th>
            <th>状态</th>
            <th class="text-right">余额</th>
            <th class="text-right">倍率</th>
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
            <td class="num text-right" :class="u.balance_micro > 0 ? 'text-signal-green' : 'text-signal-red'">
              {{ formatMoney(u.balance_micro) }}
            </td>
            <td class="num text-right">x{{ u.rate_multiplier }}</td>
            <td class="max-w-[160px] truncate text-xs text-slate-500" :title="u.note">{{ u.note }}</td>
            <td class="text-xs text-slate-500">{{ new Date(u.created_at).toLocaleDateString() }}</td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(u)">管理</button>
            </td>
          </tr>
          <tr v-if="!users.length">
            <td colspan="8" class="py-10 text-center text-sm text-slate-500">暂无用户</td>
          </tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="editing" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="editing = null">
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
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">计费倍率</label>
                <input v-model.number="form.rate_multiplier" type="number" step="0.1" min="0.1" class="input" />
              </div>
              <div>
                <label class="label">调整余额 (USD, 可负)</label>
                <input v-model.number="form.add_balance_usd" type="number" step="0.01" class="input" placeholder="+10 或 -5" />
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
