<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { CreationCapabilitySettings, CreationLibraryEntry, CreationLibraryScope, CreationLibrarySettings } from '../../api/types'

type LibraryTab = 'prompts' | 'rules' | 'skills'

const defaultCapabilities: CreationCapabilitySettings = { prompts: true, rules: true, skills: true, chat: true, image: true, video: true, audio: true }
const loading = ref(true)
const saving = ref(false)
const activeTab = ref<LibraryTab>('skills')
const search = ref('')
const library = ref<CreationLibrarySettings>({ enabled: true, catalog_version: 5, capabilities: { ...defaultCapabilities }, prompts: [], rules: [], skills: [] })
const tabs: Array<{ id: LibraryTab; label: string; singular: string }> = [
	{ id: 'skills', label: '技能', singular: 'skill' },
	{ id: 'rules', label: '规则', singular: 'rule' },
	{ id: 'prompts', label: '提示词', singular: 'prompt' },
]
const scopes: Array<{ id: CreationLibraryScope; label: string }> = [
	{ id: 'all', label: '全部请求' }, { id: 'chat', label: '对话' }, { id: 'image', label: '图像' }, { id: 'video', label: '视频' }, { id: 'audio', label: '音频' },
]
const typeCapabilities: Array<{ id: keyof CreationCapabilitySettings; label: string }> = [
	{ id: 'skills', label: '技能' }, { id: 'rules', label: '规则' }, { id: 'prompts', label: '提示词' },
]
const scopeCapabilities: Array<{ id: keyof CreationCapabilitySettings; label: string }> = [
	{ id: 'chat', label: '对话' }, { id: 'image', label: '图像' }, { id: 'video', label: '视频' }, { id: 'audio', label: '音频' },
]
const currentEntries = computed(() => library.value[activeTab.value])
const filteredEntries = computed(() => {
	const keyword = search.value.trim().toLowerCase()
	if (!keyword) return currentEntries.value.map((item, index) => ({ item, index }))
	return currentEntries.value
		.map((item, index) => ({ item, index }))
		.filter(({ item }) => [item.name, item.name_en || '', item.id, item.description, item.description_en || '', item.author || '', item.category || '', item.license || '', ...(item.tags || [])].some((value) => value.toLowerCase().includes(keyword)))
})
const publishedCount = computed(() => library.value.skills.filter((item) => item.enabled).length)

function addEntry() {
	const tab = tabs.find((item) => item.id === activeTab.value)!
	const timestamp = Date.now().toString(36)
	currentEntries.value.unshift({
		id: `${tab.singular}-${timestamp}`,
		name: activeTab.value === 'prompts' ? '新提示词' : activeTab.value === 'rules' ? '新规则' : '新技能', name_en: '',
		description: '', description_en: '', content: '', content_en: '', scope: activeTab.value === 'prompts' ? 'chat' : 'all', enabled: false, auto_apply: false,
		version: activeTab.value === 'skills' ? '1.0.0' : '', author: 'DengDeng AI', category: activeTab.value === 'skills' ? '通用' : '', tags: [],
		source_type: activeTab.value === 'skills' ? 'custom' : undefined, source_url: '', license: '',
	})
}

function removeEntry(index: number) {
	currentEntries.value.splice(index, 1)
}

function updateTags(item: CreationLibraryEntry, event: Event) {
	const value = (event.target as HTMLInputElement).value
	item.tags = value.split(/[,，、]/).map((tag) => tag.trim()).filter(Boolean).slice(0, 8)
}

