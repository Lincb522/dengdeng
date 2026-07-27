export type SortDirection = 'asc' | 'desc'
export type SortMeaning = 'text' | 'number' | 'time'

export function defaultSortDirection(meaning: SortMeaning): SortDirection {
  return meaning === 'text' ? 'asc' : 'desc'
}

export function sortDirectionLabel(direction: SortDirection, meaning: SortMeaning): string {
  if (meaning === 'time') return direction === 'asc' ? '最早在前' : '最新在前'
  if (meaning === 'text') return direction === 'asc' ? '正序排列' : '倒序排列'
  return direction === 'asc' ? '从低到高' : '从高到低'
}

export function sortDirectionOptions(meaning: SortMeaning): Array<{ value: SortDirection; label: string }> {
  return (['desc', 'asc'] as const).map((value) => ({ value, label: sortDirectionLabel(value, meaning) }))
}
