import type { Directive } from 'vue'

function syncMobileLabels(table: HTMLTableElement) {
	const headerRow = table.tHead?.rows.item(table.tHead.rows.length - 1)
	const labels = headerRow ? Array.from(headerRow.cells, (cell) => cell.textContent?.trim() || '') : []

	for (const body of Array.from(table.tBodies)) {
		for (const row of Array.from(body.rows)) {
			for (const cell of Array.from(row.cells)) {
				if (cell.colSpan > 1) {
					cell.removeAttribute('data-label')
					continue
				}
				cell.dataset.label = labels[cell.cellIndex] || ''
			}
		}
	}
}

export const responsiveTable: Directive<HTMLTableElement> = {
	mounted: syncMobileLabels,
	updated: syncMobileLabels,
}
