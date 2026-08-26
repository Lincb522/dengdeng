<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText } from '../api/client'
import type { CreationLibraryEntry, CreationLibraryScope, CreationLibrarySettings } from '../api/types'
import { useToast } from '../stores/toast'

type LibraryTab = 'prompts' | 'rules' | 'skills'

const emptyCapabilities = { prompts: true, rules: true, skills: true, chat: true, image: true, video: true, audio: true }
const toast = useToast()
const loading = ref(true)
const library = ref<CreationLibrarySettings>({ enabled: false, catalog_version: 0, capabilities: { ...emptyCapabilities }, prompts: [], rules: [], skills: [] })
const activeTab = ref<LibraryTab>('skills')
const search = ref('')
const category = ref('all')
const sourceFilter = ref('all')
const installedOnly = ref(false)
const selectedRules = ref<string[]>([])
const selectedSkills = ref<string[]>([])
const pendingIDs = ref<string[]>([])

const tabs: Array<{ id: LibraryTab; label: string }> = [
	{ id: 'skills', label: '技能商店' },
	{ id: 'rules', label: '规则' },
	{ id: 'prompts', label: '提示词' },
]
const scopeLabels: Record<CreationLibraryScope, string> = { all: '全场景', chat: '对话', image: '图像', video: '视频', audio: '音频' }
const sourceLabels: Record<string, string> = { builtin: '内置', official: '官方', community: '社区', custom: '自定义' }
const currentEntries = computed(() => library.value[activeTab.value] || [])
const categories = computed(() => {
	const values = currentEntries.value.map((item) => item.category?.trim()).filter((value): value is string => Boolean(value))
	return ['all', ...Array.from(new Set(values)).sort((a, b) => a.localeCompare(b, 'zh-CN'))]
})
const selectedCount = computed(() => selectedRules.value.length + selectedSkills.value.length)
const filteredEntries = computed(() => {
	const keyword = search.value.trim().toLowerCase()
	return currentEntries.value.filter((item) => {
		if (category.value !== 'all' && item.category !== category.value) return false
		if (sourceFilter.value !== 'all' && item.source_type !== sourceFilter.value) return false
		if (installedOnly.value && activeTab.value !== 'prompts' && !isSelected(item)) return false
		if (!keyword) return true
		return [item.name, item.name_en || '', item.description, item.description_en || '', item.content, item.content_en || '', item.id, item.author || '', item.category || '', item.license || '', ...(item.tags || [])]
			.some((value) => value.toLowerCase().includes(keyword))
	})
})

function isSelected(item: CreationLibraryEntry) {
	if (activeTab.value === 'rules') return item.auto_apply || selectedRules.value.includes(item.id)
	if (activeTab.value === 'skills') return item.auto_apply || selectedSkills.value.includes(item.id)
	return false
}

function selectTab(tab: LibraryTab) {
	activeTab.value = tab
	category.value = 'all'
	sourceFilter.value = 'all'
	installedOnly.value = false
}

async function toggleEntry(item: CreationLibraryEntry) {
	if (item.auto_apply || activeTab.value === 'prompts' || pendingIDs.value.includes(item.id)) return
	const nextRules = activeTab.value === 'rules'
		? (selectedRules.value.includes(item.id) ? selectedRules.value.filter((id) => id !== item.id) : [...selectedRules.value, item.id])
		: selectedRules.value
	const nextSkills = activeTab.value === 'skills'
		? (selectedSkills.value.includes(item.id) ? selectedSkills.value.filter((id) => id !== item.id) : [...selectedSkills.value, item.id])
		: selectedSkills.value
	pendingIDs.value = [...pendingIDs.value, item.id]
	try {
		const saved = await api.put<{ rule_ids: string[]; skill_ids: string[] }>('/api/user/creation-library/selection', {
			rule_ids: nextRules,
			skill_ids: nextSkills,
		})
		selectedRules.value = Array.isArray(saved?.rule_ids) ? saved.rule_ids : []
		selectedSkills.value = Array.isArray(saved?.skill_ids) ? saved.skill_ids : []
		toast.show(isSelected(item) ? '已启用' : '已停用', 'success')
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '保存失败', 'error')
	} finally {
		pendingIDs.value = pendingIDs.value.filter((id) => id !== item.id)
	}
}

