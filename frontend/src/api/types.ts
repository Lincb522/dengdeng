export interface User {
  id: number
  email: string
  role: 'user' | 'admin'
  status: string
  balance_micro: number
  access_expires_at: string | null
  remaining_requests: number
  rate_multiplier: number
  note?: string
	terms_revision?: string
	terms_accepted_at?: string | null
  created_at: string
}

export interface LegalDocument {
	id: string
	title: string
	content_md: string
}

export interface LoginAgreement {
	enabled: boolean
	mode: 'modal' | 'checkbox'
	updated_at: string
	revision: string
	documents: LegalDocument[]
}

export interface PublicSettings {
	site_name: string
	site_subtitle: string
	allow_register: boolean
	registration_verification: boolean
	login_agreement: LoginAgreement
}

export interface SystemSettings {
	site_name: string
	site_subtitle: string
	allow_register: boolean
	registration_email_suffixes: string[]
	init_balance_micro: number
	login_agreement: Omit<LoginAgreement, 'revision'>
	site_public_url?: string
	smtp_configured?: boolean
	smtp_from_name?: string
	smtp_from?: string
}

export interface GatewayRuntimePolicy {
	max_attempts: number
	unauthorized_cooldown_seconds: number
	rate_limit_cooldown_seconds: number
	upstream_failure_cooldown_seconds: number
	network_failure_cooldown_seconds: number
	probe_interval_seconds: number
	probe_timeout_seconds: number
	probe_retention_days: number
	probe_concurrency: number
	reasoning_effort_multipliers: Record<string, number>
}

export interface AuditLog {
	id: number
	actor_user_id: number
	actor_email: string
	action: string
	target_type: string
	target_id: string
	detail: string
	source_ip: string
	created_at: string
}

export interface AlertRule {
	id: number
	name: string
	enabled: boolean
	condition: 'down' | 'degraded_or_down' | 'not_healthy'
	platform: '' | 'anthropic' | 'openai' | 'gemini' | 'grok'
	group_id: number
	account_id: number
	notify_email: string
	created_at: string
	updated_at: string
}

export interface AlertEvent {
	id: number
	rule_id: number
	account_id: number
	group_id: number
	platform: string
	state: 'open' | 'resolved'
	severity: 'warning' | 'critical'
	title: string
	message: string
	first_seen_at: string
	last_seen_at: string
	resolved_at: string | null
	acknowledged_at: string | null
	acknowledged_by: string
	delivery_status: 'console' | 'sent' | 'failed'
	delivery_error: string
	rule_name: string
	account_name: string
}

export interface ChannelProbe {
	id: number
	account_id: number
	mode: 'api' | 'transport' | 'local'
	state: 'healthy' | 'degraded' | 'down' | 'expired'
	status_code: number
	latency_ms: number
	error_message: string
	checked_at: string
	account_name: string
	group_name: string
	platform: string
}

export interface BackupRecord {
	id: number
	filename: string
	status: 'creating' | 'ready' | 'failed'
	size_bytes: number
	error: string
	created_by: string
	created_at: string
	completed_at: string | null
}

export interface UserGroupRate {
	id: number
	user_id: number
	group_id: number
	rate_multiplier: number
}

export interface ReferralCodeStats {
	id: number
	code: string
	owner_user_id: number
	owner_email: string
	commission_bps: number
	status: string
	referred_users: number
	commission_micro: number
	created_at: string
}

export interface ReferralBindingInfo {
	code: string
	referrer_email: string
	bound_at: string
}

export interface ReferralCommission {
	id: number
	usage_log_id: number
	referral_code_id: number
	referrer_user_id: number
	referred_user_id: number
	referred_email: string
	code: string
	base_cost_micro: number
	commission_bps: number
	amount_micro: number
	created_at: string
}

export interface ReferralDashboard {
	binding: ReferralBindingInfo | null
	codes: ReferralCodeStats[]
	commissions: ReferralCommission[]
	total_commission_micro: number
}

export interface Group {
  id: number
  name: string
  platform: 'anthropic' | 'openai' | 'gemini' | 'grok'
  description: string
  rate_multiplier: number
	cache_read_multiplier: number
	cache_write_5m_multiplier: number
	cache_write_1h_multiplier: number
	image_rate_independent: boolean
	image_rate_multiplier: number
  is_public: boolean
  status: string
  account_total?: number
  account_alive?: number
}

export interface ApiKey {
  id: number
  user_id: number
  group_id: number
  key_preview: string
  name: string
  status: string
	reasoning_effort: string
	quota_micro: number
	quota_used_micro: number
	daily_quota_micro: number
	rpm: number
	allowed_ips: string
	blocked_ips: string
	expires_at: string | null
  last_used_at: string | null
  created_at: string
  group?: Group
}

