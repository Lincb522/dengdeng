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
const selectedEntry = ref<CreationLibraryEntry | null>(null)
const library = ref<CreationLibrarySettings>({ enabled: true, catalog_version: 6, capabilities: { ...defaultCapabilities }, prompts: [], rules: [], skills: [] })
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
	if (!keyword) return currentEntries.value
	return currentEntries.value
		.filter((item) => [item.name, item.name_en || '', item.id, item.description, item.description_en || '', item.author || '', item.category || '', item.license || '', item.source_url || '', item.install_command || '', ...(item.tags || [])].some((value) => value.toLowerCase().includes(keyword)))
})
const publishedCount = computed(() => library.value.skills.filter((item) => item.enabled).length)
const activePublishedCount = computed(() => currentEntries.value.filter((item) => item.enabled).length)

function selectTab(tab: LibraryTab) {
	activeTab.value = tab
	search.value = ''
	selectedEntry.value = currentEntries.value[0] || null
}

function addEntry() {
	const tab = tabs.find((item) => item.id === activeTab.value)!
	const timestamp = Date.now().toString(36)
	const entry: CreationLibraryEntry = {
		id: `${tab.singular}-${timestamp}`,
		name: activeTab.value === 'prompts' ? '新提示词' : activeTab.value === 'rules' ? '新规则' : '新技能', name_en: '',
		description: '', description_en: '', content: '', content_en: '', scope: activeTab.value === 'prompts' ? 'chat' : 'all', enabled: false, auto_apply: false,
		version: activeTab.value === 'skills' ? '1.0.0' : '', author: 'DengDeng AI', category: activeTab.value === 'skills' ? '通用' : '', tags: [],
		source_type: activeTab.value === 'skills' ? 'custom' : undefined, source_url: '', install_command: '', license: '',
	}
	currentEntries.value.unshift(entry)
	selectedEntry.value = entry
}

function removeEntry(item: CreationLibraryEntry) {
	const index = currentEntries.value.indexOf(item)
	if (index < 0) return
	currentEntries.value.splice(index, 1)
	selectedEntry.value = currentEntries.value[index] || currentEntries.value[index - 1] || null
}

function updateTags(item: CreationLibraryEntry, event: Event) {
	const value = (event.target as HTMLInputElement).value
	item.tags = value.split(/[,，、]/).map((tag) => tag.trim()).filter(Boolean).slice(0, 8)
}

function scopeLabel(scope: CreationLibraryScope) {
	return scopes.find((item) => item.id === scope)?.label || scope
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
		selectedEntry.value = library.value[activeTab.value][0] || null
	} finally {
		loading.value = false
	}
}

async function save() {
	saving.value = true
	try {
		const selectedID = selectedEntry.value?.id
		const saved = await withToast(() => api.put<CreationLibrarySettings>('/api/admin/creation-library', library.value), '技能商店已保存')
		if (saved) {
			library.value = saved
			selectedEntry.value = currentEntries.value.find((item) => item.id === selectedID) || currentEntries.value[0] || null
		}
	} finally {
		saving.value = false
	}
}

onMounted(load)
</script>