async function copy(value: string) {
	try {
		await copyText(value)
		toast.show('已复制', 'success')
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '复制失败', 'error')
	}
}

async function load() {
	loading.value = true
	try {
		const payload = await api.get<CreationLibrarySettings>('/api/user/creation-library')
		library.value = {
			enabled: payload?.enabled === true,
			catalog_version: Number(payload?.catalog_version || 0),
			capabilities: payload?.capabilities || { ...emptyCapabilities },
			prompts: Array.isArray(payload?.prompts) ? payload.prompts : [],
			rules: Array.isArray(payload?.rules) ? payload.rules : [],
			skills: Array.isArray(payload?.skills) ? payload.skills : [],
			selected_rule_ids: Array.isArray(payload?.selected_rule_ids) ? payload.selected_rule_ids : [],
			selected_skill_ids: Array.isArray(payload?.selected_skill_ids) ? payload.selected_skill_ids : [],
		}
		selectedRules.value = library.value.selected_rule_ids || []
		selectedSkills.value = library.value.selected_skill_ids || []
	} finally {
		loading.value = false
	}
}

onMounted(load)
</script>

<template>
	<div class="skill-store-page">
		<header class="skill-store-head">
			<div>
				<h1>技能商店</h1>
				<p>已选择的规则和技能会应用到账号下的 API 密钥。</p>
			</div>
			<div class="skill-store-count"><strong>{{ selectedCount }}</strong><span>已选择</span></div>
		</header>

		<div class="skill-store-controls">
			<nav aria-label="能力类型">
				<button v-for="tab in tabs" :key="tab.id" type="button" :class="{ 'is-active': activeTab === tab.id }" @click="selectTab(tab.id)">
					{{ tab.label }} <span>{{ library[tab.id].length }}</span>
				</button>
			</nav>
			<div class="skill-store-filters">
				<label class="skill-store-search">
					<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"></circle><path d="m20 20-4-4"></path></svg>
					<input v-model="search" type="search" placeholder="搜索名称、分类或标签" />
				</label>
				<select v-model="category" aria-label="分类">
					<option value="all">全部分类</option>
					<option v-for="item in categories.slice(1)" :key="item" :value="item">{{ item }}</option>
				</select>
				<select v-if="activeTab === 'skills'" v-model="sourceFilter" aria-label="来源">
					<option value="all">全部来源</option>
					<option value="official">官方</option>
					<option value="community">社区</option>
					<option value="builtin">内置</option>
				</select>
				<button v-if="activeTab !== 'prompts'" type="button" :class="{ 'is-active': installedOnly }" @click="installedOnly = !installedOnly">已选择</button>
			</div>
		</div>

		<div v-if="loading" class="skill-store-empty">正在读取…</div>
		<div v-else-if="!library.enabled" class="skill-store-empty">技能商店未启用</div>
		<div v-else-if="!filteredEntries.length" class="skill-store-empty">没有匹配内容</div>
		<div v-else class="skill-store-grid">
			<article v-for="item in filteredEntries" :key="item.id" class="skill-card" :class="{ 'is-selected': isSelected(item) }">
				<header>
					<div class="skill-card-title">
						<span class="skill-card-mark" aria-hidden="true">{{ activeTab === 'skills' ? 'S' : activeTab === 'rules' ? 'R' : 'P' }}</span>
						<div><strong>{{ item.name }}</strong><small v-if="activeTab === 'skills' && item.name_en">{{ item.name_en }}</small><span>{{ item.category || scopeLabels[item.scope] }}</span></div>
					</div>
					<button v-if="activeTab === 'prompts'" type="button" @click="copy(item.content)">复制</button>
					<button v-else-if="item.auto_apply" type="button" disabled>系统应用</button>
					<button v-else type="button" :disabled="pendingIDs.includes(item.id)" :aria-pressed="isSelected(item)" @click="toggleEntry(item)">
						{{ pendingIDs.includes(item.id) ? '保存中' : isSelected(item) ? '移除' : '选择' }}
					</button>
				</header>
				<div class="skill-card-descriptions"><p>{{ item.description }}</p><p v-if="activeTab === 'skills' && item.description_en" lang="en">{{ item.description_en }}</p></div>
				<div class="skill-card-meta">
					<span>{{ scopeLabels[item.scope] }}</span>
					<a v-if="item.source_url" :href="item.source_url" target="_blank" rel="noreferrer noopener" :class="`is-${item.source_type || 'custom'}`">{{ sourceLabels[item.source_type || 'custom'] }} · {{ item.author || '来源' }}</a>
					<span v-else-if="item.author">{{ item.author }}</span>
					<span v-if="item.version">v{{ item.version }}</span>
					<span v-if="item.license">{{ item.license }}</span>
				</div>
				<div v-if="item.tags?.length" class="skill-card-tags"><span v-for="tag in item.tags" :key="tag">{{ tag }}</span></div>
				<details><summary>查看中英文内容</summary><div class="skill-card-content"><section><span>中文</span><pre>{{ item.content }}</pre></section><section v-if="activeTab === 'skills' && item.content_en" lang="en"><span>English</span><pre>{{ item.content_en }}</pre></section></div></details>
			</article>
		</div>
	</div>