async function load() {
	loading.value = true
	try {
		const payload = await api.get<CreationLibrarySettings>('/api/admin/creation-library')
		library.value = {
			enabled: payload?.enabled !== false,
			catalog_version: Number(payload?.catalog_version || 5),
			capabilities: payload?.capabilities || { ...defaultCapabilities },
			prompts: Array.isArray(payload?.prompts) ? payload.prompts.map((item) => ({ ...item, tags: [...(item.tags || [])] })) : [],
			rules: Array.isArray(payload?.rules) ? payload.rules.map((item) => ({ ...item, tags: [...(item.tags || [])] })) : [],
			skills: Array.isArray(payload?.skills) ? payload.skills.map((item) => ({ ...item, tags: [...(item.tags || [])] })) : [],
		}
	} finally {
		loading.value = false
	}
}

async function save() {
	saving.value = true
	try {
		const saved = await withToast(() => api.put<CreationLibrarySettings>('/api/admin/creation-library', library.value), '技能商店已保存')
		if (saved) library.value = saved
	} finally {
		saving.value = false
	}
}

onMounted(load)
</script>

<template>
	<div class="skill-admin-page">
		<header class="skill-admin-head">
			<div><h1>技能管理</h1><p>{{ publishedCount }} 个技能已上架</p></div>
			<div class="skill-admin-actions">
				<label><input v-model="library.enabled" type="checkbox" role="switch" /><span>{{ library.enabled ? '商店开放' : '商店关闭' }}</span></label>
				<button type="button" :disabled="loading || saving" @click="save">{{ saving ? '保存中…' : '保存更改' }}</button>
			</div>
		</header>

		<section class="skill-capability-panel" aria-labelledby="capability-title">
			<div><h2 id="capability-title">能力开关</h2></div>
			<div class="skill-capability-groups">
				<fieldset><legend>内容</legend><label v-for="item in typeCapabilities" :key="item.id"><input v-model="library.capabilities[item.id]" type="checkbox" role="switch" /><span>{{ item.label }}</span></label></fieldset>
				<fieldset><legend>请求范围</legend><label v-for="item in scopeCapabilities" :key="item.id"><input v-model="library.capabilities[item.id]" type="checkbox" role="switch" /><span>{{ item.label }}</span></label></fieldset>
			</div>
		</section>

		<div class="skill-admin-toolbar">
			<nav aria-label="管理类型">
				<button v-for="tab in tabs" :key="tab.id" type="button" :class="{ 'is-active': activeTab === tab.id }" @click="activeTab = tab.id">{{ tab.label }} <span>{{ library[tab.id].length }}</span></button>
			</nav>
			<div>
				<label class="skill-admin-search"><span class="sr-only">搜索</span><input v-model="search" type="search" placeholder="搜索" /></label>
				<button type="button" @click="addEntry">新建{{ tabs.find((item) => item.id === activeTab)?.label }}</button>
			</div>
		</div>

		<div v-if="loading" class="skill-admin-empty">正在读取…</div>
		<div v-else-if="!filteredEntries.length" class="skill-admin-empty">暂无内容</div>
		<div v-else class="skill-admin-list">
			<article v-for="{ item, index } in filteredEntries" :key="item.id" class="skill-editor">
				<header>
					<div class="skill-editor-state">
						<label><input v-model="item.enabled" type="checkbox" role="switch" /><span>{{ item.enabled ? '已上架' : '未上架' }}</span></label>
						<label v-if="activeTab === 'rules'"><input v-model="item.auto_apply" type="checkbox" role="switch" /><span>系统应用</span></label>
					</div>
					<button type="button" @click="removeEntry(index)">删除</button>
				</header>

				<div class="skill-editor-primary">
					<label><span>名称</span><input v-model.trim="item.name" class="input" maxlength="64" /></label>
					<label v-if="activeTab === 'skills'"><span>English name</span><input v-model.trim="item.name_en" class="input" maxlength="96" /></label>
					<label><span>ID</span><input v-model.trim="item.id" class="input" maxlength="80" /></label>
					<label><span>范围</span><select v-model="item.scope" class="input"><option v-for="scope in scopes" :key="scope.id" :value="scope.id">{{ scope.label }}</option></select></label>
				</div>
				<div class="skill-editor-meta">
					<label><span>作者</span><input v-model.trim="item.author" class="input" maxlength="64" /></label>
					<label><span>分类</span><input v-model.trim="item.category" class="input" maxlength="40" /></label>
					<label v-if="activeTab === 'skills'"><span>版本</span><input v-model.trim="item.version" class="input" maxlength="24" /></label>
					<label class="skill-editor-tags"><span>标签</span><input :value="(item.tags || []).join('、')" class="input" maxlength="180" placeholder="用逗号分隔" @input="updateTags(item, $event)" /></label>
				</div>
				<div v-if="activeTab === 'skills'" class="skill-editor-source">
					<label><span>来源类型</span><select v-model="item.source_type" class="input"><option value="builtin">内置</option><option value="official">官方</option><option value="community">社区</option><option value="custom">自定义</option></select></label>
					<label><span>许可证</span><input v-model.trim="item.license" class="input" maxlength="40" /></label>
					<label class="skill-editor-source-url"><span>来源地址</span><input v-model.trim="item.source_url" class="input" maxlength="500" inputmode="url" /></label>
				</div>
				<label class="skill-editor-field"><span>中文说明</span><input v-model.trim="item.description" class="input" maxlength="160" /></label>
				<label v-if="activeTab === 'skills'" class="skill-editor-field"><span>English description</span><input v-model.trim="item.description_en" class="input" maxlength="240" /></label>
				<label class="skill-editor-field"><span>中文能力内容</span><textarea v-model="item.content" class="input" rows="5" maxlength="4000"></textarea></label>
				<label v-if="activeTab === 'skills'" class="skill-editor-field"><span>English instruction</span><textarea v-model="item.content_en" class="input" rows="5" maxlength="4000"></textarea></label>
			</article>
		</div>
	</div>
