<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
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
const detailEntry = ref<CreationLibraryEntry | null>(null)
const detailDialog = ref<HTMLDialogElement | null>(null)

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
const currentSelectedCount = computed(() => currentEntries.value.filter((item) => isSelected(item)).length)
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

function entryTone(item: CreationLibraryEntry) {
	const seed = `${item.category || ''}${item.author || ''}${item.id}`
	let total = 0
	for (const character of seed) total += character.codePointAt(0) || 0
	return `is-tone-${total % 6}`
}

function entryKindLabel() {
	if (activeTab.value === 'rules') return '规则'
	if (activeTab.value === 'prompts') return '提示词'
	return '技能'
}

async function openDetail(item: CreationLibraryEntry) {
	detailEntry.value = item
	await nextTick()
	detailDialog.value?.showModal()
}

function closeDetail() {
	detailDialog.value?.close()
	detailEntry.value = null
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
			<div class="skill-store-count"><span>我的能力库</span><strong>{{ selectedCount }}</strong></div>
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
				<select v-if="activeTab === 'skills'" v-model="sourceFilter" aria-label="来源">
					<option value="all">全部来源</option>
					<option value="official">官方</option>
					<option value="community">社区</option>
					<option value="builtin">内置</option>
				</select>
				<button v-if="activeTab !== 'prompts'" type="button" :class="{ 'is-active': installedOnly }" @click="installedOnly = !installedOnly">已选择</button>
			</div>
		</div>
		<div v-if="categories.length > 1" class="skill-category-shelf" aria-label="技能分类">
			<button type="button" :class="{ 'is-active': category === 'all' }" @click="category = 'all'">全部</button>
			<button v-for="item in categories.slice(1)" :key="item" type="button" :class="{ 'is-active': category === item }" @click="category = item">{{ item }}</button>
		</div>

		<div v-if="loading" class="skill-store-empty">正在读取…</div>
		<div v-else-if="!library.enabled" class="skill-store-empty">技能商店未启用</div>
		<div v-else-if="!filteredEntries.length" class="skill-store-empty">没有匹配内容</div>
		<div v-else>
			<div class="skill-store-result"><strong>{{ filteredEntries.length }} 个{{ entryKindLabel() }}</strong><span v-if="activeTab !== 'prompts'">{{ currentSelectedCount }} 个已添加</span></div>
			<div class="skill-store-grid">
				<article v-for="item in filteredEntries" :key="item.id" class="skill-market-card" :class="[entryTone(item), { 'is-selected': isSelected(item) }]">
					<div class="skill-market-cover">
						<div class="skill-market-cover-top">
							<span>{{ item.category || scopeLabels[item.scope] }}</span>
							<span v-if="isSelected(item)" class="is-added">✓ 已添加</span>
						</div>
						<svg viewBox="0 0 48 48" aria-hidden="true">
							<rect x="7" y="7" width="14" height="14" rx="4" />
							<rect x="27" y="7" width="14" height="14" rx="4" />
							<rect x="7" y="27" width="14" height="14" rx="4" />
							<path d="M34 27v14M27 34h14" />
						</svg>
						<div class="skill-market-source"><span>{{ sourceLabels[item.source_type || 'custom'] }}</span><span v-if="item.version">v{{ item.version }}</span></div>
					</div>
					<div class="skill-market-body">
						<div class="skill-market-title">
							<strong>{{ item.name }}</strong>
							<small v-if="activeTab === 'skills' && item.name_en" lang="en">{{ item.name_en }}</small>
						</div>
						<p>{{ item.description }}</p>
						<div v-if="item.tags?.length" class="skill-market-tags">
							<span v-for="tag in item.tags.slice(0, 3)" :key="tag">{{ tag }}</span>
							<span v-if="item.tags.length > 3">+{{ item.tags.length - 3 }}</span>
						</div>
						<div class="skill-market-publisher">
							<span>{{ item.author || 'DengDeng AI' }}</span><span>{{ scopeLabels[item.scope] }}</span>
						</div>
					</div>
					<footer class="skill-market-actions">
						<button type="button" class="skill-market-detail" @click="openDetail(item)">查看详情</button>
						<button v-if="activeTab === 'prompts'" type="button" class="skill-market-primary" @click="copy(item.content)">复制提示词</button>
						<button v-else-if="item.auto_apply" type="button" class="skill-market-primary is-installed" disabled>系统应用</button>
						<button v-else type="button" class="skill-market-primary" :class="{ 'is-installed': isSelected(item) }" :disabled="pendingIDs.includes(item.id)" :aria-pressed="isSelected(item)" @click="toggleEntry(item)">
							{{ pendingIDs.includes(item.id) ? '保存中' : isSelected(item) ? '✓ 已添加' : '+ 添加' }}
						</button>
					</footer>
				</article>
			</div>
		</div>

		<dialog ref="detailDialog" class="skill-detail-dialog" @click.self="closeDetail" @close="detailEntry = null">
			<template v-if="detailEntry">
				<header>
					<div><span>{{ detailEntry.category || scopeLabels[detailEntry.scope] }}</span><h2>{{ detailEntry.name }}</h2><p v-if="detailEntry.name_en" lang="en">{{ detailEntry.name_en }}</p></div>
					<button type="button" aria-label="关闭详情" @click="closeDetail">×</button>
				</header>
				<div class="skill-detail-meta">
					<a v-if="detailEntry.source_url" :href="detailEntry.source_url" target="_blank" rel="noreferrer noopener">{{ sourceLabels[detailEntry.source_type || 'custom'] }} ↗</a><span v-else>{{ sourceLabels[detailEntry.source_type || 'custom'] }}</span><span>{{ detailEntry.author || 'DengDeng AI' }}</span><span>{{ scopeLabels[detailEntry.scope] }}</span><span v-if="detailEntry.license">{{ detailEntry.license }}</span>
				</div>
				<div class="skill-detail-description"><p>{{ detailEntry.description }}</p><p v-if="detailEntry.description_en" lang="en">{{ detailEntry.description_en }}</p></div>
				<div class="skill-card-content"><section><span>中文</span><pre>{{ detailEntry.content }}</pre></section><section v-if="detailEntry.content_en" lang="en"><span>English</span><pre>{{ detailEntry.content_en }}</pre></section></div>
			</template>
		</dialog>
	</div>
</template>

<style scoped>
.skill-store-page { display: grid; width: 100%; min-width: 0; gap: 1rem; color: var(--ink); }
.skill-store-head { display: flex; min-width: 0; align-items: flex-end; justify-content: space-between; gap: 1rem; padding: .3rem 0 1rem; border-bottom: 1px solid var(--line); }
.skill-store-head h1 { font-size: 1.45rem; font-weight: 880; letter-spacing: -.03em; }
.skill-store-head p { margin-top: .32rem; color: var(--ink-soft); font-size: .75rem; }
.skill-store-count { display: flex; flex: 0 0 auto; align-items: center; gap: .55rem; padding: .5rem .65rem; border-radius: .6rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .68rem; font-weight: 720; }
.skill-store-count strong { display: grid; min-width: 1.7rem; height: 1.7rem; place-items: center; border-radius: .42rem; background: var(--surface); color: var(--ink); font-size: .78rem; }
.skill-store-controls { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: .8rem; }
.skill-store-controls nav { display: flex; flex: 0 0 auto; gap: .2rem; padding: .2rem; border: 1px solid var(--line); border-radius: .65rem; background: var(--surface-muted); }
.skill-store-controls nav button, .skill-store-filters > button { min-height: 2.3rem; padding: 0 .72rem; border: 0; border-radius: .46rem; background: transparent; color: var(--ink-soft); font-size: .69rem; font-weight: 780; white-space: nowrap; }
.skill-store-controls nav button span { margin-left: .24rem; opacity: .65; }
.skill-store-controls button.is-active { background: var(--surface); box-shadow: 0 1px 3px var(--shadow); color: var(--ink); }
.skill-store-filters { display: flex; min-width: 0; align-items: center; gap: .42rem; }
.skill-store-search { display: flex; width: min(20rem, 32vw); min-width: 10rem; min-height: 2.45rem; align-items: center; gap: .45rem; padding: 0 .68rem; border: 1px solid var(--line); border-radius: .52rem; background: var(--surface); }
.skill-store-search svg { width: .9rem; height: .9rem; flex: 0 0 auto; fill: none; stroke: var(--ink-soft); stroke-width: 1.8; }
.skill-store-search input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--ink); font-size: .7rem; }
.skill-store-filters select { min-height: 2.45rem; max-width: 9rem; border: 1px solid var(--line); border-radius: .52rem; background: var(--surface); color: var(--ink); padding: 0 1.7rem 0 .62rem; font-size: .68rem; }
.skill-store-filters > button { border: 1px solid var(--line); background: var(--surface); }
.skill-category-shelf { display: flex; min-width: 0; flex-wrap: wrap; gap: .35rem; }
.skill-category-shelf button { min-height: 1.8rem; padding: 0 .58rem; border: 1px solid transparent; border-radius: 999px; background: var(--surface-muted); color: var(--ink-soft); font-size: .62rem; font-weight: 700; }
.skill-category-shelf button:hover { color: var(--ink); }
.skill-category-shelf button.is-active { border-color: color-mix(in srgb, var(--accent) 42%, var(--line)); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); color: var(--ink); }
.skill-store-result { display: flex; align-items: center; justify-content: space-between; gap: .75rem; margin: .15rem 0 .65rem; color: var(--ink-soft); font-size: .66rem; }
.skill-store-result strong { color: var(--ink); font-size: .72rem; }
.skill-store-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 16.5rem), 1fr)); gap: .85rem; }
.skill-market-card { --skill-cover: #e9d7ff; --skill-cover-ink: #4b2d69; display: flex; min-width: 0; overflow: hidden; border: 1px solid var(--line); border-radius: .82rem; background: var(--surface); flex-direction: column; transition: border-color .18s ease, transform .18s ease; }
.skill-market-card:hover { border-color: color-mix(in srgb, var(--skill-cover-ink) 42%, var(--line)); transform: translateY(-2px); }
.skill-market-card.is-selected { border-color: color-mix(in srgb, var(--accent) 64%, var(--line)); }
.skill-market-card.is-tone-1 { --skill-cover: #ccefe3; --skill-cover-ink: #245f4b; }
.skill-market-card.is-tone-2 { --skill-cover: #ffe1c2; --skill-cover-ink: #7c4718; }
.skill-market-card.is-tone-3 { --skill-cover: #cfe8ff; --skill-cover-ink: #24547c; }
.skill-market-card.is-tone-4 { --skill-cover: #f7d5dc; --skill-cover-ink: #7c3546; }
.skill-market-card.is-tone-5 { --skill-cover: #f0e4b8; --skill-cover-ink: #66541c; }
.skill-market-cover { position: relative; display: flex; min-height: 7.4rem; overflow: hidden; flex-direction: column; justify-content: space-between; padding: .75rem; background: var(--skill-cover); color: var(--skill-cover-ink); }
.skill-market-cover::after { position: absolute; right: -1.3rem; bottom: -2.1rem; width: 7rem; height: 7rem; border: 1.2rem solid color-mix(in srgb, var(--skill-cover-ink) 9%, transparent); border-radius: 50%; content: ''; }
.skill-market-cover-top, .skill-market-source { position: relative; z-index: 1; display: flex; align-items: center; justify-content: space-between; gap: .5rem; }
.skill-market-cover-top span, .skill-market-source span { padding: .18rem .42rem; border-radius: 999px; background: rgba(255, 255, 255, .62); font-size: .59rem; font-weight: 800; white-space: nowrap; }
.skill-market-cover-top .is-added { background: rgba(255, 255, 255, .9); color: #246849; }
.skill-market-cover svg { position: absolute; inset: 50% auto auto 50%; width: 3.5rem; height: 3.5rem; fill: none; stroke: currentColor; stroke-width: 2.4; transform: translate(-50%, -50%); }
.skill-market-source { justify-content: flex-start; }
.skill-market-body { display: flex; min-height: 11.6rem; flex: 1; flex-direction: column; padding: .85rem .85rem .7rem; }
.skill-market-title { display: grid; min-width: 0; gap: .16rem; }
.skill-market-title strong { color: var(--ink); font-size: .86rem; font-weight: 850; line-height: 1.3; }
.skill-market-title small { overflow: hidden; color: var(--ink-soft); font-size: .63rem; font-weight: 680; text-overflow: ellipsis; white-space: nowrap; }
.skill-market-body > p { display: -webkit-box; min-height: 2.9rem; margin-top: .55rem; overflow: hidden; color: var(--ink-soft); font-size: .69rem; line-height: 1.55; -webkit-box-orient: vertical; -webkit-line-clamp: 3; }
.skill-market-tags { display: flex; min-width: 0; flex-wrap: wrap; gap: .3rem; margin-top: .72rem; }
.skill-market-tags span { max-width: 100%; overflow: hidden; padding: .16rem .38rem; border-radius: .32rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .56rem; text-overflow: ellipsis; white-space: nowrap; }
.skill-market-publisher { display: flex; align-items: center; justify-content: space-between; gap: .6rem; margin-top: auto; padding-top: .75rem; color: var(--ink-faint); font-size: .58rem; }
.skill-market-publisher span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.skill-market-publisher span:last-child { flex: 0 0 auto; }
.skill-market-actions { display: grid; grid-template-columns: minmax(0, .85fr) minmax(0, 1.15fr); gap: .45rem; padding: .7rem .85rem .82rem; border-top: 1px solid var(--line); }
.skill-market-actions button { min-height: 2.15rem; border-radius: .48rem; font-size: .65rem; font-weight: 800; }
.skill-market-detail { border: 1px solid var(--line); color: var(--ink-soft); }
.skill-market-primary { background: var(--ink); color: var(--surface); }
.skill-market-primary.is-installed { background: color-mix(in srgb, rgb(var(--dd-signal-green)) 12%, var(--surface)); color: rgb(var(--dd-signal-green)); }
.skill-market-primary:disabled { cursor: default; opacity: .8; }
.skill-detail-dialog { width: min(44rem, calc(100vw - 1.5rem)); max-height: min(46rem, calc(100dvh - 1.5rem)); overflow: auto; border: 1px solid var(--line); border-radius: .9rem; padding: 0; background: var(--surface); color: var(--ink); }
.skill-detail-dialog::backdrop { background: rgba(31, 25, 20, .4); }
.skill-detail-dialog > header { position: sticky; z-index: 1; top: 0; display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; padding: 1rem; border-bottom: 1px solid var(--line); background: var(--surface); }
.skill-detail-dialog header span { color: var(--ink-soft); font-size: .62rem; font-weight: 760; }
.skill-detail-dialog h2 { margin-top: .18rem; font-size: 1rem; font-weight: 850; }
.skill-detail-dialog header p { margin-top: .15rem; color: var(--ink-soft); font-size: .68rem; }
.skill-detail-dialog header button { display: grid; width: 2rem; height: 2rem; flex: 0 0 auto; place-items: center; border: 1px solid var(--line); border-radius: .48rem; color: var(--ink); font-size: 1.1rem; }
.skill-detail-meta { display: flex; flex-wrap: wrap; gap: .35rem; padding: .8rem 1rem 0; }
.skill-detail-meta span, .skill-detail-meta a { padding: .18rem .4rem; border-radius: .32rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .58rem; text-decoration: none; }
.skill-detail-meta a:hover { color: var(--ink); }
.skill-detail-description { display: grid; gap: .25rem; padding: .8rem 1rem 0; color: var(--ink-soft); font-size: .68rem; line-height: 1.5; }
.skill-detail-description p[lang="en"] { color: var(--ink-faint); font-size: .63rem; }
.skill-card-content { display: grid; gap: .8rem; padding: 1rem; }
.skill-card-content section { display: grid; gap: .35rem; }
.skill-card-content section > span { color: var(--ink-faint); font-size: .58rem; font-weight: 760; }
.skill-card-content pre { max-height: 17rem; overflow: auto; margin: 0; padding: .75rem; border-radius: .55rem; background: var(--surface-muted); color: var(--ink); font-family: inherit; font-size: .68rem; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
.skill-store-empty { display: grid; min-height: 15rem; place-items: center; border: 1px solid var(--line); border-radius: .82rem; background: var(--surface); color: var(--ink-soft); font-size: .72rem; }
.skill-store-page :is(button, input, select):focus-visible, .skill-detail-dialog button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
@media (max-width: 820px) {
	.skill-store-controls { align-items: stretch; flex-direction: column; }
	.skill-store-controls nav { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); }
	.skill-store-filters { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; }
	.skill-store-search { width: 100%; }
}
@media (max-width: 560px) {
	.skill-store-head { align-items: flex-start; }
	.skill-store-head { flex-direction: column; }
	.skill-store-count { align-self: stretch; justify-content: space-between; }
	.skill-store-filters { grid-template-columns: 1fr 1fr; }
	.skill-store-search { grid-column: 1 / -1; }
	.skill-store-filters select { width: 100%; max-width: none; }
	.skill-store-filters > button { width: 100%; }
	.skill-store-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .55rem; }
	.skill-market-cover { min-height: 6rem; padding: .55rem; }
	.skill-market-cover svg { width: 2.6rem; height: 2.6rem; }
	.skill-market-cover-top span, .skill-market-source span { padding: .14rem .3rem; font-size: .5rem; }
	.skill-market-cover-top span:first-child { max-width: 8ch; overflow: hidden; text-overflow: ellipsis; }
	.skill-market-body { min-height: 10rem; padding: .65rem; }
	.skill-market-title strong { font-size: .73rem; }
	.skill-market-title small { font-size: .54rem; }
	.skill-market-body > p { min-height: 2.6rem; font-size: .6rem; -webkit-line-clamp: 3; }
	.skill-market-tags span:nth-child(n+3) { display: none; }
	.skill-market-actions { grid-template-columns: 1fr; padding: .55rem .65rem .65rem; }
	.skill-market-actions button { min-height: 2rem; }
}
@media (prefers-reduced-motion: reduce) { .skill-market-card { transition: none; } .skill-market-card:hover { transform: none; } }
</style>