</template>

<style scoped>
.skill-store-page { display: grid; width: 100%; min-width: 0; gap: .9rem; color: var(--ink); }
.skill-store-head { display: flex; min-width: 0; align-items: flex-end; justify-content: space-between; gap: 1rem; padding-bottom: .85rem; border-bottom: 1px solid var(--line); }
.skill-store-head h1 { font-size: 1.12rem; font-weight: 880; letter-spacing: -.02em; }
.skill-store-head p { margin-top: .26rem; color: var(--ink-soft); font-size: .62rem; }
.skill-store-count { display: flex; flex: 0 0 auto; align-items: baseline; gap: .35rem; color: var(--ink-soft); font-size: .58rem; }
.skill-store-count strong { color: var(--ink); font-size: 1rem; }
.skill-store-controls { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: .75rem; }
.skill-store-controls nav { display: flex; flex: 0 0 auto; gap: .18rem; padding: .18rem; border: 1px solid var(--line); border-radius: .55rem; background: var(--surface-muted); }
.skill-store-controls nav button, .skill-store-filters > button { min-height: 1.95rem; padding: 0 .64rem; border: 0; border-radius: .38rem; background: transparent; color: var(--ink-soft); font-size: .61rem; font-weight: 780; white-space: nowrap; }
.skill-store-controls nav button span { margin-left: .2rem; opacity: .62; }
.skill-store-controls button.is-active { background: var(--surface); box-shadow: 0 1px 3px var(--shadow); color: var(--ink); }
.skill-store-filters { display: flex; min-width: 0; align-items: center; gap: .38rem; }
.skill-store-search { display: flex; width: min(19rem, 32vw); min-width: 9rem; min-height: 2.28rem; align-items: center; gap: .4rem; padding: 0 .64rem; border: 1px solid var(--line); border-radius: .46rem; background: var(--surface); }
.skill-store-search svg { width: .86rem; height: .86rem; flex: 0 0 auto; fill: none; stroke: var(--ink-soft); stroke-width: 1.8; }
.skill-store-search input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--ink); font-size: .62rem; }
.skill-store-filters select { min-height: 2.28rem; max-width: 8.5rem; border: 1px solid var(--line); border-radius: .46rem; background: var(--surface); color: var(--ink); padding: 0 1.7rem 0 .58rem; font-size: .61rem; }
.skill-store-filters > button { border: 1px solid var(--line); background: var(--surface); }
.skill-store-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr)); gap: .65rem; }
.skill-card { display: flex; min-width: 0; min-height: 13rem; flex-direction: column; gap: .62rem; padding: .82rem; border: 1px solid var(--line); border-radius: .68rem; background: var(--surface); }
.skill-card.is-selected { border-color: color-mix(in srgb, var(--accent) 58%, var(--line)); background: color-mix(in srgb, var(--accent) 4%, var(--surface)); }
.skill-card > header { display: flex; min-width: 0; align-items: flex-start; justify-content: space-between; gap: .65rem; }
.skill-card-title { display: flex; min-width: 0; align-items: center; gap: .5rem; }
.skill-card-mark { display: grid; width: 1.9rem; height: 1.9rem; flex: 0 0 auto; place-items: center; border-radius: .48rem; background: var(--surface-muted); color: var(--ink); font-size: .68rem; font-weight: 900; }
.skill-card-title > div { display: grid; min-width: 0; gap: .13rem; }
.skill-card strong { overflow: hidden; font-size: .72rem; font-weight: 850; text-overflow: ellipsis; white-space: nowrap; }
.skill-card-title small { overflow: hidden; color: var(--ink); font-size: .55rem; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.skill-card-title div > span { overflow: hidden; color: var(--ink-soft); font-size: .53rem; text-overflow: ellipsis; white-space: nowrap; }
.skill-card header > button { min-height: 1.82rem; flex: 0 0 auto; padding: 0 .54rem; border: 1px solid var(--line); border-radius: .4rem; background: var(--surface); color: var(--ink); font-size: .57rem; font-weight: 800; }
.skill-card header > button[aria-pressed="true"], .skill-card header > button:disabled { color: rgb(var(--dd-signal-green)); }
.skill-card-descriptions { display: grid; min-height: 3.65rem; gap: .18rem; }
.skill-card-descriptions p { color: var(--ink-soft); font-size: .61rem; line-height: 1.45; }
.skill-card-descriptions p[lang="en"] { color: var(--ink-faint); font-size: .56rem; }
.skill-card-meta, .skill-card-tags { display: flex; min-width: 0; flex-wrap: wrap; gap: .3rem; }
.skill-card-meta span, .skill-card-meta a, .skill-card-tags span { max-width: 100%; overflow: hidden; padding: .14rem .34rem; border-radius: .3rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .5rem; text-decoration: none; text-overflow: ellipsis; white-space: nowrap; }
.skill-card-meta a.is-official { background: color-mix(in srgb, var(--accent) 10%, var(--surface-muted)); color: var(--accent); }
.skill-card-meta a.is-community { color: rgb(var(--dd-signal-green)); }
.skill-card details { margin-top: auto; border-top: 1px solid var(--line); padding-top: .5rem; }
.skill-card summary { cursor: pointer; color: var(--ink-soft); font-size: .56rem; font-weight: 740; }
.skill-card-content { display: grid; gap: .55rem; margin-top: .52rem; }
.skill-card-content section { display: grid; gap: .2rem; }
.skill-card-content section > span { color: var(--ink-faint); font-size: .5rem; font-weight: 760; }
.skill-card pre { max-height: 8rem; overflow: auto; margin: 0; color: var(--ink); font-family: inherit; font-size: .58rem; line-height: 1.55; white-space: pre-wrap; overflow-wrap: anywhere; }
.skill-store-empty { display: grid; min-height: 12rem; place-items: center; border: 1px solid var(--line); border-radius: .68rem; background: var(--surface); color: var(--ink-soft); font-size: .66rem; }
.skill-store-page :is(button, input, select, summary):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 820px) {
	.skill-store-controls { align-items: stretch; flex-direction: column; }
	.skill-store-controls nav { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); }
	.skill-store-filters { display: grid; grid-template-columns: minmax(0, 1fr) auto auto auto; }
	.skill-store-search { width: 100%; }
}
@media (max-width: 560px) {
	.skill-store-head { align-items: flex-start; }
	.skill-store-head p { max-width: 28ch; }
	.skill-store-filters { grid-template-columns: 1fr 1fr; }
	.skill-store-search { grid-column: 1 / -1; }
	.skill-store-filters select { width: 100%; max-width: none; }
	.skill-store-filters > button { width: 100%; }
	.skill-card { min-height: 0; }
}
</style>
