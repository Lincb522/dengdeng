import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

export function useAnchoredPopover(align: 'start' | 'end' = 'end') {
	const trigger = ref<HTMLElement | null>(null)
	const panel = ref<HTMLElement | null>(null)
	const open = ref(false)
	const pinned = ref(false)
	const panelStyle = ref({ top: '0px', left: '0px' })
	let closeTimer: number | undefined

	function clearCloseTimer() {
		if (closeTimer !== undefined) {
			window.clearTimeout(closeTimer)
			closeTimer = undefined
		}
	}

	async function show() {
		clearCloseTimer()
		open.value = true
		await nextTick()
		positionPanel()
	}

	function scheduleClose() {
		clearCloseTimer()
		if (pinned.value) return
		closeTimer = window.setTimeout(() => {
			open.value = false
		}, 120)
	}

	function togglePinned() {
		pinned.value = !pinned.value
		if (pinned.value) void show()
		else open.value = false
	}

	function close() {
		clearCloseTimer()
		pinned.value = false
		open.value = false
	}

	function positionPanel() {
		const anchor = trigger.value
		const content = panel.value
		if (!anchor || !content) return
		const rect = anchor.getBoundingClientRect()
		const gap = 8
		const margin = 10
		const width = content.offsetWidth
		const height = content.offsetHeight
		const preferredLeft = align === 'start' ? rect.left : rect.right - width
		const left = Math.min(
			Math.max(margin, preferredLeft),
			Math.max(margin, window.innerWidth - width - margin),
		)
		const below = rect.bottom + gap
		const top = below + height <= window.innerHeight - margin
			? below
			: Math.max(margin, rect.top - height - gap)
		panelStyle.value = { top: `${top}px`, left: `${left}px` }
	}

	function handleDocumentPointer(event: PointerEvent) {
		const target = event.target as Node
		if (trigger.value?.contains(target) || panel.value?.contains(target)) return
		close()
	}

	function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape' && open.value) {
			close()
			trigger.value?.focus()
		}
	}

	function handleViewportChange() {
		if (open.value) positionPanel()
	}

	onMounted(() => {
		document.addEventListener('pointerdown', handleDocumentPointer)
		document.addEventListener('keydown', handleKeydown)
		window.addEventListener('resize', handleViewportChange)
		window.addEventListener('scroll', handleViewportChange, true)
	})

	onBeforeUnmount(() => {
		clearCloseTimer()
		document.removeEventListener('pointerdown', handleDocumentPointer)
		document.removeEventListener('keydown', handleKeydown)
		window.removeEventListener('resize', handleViewportChange)
		window.removeEventListener('scroll', handleViewportChange, true)
	})

	return {
		trigger,
		panel,
		open,
		panelStyle,
		clearCloseTimer,
		show,
		scheduleClose,
		togglePinned,
	}
}
