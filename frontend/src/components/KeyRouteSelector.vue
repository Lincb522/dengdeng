<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api/client'
import type { ApiKey, Group } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'
import { useToast } from '../stores/toast'
import ProviderLogo from './ProviderLogo.vue'

const props = defineProps<{
	apiKey: ApiKey
	groups: Group[]
	multiGroupEnabled: boolean
	admin?: boolean
}>()

const emit = defineEmits<{ updated: [key: ApiKey] }>()
const toast = useToast()
const saving = ref(false)
const draft = ref<number[]>([])
const { trigger, panel, open, panelStyle, togglePinned, close } = useAnchoredPopover('start')
const popoverID = `key-route-${Math.random().toString(36).slice(2)}`
const platformOrder = ['openai', 'anthropic', 'gemini', 'grok']

const selectedGroups = computed(() => {
	const values = props.apiKey.groups?.length ? props.apiKey.groups : (props.apiKey.group ? [props.apiKey.group] : [])
	return values.filter((group, index) => values.findIndex((candidate) => candidate.id === group.id) === index)
})

const groupedOptions = computed(() => {
	const result = new Map<string, Group[]>()
	for (const group of props.groups) {
		if (!result.has(group.platform)) result.set(group.platform, [])
		result.get(group.platform)!.push(group)
	}
	return [...result.entries()].sort(([left], [right]) => {
		const leftIndex = platformOrder.indexOf(left)
		const rightIndex = platformOrder.indexOf(right)
		return (leftIndex < 0 ? 99 : leftIndex) - (rightIndex < 0 ? 99 : rightIndex)
	})
})

const routeTitle = computed(() => {
	if (!selectedGroups.value.length) return '未绑定路由'
	if (selectedGroups.value.length === 1) return selectedGroups.value[0].name
	return `${selectedGroups.value[0].name} +${selectedGroups.value.length - 1}`
})

const routeMeta = computed(() => {
	if (!selectedGroups.value.length) return '请选择分组'
	const platforms = [...new Set(selectedGroups.value.map((group) => PLATFORM_LABELS[group.platform] || group.platform))]
	return props.multiGroupEnabled && selectedGroups.value.length > 1
		? `${platforms.join(' / ')} · ${selectedGroups.value.length} 组容错`
		: `${platforms[0]} · 倍率 ×${selectedGroups.value[0].rate_multiplier}`
})

function syncDraft() {
	draft.value = selectedGroups.value.map((group) => group.id)
}

watch(() => props.apiKey.group_ids, syncDraft, { immediate: true, deep: true })
watch(open, (isOpen) => { if (isOpen) syncDraft() })

function toggleGroup(groupID: number) {
	if (!props.multiGroupEnabled) {
		draft.value = [groupID]
		void saveRoute()
		return
	}
	const selected = new Set(draft.value)
	if (selected.has(groupID)) selected.delete(groupID)
	else selected.add(groupID)
	draft.value = [...selected]
}

async function saveRoute() {
	if (!draft.value.length || saving.value) return
	const current = selectedGroups.value.map((group) => group.id)
	if (current.length === draft.value.length && current.every((id, index) => id === draft.value[index])) {
		close()
		return
	}
	saving.value = true
	try {
		const updated = await api.put<ApiKey>(`/api/user/keys/${props.apiKey.id}`, { group_ids: draft.value })
		emit('updated', updated)
		toast.show('路由已切换', 'success')
		close()
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '路由切换失败', 'error')
	} finally {
		saving.value = false
	}
}
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="key-route-trigger"
		:aria-controls="open ? popoverID : undefined"
		:aria-expanded="open"
		:disabled="saving || !groups.length"
		@click.stop="togglePinned"
	>
		<ProviderLogo class="key-route-mark" :platform="selectedGroups[0]?.platform" size="md" />
		<span class="key-route-summary">
			<strong>{{ routeTitle }}</strong>
			<small>{{ routeMeta }}</small>
		</span>
		<svg viewBox="0 0 20 20" aria-hidden="true"><path d="m6 8 4 4 4-4" /></svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section
				v-if="open"
				:id="popoverID"
				ref="panel"
				class="key-route-popover"
				:style="panelStyle"
				role="dialog"
				aria-label="切换密钥路由"
				@click.stop
			>
				<header>
					<div><strong>路由目标</strong><small>{{ multiGroupEnabled ? '可选择多个分组作为容错路由' : '选择一个分组' }}</small></div>
					<span v-if="admin">管理员</span>
				</header>

				<div class="key-route-options">
					<section v-for="[platform, options] in groupedOptions" :key="platform">
						<h4 class="provider-inline-label"><ProviderLogo :platform="platform" size="sm" />{{ PLATFORM_LABELS[platform] || platform }}</h4>
						<button
							v-for="group in options"
							:key="group.id"
							type="button"
							:class="{ 'is-selected': draft.includes(group.id) }"
							:disabled="saving"
							@click="toggleGroup(group.id)"
						>
							<span class="key-route-check" aria-hidden="true">{{ draft.includes(group.id) ? '✓' : '' }}</span>
							<span><strong>{{ group.name }}</strong><small>倍率 ×{{ group.rate_multiplier }}</small></span>
							<em v-if="admin && !group.is_public">私有</em>
						</button>
					</section>
				</div>

				<footer v-if="multiGroupEnabled">
					<span>已选 {{ draft.length }} 个分组</span>
					<div><button type="button" class="btn-ghost" :disabled="saving" @click="close">取消</button><button type="button" class="btn-primary" :disabled="saving || !draft.length" @click="saveRoute">{{ saving ? '保存中…' : '保存路由' }}</button></div>
				</footer>
			</section>
		</Transition>
	</Teleport>
</template>