<template>
	<div class="skill-admin-page">
		<header class="skill-admin-head">
			<div><h1>技能上架管理</h1><p>{{ publishedCount }} 个技能已上架</p></div>
			<div class="skill-admin-actions">
				<label><input v-model="library.enabled" type="checkbox" role="switch" /><span>{{ library.enabled ? '商店开放' : '商店关闭' }}</span></label>
				<button type="button" :disabled="loading || saving" @click="save">{{ saving ? '保存中…' : '保存更改' }}</button>
			</div>
		</header>

		<section class="skill-capability-panel" aria-labelledby="capability-title">
			<h2 id="capability-title">能力开关</h2>
			<div class="skill-capability-groups">
				<fieldset><legend>内容类型</legend><label v-for="item in typeCapabilities" :key="item.id"><input v-model="library.capabilities[item.id]" type="checkbox" role="switch" /><span>{{ item.label }}</span></label></fieldset>
				<fieldset><legend>使用范围</legend><label v-for="item in scopeCapabilities" :key="item.id"><input v-model="library.capabilities[item.id]" type="checkbox" role="switch" /><span>{{ item.label }}</span></label></fieldset>
			</div>
		</section>

		<section class="skill-admin-workspace">
			<aside class="skill-library-index" aria-label="技能内容目录">
				<nav class="skill-admin-tabs" aria-label="管理类型">
					<button v-for="tab in tabs" :key="tab.id" type="button" :class="{ 'is-active': activeTab === tab.id }" @click="selectTab(tab.id)">{{ tab.label }} <span>{{ library[tab.id].length }}</span></button>
				</nav>
				<div class="skill-index-tools">
					<label class="skill-admin-search"><span class="sr-only">搜索</span><input v-model="search" type="search" placeholder="搜索名称、ID 或标签" /></label>
					<button type="button" @click="addEntry">新建</button>
				</div>
				<div class="skill-index-summary"><span>{{ filteredEntries.length }} 项</span><span>{{ activePublishedCount }} 项已上架</span></div>
				<div v-if="loading" class="skill-admin-empty">正在读取…</div>
				<div v-else-if="!filteredEntries.length" class="skill-admin-empty">暂无内容</div>
				<div v-else class="skill-admin-list">
					<button v-for="item in filteredEntries" :key="item.id" type="button" class="skill-index-item" :class="{ 'is-selected': selectedEntry === item }" @click="selectedEntry = item">
						<span class="skill-index-title"><strong>{{ item.name || '未命名' }}</strong><i :class="{ 'is-enabled': item.enabled }">{{ item.enabled ? '上架' : '下架' }}</i></span>
						<span class="skill-index-meta"><code>{{ item.id }}</code><span>{{ scopeLabel(item.scope) }}</span><span v-if="item.category">{{ item.category }}</span></span>
					</button>
				</div>
			</aside>

			<div class="skill-editor-pane">
				<div v-if="!selectedEntry" class="skill-editor-empty">选择一项进行编辑</div>
				<template v-else>
					<header class="skill-editor-head">
						<div><h2>{{ selectedEntry.name || '未命名' }}</h2><code>{{ selectedEntry.id }}</code></div>
						<div class="skill-editor-state">
							<label><input v-model="selectedEntry.enabled" type="checkbox" role="switch" /><span>{{ selectedEntry.enabled ? '已上架' : '未上架' }}</span></label>
							<label v-if="activeTab === 'rules'"><input v-model="selectedEntry.auto_apply" type="checkbox" role="switch" /><span>系统应用</span></label>
							<button type="button" @click="removeEntry(selectedEntry)">删除</button>
						</div>
					</header>

					<section class="skill-editor-section">
						<h3>基本信息</h3>
						<div class="skill-editor-grid skill-editor-grid--primary">
							<label><span>名称</span><input v-model.trim="selectedEntry.name" class="input" maxlength="64" /></label>
							<label v-if="activeTab === 'skills'"><span>English name</span><input v-model.trim="selectedEntry.name_en" class="input" maxlength="96" /></label>
							<label><span>ID</span><input v-model.trim="selectedEntry.id" class="input" maxlength="80" /></label>
							<label><span>范围</span><select v-model="selectedEntry.scope" class="input"><option v-for="scope in scopes" :key="scope.id" :value="scope.id">{{ scope.label }}</option></select></label>
							<label><span>作者</span><input v-model.trim="selectedEntry.author" class="input" maxlength="64" /></label>
							<label><span>分类</span><input v-model.trim="selectedEntry.category" class="input" maxlength="40" /></label>
							<label v-if="activeTab === 'skills'"><span>版本</span><input v-model.trim="selectedEntry.version" class="input" maxlength="24" /></label>
							<label class="skill-editor-tags"><span>标签</span><input :value="(selectedEntry.tags || []).join('、')" class="input" maxlength="180" placeholder="用逗号分隔" @input="updateTags(selectedEntry, $event)" /></label>
						</div>
					</section>

					<section class="skill-editor-section">
						<h3>说明</h3>
						<div class="skill-editor-grid">
							<label><span>中文说明</span><input v-model.trim="selectedEntry.description" class="input" maxlength="160" /></label>
							<label v-if="activeTab === 'skills'"><span>English description</span><input v-model.trim="selectedEntry.description_en" class="input" maxlength="240" /></label>
						</div>
					</section>

					<section v-if="activeTab === 'skills'" class="skill-editor-section">
						<h3>上架与安装</h3>
						<div class="skill-editor-grid skill-editor-grid--source">
							<label><span>来源类型</span><select v-model="selectedEntry.source_type" class="input"><option value="builtin">内置</option><option value="official">官方</option><option value="community">社区</option><option value="custom">自定义</option></select></label>
							<label><span>许可证</span><input v-model.trim="selectedEntry.license" class="input" maxlength="40" /></label>
							<label class="skill-editor-source-url"><span>开源地址</span><input v-model.trim="selectedEntry.source_url" class="input" maxlength="500" inputmode="url" placeholder="https://github.com/owner/repository" /></label>
						</div>
						<label class="skill-editor-install-command"><span>安装命令</span><textarea v-model="selectedEntry.install_command" class="input" rows="3" maxlength="2000" spellcheck="false" placeholder="输入用户需要执行的完整安装命令"></textarea></label>
					</section>

					<section class="skill-editor-section">
						<h3>能力内容</h3>
						<div class="skill-editor-content">
							<label><span>中文</span><textarea v-model="selectedEntry.content" class="input" rows="7" maxlength="4000"></textarea></label>
							<label v-if="activeTab === 'skills'"><span>English</span><textarea v-model="selectedEntry.content_en" class="input" rows="7" maxlength="4000"></textarea></label>
						</div>
					</section>
				</template>
			</div>
		</section>
	</div>
