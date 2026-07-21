<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { Proxy } from '../../api/types'
import { useToast } from '../../stores/toast'

const toast = useToast()
const proxies = ref<Proxy[]>([])
const showForm = ref(false)
const editing = ref<Proxy | null>(null)
const testingID = ref<number | null>(null)
const form = ref({
  name: '', protocol: 'http' as Proxy['protocol'], host: '', port: 7890,
  username: '', password: '', clear_auth: false, status: 'active',
})

const canSave = computed(() => !!form.value.name.trim() && !!form.value.host.trim() && Number(form.value.port) >= 1 && Number(form.value.port) <= 65535)

async function load() {
  proxies.value = await api.get<Proxy[]>('/api/admin/proxies')
}

function openCreate() {
  editing.value = null
  form.value = { name: '', protocol: 'http', host: '', port: 7890, username: '', password: '', clear_auth: false, status: 'active' }
  showForm.value = true
}

function openEdit(item: Proxy) {
  editing.value = item
  form.value = {
    name: item.name, protocol: item.protocol, host: item.host, port: item.port,
    username: '', password: '', clear_auth: false, status: item.status,
  }
  showForm.value = true
}

async function save() {
  const body = { ...form.value, port: Number(form.value.port) }
  const result = editing.value
    ? await withToast(() => api.put(`/api/admin/proxies/${editing.value!.id}`, body), '代理已保存')
    : await withToast(() => api.post('/api/admin/proxies', body), '代理已添加')
  if (result) {
    showForm.value = false
    await load()
  }
}

async function testProxy(item: Proxy) {
  testingID.value = item.id
  try {
    const result = await api.post<{ ok: boolean; status: number; latency_ms: number }>(`/api/admin/proxies/${item.id}/test`)
    toast.show(result.ok ? `代理可用 · ${result.latency_ms}ms` : `代理响应异常 · HTTP ${result.status}`, result.ok ? 'success' : 'error')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '代理测试失败', 'error')
  } finally {
    testingID.value = null
  }
}

async function remove(item: Proxy) {
  if (!confirm(`删除代理「${item.name}」？关联账号会自动改为不使用独立代理。`)) return
  const result = await withToast(() => api.delete(`/api/admin/proxies/${item.id}`), '代理已删除')
  if (result) await load()
}

onMounted(load)
</script>

<template>
  <div class="proxies-page">
    <div class="console-page-head">
      <div>
        <h1>代理配置</h1>
        <p>代理单独维护，再按需绑定到上游账号；不会改变未绑定账号的出口。</p>
      </div>
      <button class="btn-primary" @click="openCreate">添加代理</button>
    </div>

    <div class="proxy-guide">
      <span class="proxy-guide__mark">i</span>
      <p>支持 HTTP、HTTPS CONNECT 和 SOCKS5。账号绑定请前往「上游账号」编辑；代理认证信息只会加密保存，不会再次显示。</p>
    </div>

    <div class="card overflow-x-auto">
      <table v-responsive-table class="table-base proxy-table">
        <thead>
          <tr><th>名称</th><th>地址</th><th>认证</th><th>绑定账号</th><th>状态</th><th>最近修改</th><th class="text-right">操作</th></tr>
        </thead>
        <tbody>
          <tr v-for="item in proxies" :key="item.id">
            <td class="font-medium text-slate-200">{{ item.name }}</td>
            <td><span class="proxy-endpoint">{{ item.protocol }}://{{ item.host }}:{{ item.port }}</span></td>
            <td><span :class="item.auth_configured ? 'tag-cyan' : 'tag-gray'">{{ item.auth_configured ? '已配置' : '无认证' }}</span></td>
            <td class="num">{{ item.account_count }}</td>
            <td><span :class="item.status === 'active' ? 'tag-green' : 'tag-gray'">{{ item.status === 'active' ? '启用' : '停用' }}</span></td>
            <td class="text-xs text-slate-500">{{ new Date(item.updated_at).toLocaleString() }}</td>
            <td class="whitespace-nowrap text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" :disabled="testingID === item.id" @click="testProxy(item)">{{ testingID === item.id ? '测试中…' : '测试' }}</button>
              <button class="btn-ghost ml-2 !px-2.5 !py-1 text-xs" @click="openEdit(item)">编辑</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(item)">删除</button>
            </td>
          </tr>
          <tr v-if="!proxies.length"><td colspan="7" class="py-12 text-center text-sm text-slate-500">还没有单独的代理配置</td></tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showForm = false">
        <section class="card w-full max-w-lg p-6" role="dialog" aria-modal="true" :aria-label="editing ? '编辑代理' : '添加代理'">
          <h2 class="mb-1 text-base font-semibold text-slate-100">{{ editing ? '编辑代理' : '添加代理' }}</h2>
          <p class="mb-5 text-xs text-slate-500">保存后可在上游账号中选择该代理。修改认证信息时，留空会保留原值。</p>
          <div class="space-y-4">
            <label class="label">名称<input v-model="form.name" class="input mt-1.5" placeholder="例如：香港出口 A" /></label>
            <div class="grid grid-cols-[120px_minmax(0,1fr)_92px] gap-3">
              <label class="label">协议<select v-model="form.protocol" class="input mt-1.5"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option></select></label>
              <label class="label">主机<input v-model="form.host" class="input mt-1.5 font-mono" placeholder="127.0.0.1 或域名" /></label>
              <label class="label">端口<input v-model.number="form.port" type="number" min="1" max="65535" class="input mt-1.5" /></label>
            </div>
            <div class="grid grid-cols-2 gap-3">
              <label class="label">用户名（可选）<input v-model="form.username" autocomplete="off" class="input mt-1.5" :placeholder="editing ? '留空保持不变' : ''" /></label>
              <label class="label">密码（可选）<input v-model="form.password" type="password" autocomplete="new-password" class="input mt-1.5" :placeholder="editing ? '留空保持不变' : ''" /></label>
            </div>
            <label v-if="editing && editing.auth_configured" class="flex items-center gap-2 text-xs text-slate-400"><input v-model="form.clear_auth" type="checkbox" class="accent-amber" /> 清除已有代理认证</label>
            <label class="label">状态<select v-model="form.status" class="input mt-1.5"><option value="active">启用</option><option value="disabled">停用</option></select></label>
            <div class="flex justify-end gap-3 pt-2"><button class="btn-ghost" @click="showForm = false">取消</button><button class="btn-primary" :disabled="!canSave" @click="save">保存</button></div>
          </div>
        </section>
      </div>
    </Teleport>
  </div>
</template>
