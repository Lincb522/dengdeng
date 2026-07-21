import type { Directive } from 'vue'

const SUMMARY_PRIORITY = new Map([
	['状态', 120],
	['可用度', 115],
	['用户', 112],
	['模型', 110],
	['额度', 108],
	['费用', 105],
	['分组', 100],
	['平台', 95],
	['类型', 90],
	['地址', 85],
	['输入', 80],
	['余额', 78],
	['请求', 75],
	['权益', 72],
	['结果', 70],
	['来源', 65],
])

function ensureExpandButton(row: HTMLTableRowElement, firstCell: HTMLTableCellElement) {
	let button = firstCell.querySelector<HTMLButtonElement>(':scope > .mobile-table-expand')
	if (button) return

	button = document.createElement('button')
	button.type = 'button'
	button.className = 'mobile-table-expand'
	button.textContent = '详情'
	button.setAttribute('aria-expanded', 'false')
	button.addEventListener('click', () => {
		const expanded = row.dataset.mobileExpanded === 'true'
		row.dataset.mobileExpanded = expanded ? 'false' : 'true'
		button!.textContent = expanded ? '详情' : '收起'
		button!.setAttribute('aria-expanded', expanded ? 'false' : 'true')
	})
	firstCell.append(button)
}

function syncMobileLabels(table: HTMLTableElement) {
	const headerRow = table.tHead?.rows.item(table.tHead.rows.length - 1)
	const labels = headerRow ? Array.from(headerRow.cells, (cell) => cell.textContent?.trim() || '') : []

	for (const body of Array.from(table.tBodies)) {
		for (const row of Array.from(body.rows)) {
			const cells = Array.from(row.cells)
			for (const cell of cells) {
				cell.removeAttribute('data-mobile-summary')
				if (cell.colSpan > 1) {
					cell.removeAttribute('data-label')
					continue
				}
				cell.dataset.label = labels[cell.cellIndex] || ''
			}

			if (cells.length < 4 || cells[0].colSpan > 1) continue
			const candidates = cells
				.slice(1)
				.filter((cell) => cell.dataset.label && cell.dataset.label !== '操作')
			const usageSummaryLabels = labels.includes('请求编号') ? new Set(['用户', '模型', '分组', '状态']) : null
			const summaryCells = usageSummaryLabels
				? candidates.filter((cell) => usageSummaryLabels.has(cell.dataset.label || '')).map((cell) => ({ cell }))
				: candidates
					.map((cell, index) => ({ cell, index, priority: SUMMARY_PRIORITY.get(cell.dataset.label || '') || 0 }))
					.sort((left, right) => right.priority - left.priority || left.index - right.index)
					.slice(0, 2)
			for (const { cell } of summaryCells) cell.dataset.mobileSummary = 'true'
			ensureExpandButton(row, cells[0])
		}
	}
}

export const responsiveTable: Directive<HTMLTableElement> = {
	mounted: syncMobileLabels,
	updated: syncMobileLabels,
}