export interface UpstreamAccount {
  id: number
  group_id: number
	proxy_id: number
  name: string
  platform: string
  base_url: string
  auth_type: 'api_key' | 'oauth'
  expires_at: string | null
  email: string
  account_id: string
  priority: number
  // Console-only display position. Gateway scheduling continues to use
  // `priority`, so moving a card never changes which account receives traffic.
  display_order: number
  status: string
  error_count: number
  cooldown_until: string | null
  last_used_at: string | null
  last_error: string
  created_at: string
	group?: Group
	proxy?: Proxy
	quota?: AccountQuotaSnapshot
	codex_quota?: CodexQuotaSnapshot
}

export interface AccountQuotaWindow {
	key: string
	label: string
	used_percent?: number
	limit?: number
	remaining?: number
	unit?: string
	reset_at?: string | null
}

export interface AccountObservedUsage {
	key: string
	label: string
	requests: number
	input_tokens: number
	output_tokens: number
	cost_micro: number
}

// Unified allowance snapshot for every upstream account. Subscription OAuth
// accounts expose provider windows; API keys always retain locally observed
// request/token usage and any rate-limit headers returned by the provider.
export interface AccountQuotaSnapshot {
	id: number
	upstream_account_id: number
	platform: string
	source: 'codex_subscription' | 'claude_subscription' | 'grok_billing' | 'rate_limit_headers' | 'local_observed' | string
	state: 'ready' | 'partial' | 'local_only' | 'error'
	plan_type: string
	message: string
	windows: AccountQuotaWindow[]
	observed_usage: AccountObservedUsage[]
	fetched_at?: string | null
	last_attempt_at: string
	last_credential_refresh?: string | null
	updated_at: string
}

// The subscription windows reported by ChatGPT/Codex for an OAuth account.
// These are provider-side message/rate allowances, not DengDeng billing funds.
export interface CodexQuotaSnapshot {
  id: number
  upstream_account_id: number
  plan_type: string
  allowed: boolean
  limit_reached: boolean
  has_primary_window: boolean
  primary_used_percent: number
  primary_window_seconds: number
  primary_reset_after_seconds: number
  primary_reset_at: string | null
  has_secondary_window: boolean
  secondary_used_percent: number
  secondary_window_seconds: number
  secondary_reset_after_seconds: number
  secondary_reset_at: string | null
  fetched_at: string
  updated_at: string
}

export interface Proxy {
	id: number
	name: string
	protocol: 'http' | 'https' | 'socks5'
	host: string
	port: number
	status: string
	auth_configured: boolean
	account_count: number
	created_at: string
	updated_at: string
}

export interface ModelPrice {
  id: number
  match: string
  platform: string
  input_price: number
  output_price: number
  cache_read_price: number
  cache_write_price: number
	cache_write_5m_price: number
	cache_write_1h_price: number
  image_input_price: number
  image_output_price: number
  image_cache_read_price: number
	image_price_per_image: number
}

export interface ModelConfig {
  id: number
  name: string
  platform: string
  kind: 'chat' | 'image'
  upstream_model: string
	context_window: number
	max_output_tokens: number
	supports_vision: boolean
	supports_tools: boolean
	supports_reasoning: boolean
	image_group_id: number
  description: string
  status: string
}

export interface ModelCatalogueGroup {
	id: number
	name: string
	platform: string
	rate_multiplier: number
	image_rate_independent: boolean
	image_rate_multiplier: number
	ready: boolean
}

export interface ModelCatalogueItem extends ModelConfig {
	available: boolean
	groups: ModelCatalogueGroup[]
	pricing?: ModelPrice
}

export interface UsageLog {
  id: number
	request_id: string
  user_id: number
  api_key_id: number
  account_id: number
  group_id: number
  model: string
  stream: boolean
	reasoning_effort?: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
	cache_write_5m_tokens: number
	cache_write_1h_tokens: number
	image_count: number
  cost_micro: number
  duration_ms: number
  status_code: number
  error_message: string
  created_at: string
  user_email?: string
  key_name?: string
  group_name?: string
  account_name?: string
}

export interface OpsAggregate {
  requests: number
  success_requests: number
  error_requests: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
	cache_write_5m_tokens: number
	cache_write_1h_tokens: number
  cost_micro: number
  average_latency_ms: number
}

export interface OpsWindow {
  requests: number
  success_rate: number
  error_rate: number
  tokens: number
  cost_micro: number
  requests_per_minute: number
	requests_per_second: number
	tokens_per_second: number
  average_latency_ms: number
}

export interface OpsOverview extends OpsAggregate {
  total_tokens: number
  success_rate: number
  error_rate: number
  p50_latency_ms: number
  p95_latency_ms: number
  health_score: number
  account_total: number
  account_available: number
  account_cooling: number
  account_attention: number
  account_disabled: number
  last_5_minutes: OpsWindow
}