</template>

<style scoped>
.skill-admin-page { display: grid; width: 100%; min-width: 0; gap: .85rem; color: var(--ink); }
.skill-admin-head { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1rem; padding-bottom: .8rem; border-bottom: 1px solid var(--line); }
.skill-admin-head h1 { font-size: 1.08rem; font-weight: 880; letter-spacing: -.02em; }
.skill-admin-head p { margin-top: .2rem; color: var(--ink-soft); font-size: .58rem; }
.skill-admin-actions { display: flex; align-items: center; gap: .58rem; }
.skill-admin-actions label, .skill-editor-state label, .skill-capability-panel label { display: inline-flex; align-items: center; gap: .38rem; color: var(--ink-soft); font-size: .59rem; font-weight: 730; white-space: nowrap; }
.skill-admin-actions button, .skill-admin-toolbar > div > button { min-height: 2.2rem; padding: 0 .76rem; border: 1px solid color-mix(in srgb, var(--accent) 55%, var(--line)); border-radius: .46rem; background: var(--surface); color: var(--ink); font-size: .62rem; font-weight: 820; }
.skill-admin-actions button:disabled { opacity: .5; }
.skill-capability-panel { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1rem; padding: .68rem .78rem; border: 1px solid var(--line); border-radius: .62rem; background: var(--surface); }
.skill-capability-panel h2 { font-size: .68rem; font-weight: 830; }
.skill-capability-groups { display: flex; min-width: 0; flex-wrap: wrap; justify-content: flex-end; gap: .8rem; }
.skill-capability-groups fieldset { display: flex; min-width: 0; align-items: center; gap: .55rem; border: 0; }
.skill-capability-groups legend { float: left; margin-right: .1rem; color: var(--ink-faint); font-size: .52rem; }
.skill-admin-toolbar { display: flex; align-items: center; justify-content: space-between; gap: .7rem; }
.skill-admin-toolbar nav { display: flex; gap: .18rem; padding: .18rem; border: 1px solid var(--line); border-radius: .52rem; background: var(--surface-muted); }
.skill-admin-toolbar nav button { min-height: 1.9rem; padding: 0 .66rem; border: 0; border-radius: .36rem; background: transparent; color: var(--ink-soft); font-size: .61rem; font-weight: 780; white-space: nowrap; }
.skill-admin-toolbar nav button span { margin-left: .22rem; opacity: .62; }
.skill-admin-toolbar nav button.is-active { background: var(--surface); box-shadow: 0 1px 3px var(--shadow); color: var(--ink); }
.skill-admin-toolbar > div { display: flex; min-width: 0; gap: .4rem; }
.skill-admin-search input { width: min(14rem, 24vw); min-width: 8rem; min-height: 2.2rem; border: 1px solid var(--line); border-radius: .46rem; background: var(--surface); padding: 0 .62rem; color: var(--ink); font-size: .62rem; }
.skill-admin-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 32rem), 1fr)); gap: .68rem; }
.skill-editor { display: grid; min-width: 0; align-content: start; gap: .62rem; padding: .8rem; border: 1px solid var(--line); border-radius: .66rem; background: var(--surface); }
.skill-editor > header { display: flex; align-items: center; justify-content: space-between; gap: .7rem; }
.skill-editor-state { display: flex; flex-wrap: wrap; gap: .65rem; }
.skill-editor > header > button { border: 0; background: transparent; color: rgb(var(--dd-signal-red)); font-size: .58rem; font-weight: 760; }
.skill-editor-primary, .skill-editor-meta { display: grid; min-width: 0; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr) minmax(7rem, .6fr); gap: .5rem; }
.skill-editor-meta { grid-template-columns: minmax(0, .8fr) minmax(0, .7fr) minmax(5.5rem, .5fr) minmax(0, 1.2fr); }
.skill-editor-source { display: grid; min-width: 0; grid-template-columns: 7rem 7rem minmax(0, 1fr); gap: .5rem; }
.skill-editor-primary label, .skill-editor-meta label, .skill-editor-source label, .skill-editor-field { display: grid; min-width: 0; gap: .28rem; }
.skill-editor :is(label > span) { color: var(--ink-soft); font-size: .53rem; font-weight: 720; }
.skill-editor :is(input.input, select.input, textarea.input) { width: 100%; min-width: 0; border-color: var(--line); background: var(--surface-muted); color: var(--ink); font-size: .62rem; }
.skill-editor textarea { min-height: 6.8rem; resize: vertical; line-height: 1.52; }
.skill-admin-empty { display: grid; min-height: 12rem; place-items: center; border: 1px solid var(--line); border-radius: .66rem; background: var(--surface); color: var(--ink-soft); font-size: .65rem; }
.skill-admin-page :is(button, input, textarea, select):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 900px) {
	.skill-capability-panel { align-items: flex-start; flex-direction: column; }
	.skill-capability-groups { justify-content: flex-start; }
}
@media (max-width: 700px) {
	.skill-admin-head, .skill-admin-toolbar { align-items: stretch; flex-direction: column; }
	.skill-admin-actions { justify-content: space-between; }
	.skill-admin-actions button { flex: 1; }
	.skill-admin-toolbar nav { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); }
	.skill-admin-toolbar > div { display: grid; grid-template-columns: minmax(0, 1fr) auto; }
	.skill-admin-search input { width: 100%; }
	.skill-capability-groups, .skill-capability-groups fieldset { align-items: flex-start; flex-direction: column; }
	.skill-editor-primary, .skill-editor-meta { grid-template-columns: 1fr 1fr; }
	.skill-editor-source { grid-template-columns: 1fr 1fr; }
	.skill-editor-source-url { grid-column: 1 / -1; }
	.skill-editor-tags { grid-column: 1 / -1; }
}
@media (max-width: 460px) {
	.skill-editor-primary, .skill-editor-meta, .skill-editor-source { grid-template-columns: 1fr; }
	.skill-editor-tags { grid-column: auto; }
	.skill-editor-source-url { grid-column: auto; }
}
</style>