</template>

<style scoped>
.skill-admin-page { display: grid; width: 100%; min-width: 0; gap: 1.1rem; color: var(--ink); }
.skill-admin-head { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1rem; padding-bottom: 1rem; border-bottom: 1px solid var(--line); }
.skill-admin-head h1 { font-size: 1.3rem; font-weight: 880; letter-spacing: -.02em; }
.skill-admin-head p { margin-top: .3rem; color: var(--ink-soft); font-size: .74rem; }
.skill-admin-actions { display: flex; align-items: center; gap: .75rem; }
.skill-admin-actions label,
.skill-editor-state label,
.skill-capability-panel label { display: inline-flex; min-height: 2.75rem; align-items: center; gap: .45rem; color: var(--ink-soft); font-size: .72rem; font-weight: 730; white-space: nowrap; }
.skill-admin-actions button,
.skill-index-tools > button { min-height: 2.75rem; padding: 0 .95rem; border: 1px solid color-mix(in srgb, var(--accent) 55%, var(--line)); border-radius: .5rem; background: var(--surface); color: var(--ink); font-size: .74rem; font-weight: 820; }
.skill-admin-actions button:hover,
.skill-index-tools > button:hover { border-color: var(--accent); background: var(--accent-soft); }
.skill-admin-actions button:disabled { opacity: .5; }
.skill-capability-panel { display: grid; min-width: 0; grid-template-columns: 8rem minmax(0, 1fr); align-items: center; gap: 1rem; padding: .85rem 1rem; border: 1px solid var(--line); border-radius: .7rem; background: var(--surface); }
.skill-capability-panel h2 { font-size: .82rem; font-weight: 830; }
.skill-capability-groups { display: flex; min-width: 0; flex-wrap: wrap; justify-content: flex-end; gap: .55rem 1.5rem; }
.skill-capability-groups fieldset { display: flex; min-width: 0; align-items: center; gap: .75rem; border: 0; }
.skill-capability-groups legend { float: left; min-width: 4.5rem; margin-right: .1rem; color: var(--ink-faint); font-size: .66rem; }
.skill-admin-workspace { display: grid; min-width: 0; min-height: 38rem; grid-template-columns: 20rem minmax(0, 1fr); overflow: hidden; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); }
.skill-library-index { display: flex; min-width: 0; max-height: calc(100vh - 13rem); flex-direction: column; padding: .8rem; border-right: 1px solid var(--line); background: var(--surface-muted); }
.skill-admin-tabs { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .2rem; padding: .2rem; border: 1px solid var(--line); border-radius: .55rem; background: var(--surface); }
.skill-admin-tabs button { min-height: 2.5rem; padding: 0 .45rem; border: 0; border-radius: .38rem; background: transparent; color: var(--ink-soft); font-size: .72rem; font-weight: 780; white-space: nowrap; }
.skill-admin-tabs button span { margin-left: .2rem; color: var(--ink-faint); font-variant-numeric: tabular-nums; }
.skill-admin-tabs button.is-active { background: var(--accent-soft); color: var(--ink); }
.skill-index-tools { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: .5rem; margin-top: .75rem; }
.skill-admin-search input { width: 100%; min-width: 0; min-height: 2.75rem; border: 1px solid var(--line); border-radius: .5rem; background: var(--surface); padding: 0 .75rem; color: var(--ink); font-size: .74rem; }
.skill-index-summary { display: flex; justify-content: space-between; gap: .75rem; padding: .65rem .15rem .5rem; color: var(--ink-soft); font-size: .66rem; }
.skill-admin-list { min-height: 0; overflow-y: auto; border-top: 1px solid var(--line); }
.skill-index-item { display: grid; width: 100%; min-height: 4.6rem; align-content: center; gap: .45rem; padding: .72rem .6rem; border: 0; border-bottom: 1px solid var(--line); background: transparent; color: var(--ink); text-align: left; }
.skill-index-item:hover { background: color-mix(in srgb, var(--surface) 72%, transparent); }
.skill-index-item.is-selected { background: var(--surface); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent) 55%, var(--line)); }
.skill-index-title { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: .6rem; }
.skill-index-title strong { overflow: hidden; min-width: 0; font-size: .8rem; font-weight: 820; text-overflow: ellipsis; white-space: nowrap; }
.skill-index-title i { flex: 0 0 auto; padding: .18rem .38rem; border-radius: 999px; background: var(--surface); color: var(--ink-faint); font-size: .6rem; font-style: normal; font-weight: 760; }
.skill-index-title i.is-enabled { background: color-mix(in srgb, var(--surface) 82%, rgb(var(--dd-signal-green))); color: rgb(var(--dd-signal-green)); }
.skill-index-meta { display: flex; min-width: 0; align-items: center; gap: .45rem; color: var(--ink-soft); font-size: .65rem; }
.skill-index-meta code { overflow: hidden; min-width: 0; max-width: 9rem; text-overflow: ellipsis; white-space: nowrap; }
.skill-index-meta > span { flex: 0 0 auto; }
.skill-index-meta > span::before { content: '·'; margin-right: .45rem; color: var(--line-strong); }
.skill-admin-empty,
.skill-editor-empty { display: grid; min-height: 12rem; place-items: center; color: var(--ink-soft); font-size: .75rem; }
.skill-editor-pane { display: grid; min-width: 0; align-content: start; padding: 1.25rem; }
.skill-editor-head { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1rem; padding-bottom: 1rem; }
.skill-editor-head > div:first-child { display: grid; min-width: 0; gap: .3rem; }
.skill-editor-head h2 { overflow: hidden; font-size: 1.08rem; font-weight: 850; text-overflow: ellipsis; white-space: nowrap; }
.skill-editor-head code { overflow: hidden; max-width: 28rem; color: var(--ink-soft); font-size: .68rem; text-overflow: ellipsis; white-space: nowrap; }
.skill-editor-state { display: flex; flex: 0 0 auto; align-items: center; gap: .75rem; }
.skill-editor-state > button { min-height: 2.75rem; padding: 0 .75rem; border: 0; border-radius: .45rem; background: transparent; color: rgb(var(--dd-signal-red)); font-size: .72rem; font-weight: 780; }
.skill-editor-state > button:hover { background: color-mix(in srgb, var(--surface) 88%, rgb(var(--dd-signal-red))); }
.skill-editor-section { display: grid; min-width: 0; gap: .75rem; padding-block: 1rem; border-top: 1px solid var(--line); }
.skill-editor-section h3 { font-size: .78rem; font-weight: 830; }
.skill-editor-grid { display: grid; min-width: 0; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
.skill-editor-grid--primary { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.skill-editor-grid--source { grid-template-columns: 9rem 9rem minmax(0, 1fr); }
.skill-editor-grid label,
.skill-editor-content label { display: grid; min-width: 0; gap: .35rem; }
.skill-editor-install-command { display: grid; min-width: 0; gap: .35rem; }
.skill-editor-pane label > span { color: var(--ink-soft); font-size: .68rem; font-weight: 720; }
.skill-editor-pane :is(input.input, select.input, textarea.input) { width: 100%; min-width: 0; border-color: var(--line); background: var(--surface-muted); color: var(--ink); font-size: .75rem; }
.skill-editor-pane :is(input.input, select.input) { min-height: 2.65rem; }
.skill-editor-install-command textarea { min-height: 5.5rem; resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; line-height: 1.5; }
.skill-editor-content { display: grid; min-width: 0; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .75rem; }
.skill-editor-content textarea { min-height: 10rem; resize: vertical; line-height: 1.55; }
.skill-admin-page :is(button, input, textarea, select):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 1100px) {
	.skill-admin-workspace { grid-template-columns: 17rem minmax(0, 1fr); }
	.skill-editor-grid--primary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
	.skill-editor-grid--source { grid-template-columns: 8rem 8rem minmax(0, 1fr); }
}
@media (max-width: 820px) {
	.skill-capability-panel { grid-template-columns: 1fr; }
	.skill-capability-groups { justify-content: flex-start; }
	.skill-admin-workspace { display: block; }
	.skill-library-index { max-height: 23rem; border-right: 0; border-bottom: 1px solid var(--line); }
	.skill-editor-grid--source { grid-template-columns: 8rem 8rem minmax(0, 1fr); }
}
@media (max-width: 620px) {
	.skill-admin-head { align-items: stretch; flex-direction: column; }
	.skill-admin-actions { justify-content: space-between; }
	.skill-admin-actions button { flex: 1; }
	.skill-capability-groups { display: grid; width: 100%; }
	.skill-capability-groups fieldset { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
	.skill-capability-groups legend { float: none; grid-column: 1 / -1; }
	.skill-editor-pane { padding: 1rem; }
	.skill-editor-head { align-items: flex-start; flex-direction: column; }
	.skill-editor-state { width: 100%; flex-wrap: wrap; }
	.skill-editor-grid,
	.skill-editor-grid--primary { grid-template-columns: 1fr; }
	.skill-editor-grid--source { grid-template-columns: 1fr 1fr; }
	.skill-editor-content { grid-template-columns: 1fr; }
	.skill-editor-source-url { grid-column: 1 / -1; }
	.skill-editor-tags { grid-column: 1 / -1; }
}
@media (max-width: 430px) {
	.skill-editor-grid--source { grid-template-columns: 1fr; }
	.skill-editor-source-url { grid-column: auto; }
	.skill-editor-tags { grid-column: auto; }
}
</style>