export interface OpsTrend {
  start: string
  end: string
  label: string
  requests: number
  success_requests: number
  error_requests: number
  tokens: number
  cost_micro: number
  average_latency_ms: number
}

export interface OpsRank {
  id?: number
  name: string
  requests: number
  success_requests: number
  error_requests: number
  tokens: number
	input_tokens: number
	output_tokens: number
	cache_read_tokens: number
	cache_write_tokens: number
	cache_write_5m_tokens: number
	cache_write_1h_tokens: number
  cost_micro: number
  average_latency_ms: number
}

export interface OpsRateProfile {
	id: number
	name: string
	platform: string
	rate_multiplier: number
	cache_read_multiplier: number
	cache_write_5m_multiplier: number
	cache_write_1h_multiplier: number
	image_rate_independent: boolean
	image_rate_multiplier: number
}

export interface OpsLiveCount {
	scope: 'platform' | 'group' | 'account'
	id?: number
	name: string
	in_flight: number
}

export interface OpsRealtime {
	captured_at: string
	in_flight: number
	last_minute: OpsWindow
	breakdown: OpsLiveCount[]
}

export interface OpsAccountHealth {
  id: number
  name: string
  email?: string
  group_id: number
  group_name: string
  platform: string
  status: string
  health: 'ready' | 'checking' | 'stale' | 'cooling' | 'attention' | 'disabled'
  error_count: number
  cooldown_until?: string
  last_used_at?: string
  last_error?: string
	probe_state: 'healthy' | 'degraded' | 'down' | 'expired' | ''
	probe_mode?: 'api' | 'transport'
	probe_status_code?: number
	probe_latency_ms?: number
	probe_checked_at?: string
	probe_error?: string
}

export interface OpsSystemMetrics {
  uptime_seconds: number
  goroutines: number
  memory_alloc_bytes: number
  heap_in_use_bytes: number
  db_open_connections: number
  db_in_use: number
  db_idle: number
  db_wait_count: number
}

export interface OpsSnapshot {
  generated_at: string
  range: string
  start: string
  end: string
  platform?: string
  group_id?: number
  overview: OpsOverview
  trend: OpsTrend[]
  top_models: OpsRank[]
  top_groups: OpsRank[]
  top_users: OpsRank[]
  top_accounts: OpsRank[]
	model_usage: OpsRank[]
	rate_profiles: OpsRateProfile[]
	realtime: OpsRealtime
  account_health: OpsAccountHealth[]
  recent_errors: UsageLog[]
  system: OpsSystemMetrics
  sample_truncated: boolean
}

export interface RedeemCode {
  id: number
  code: string
  kind: 'amount' | 'days' | 'requests' | ''
  amount_micro: number
  value: number
  batch: string
  used_by: number | null
  used_at: string | null
  used_by_email?: string
  created_at: string
}

export interface SummaryRow {
  requests: number
  input_tokens: number
  output_tokens: number
  cost_micro: number
}

export interface DailyRow {
  day: string
  requests: number
  tokens: number
  cost_micro: number
}

export interface UsageSummary {
  today: SummaryRow
  month: SummaryRow
  daily: DailyRow[]
  counts?: { users: number; groups: number; accounts: number; keys: number }
}

export interface PaymentCheckoutInfo {
  enabled: boolean
  currency: string
  credit_micro_per_unit: number
  min_amount_minor: number
  max_amount_minor: number
  daily_limit_minor: number
  order_expiry_minutes: number
  max_pending_orders: number
  product_name: string
  methods: string[]
}

export interface PaymentCheckout {
  trade_no?: string
  pay_url?: string
  qr_code?: string
  client_secret?: string
  intent_id?: string
  publishable_key?: string
  payment_env?: string
  country_code?: string
}

export interface PaymentOrder {
  id: number
  out_trade_no: string
  provider_key: string
  payment_method: string
  status: string
  currency: string
  amount_minor: number
  credit_micro: number
  expires_at: string
  paid_at?: string
  completed_at?: string
  cancelled_at?: string
  checkout?: PaymentCheckout
  failure_reason?: string
  created_at: string
}

export interface PaymentProvider {
  id: number
  name: string
  provider_key: string
  currency: string
  supported_methods: string
  payment_mode: string
  status: string
  min_amount_minor: number
  max_amount_minor: number
  daily_limit_minor: number
  priority: number
  last_selected_at?: string
  created_at: string
  updated_at: string
}

export function formatMoney(micro: number): string {
  return `$${(micro / 1_000_000).toFixed(4)}`
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

export const PLATFORM_LABELS: Record<string, string> = {
  anthropic: 'Claude',
  openai: 'OpenAI',
  gemini: 'Gemini',
  grok: 'Grok',
}
