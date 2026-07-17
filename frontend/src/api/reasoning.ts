// GPT-5.6 reasoning.effort values, plus DengDeng's "auto" option. Auto does
// not inject a value and therefore follows an explicit client value or the
// model default.
export const OFFICIAL_REASONING_EFFORTS = ['none', 'low', 'medium', 'high', 'xhigh', 'max'] as const

export interface ReasoningOption {
  value: string
  label: string
}

export const REASONING_OPTIONS: ReasoningOption[] = [
  { value: 'auto', label: '自动 Auto · 跟随客户端/模型' },
  { value: 'none', label: '无 None' },
  { value: 'low', label: '低 Low' },
  { value: 'medium', label: '中 Medium' },
  { value: 'high', label: '高 High' },
  { value: 'xhigh', label: '极高 Extra High' },
  { value: 'max', label: '最大 Max' },
]

export function defaultReasoningMultipliers(): Record<string, number> {
  return {
    none: 0.8,
    low: 0.9,
    medium: 1,
    high: 1.25,
    xhigh: 1.5,
    max: 2,
  }
}

// Legacy releases exposed fast/minimal; GPT-5.6 uses low instead.
export function normalizeReasoningEffort(value: string | null | undefined): string {
  const normalized = (value || '').trim().toLowerCase()
  if (normalized === 'fast' || normalized === 'minimal') return 'low'
  if (OFFICIAL_REASONING_EFFORTS.includes(normalized as (typeof OFFICIAL_REASONING_EFFORTS)[number])) return normalized
  return 'auto'
}

export function reasoningLabel(value: string | null | undefined): string {
  const normalized = normalizeReasoningEffort(value)
  return REASONING_OPTIONS.find((option) => option.value === normalized)?.label || REASONING_OPTIONS[0].label
}
