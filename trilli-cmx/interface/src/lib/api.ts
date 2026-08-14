// Minimal fetch wrapper for the CMX internal API (/api/cmx/*). Sends the
// session cookie (same-origin), parses JSON, and surfaces server error messages.

export class ApiError extends Error {
  status: number;
  code?: string;
  constructor(message: string, status: number, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const d = (data ?? {}) as { error?: string; code?: string };
    throw new ApiError(d.error || `Request failed (${res.status})`, res.status, d.code);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
};

// ---- Domain types ----

export type Role = "global" | "cmx";

export interface Operator {
  id: number;
  email: string;
  name: string;
  role: Role;
  status: string;
  geofence_enabled: boolean;
  last_login_at?: string;
  created_at: string;
}

export interface MeResponse {
  operator: Operator;
  step_up_fresh: boolean;
}

export type LoginStage = "totp" | "enroll";

export interface BeginLoginResult {
  challenge_id: string;
  stage: LoginStage;
}

export interface TOTPSetup {
  secret: string;
  otpauth_url: string;
  qr_data_url: string;
}

// ---- Directory: Customers + Accounts (SPEC §6.1/§6.2) ----

export type LifecycleStage = "lead" | "trial" | "paying" | "churned";

export interface CustomerListItem {
  identity_user_id: number;
  email: string;
  name: string;
  company: string;
  lifecycle_stage: LifecycleStage;
  account_count: number;
  status: string;
  created_at: string;
  last_login_at?: string;
}

export interface AccountBrief {
  tenant_id: number;
  name: string;
  role: string;
  member_status: string;
  plan_code: string;
  tenant_status: string;
  lifecycle_state: string;
}

export interface CustomerNote {
  id: number;
  operator_id: number;
  operator_email: string;
  body: string;
  created_at: string;
}

export interface CustomerDetail {
  identity_user_id: number;
  email: string;
  name: string;
  company: string;
  lifecycle_stage: LifecycleStage;
  status: string;
  email_verified: boolean;
  created_at: string;
  last_login_at?: string;
  owned_accounts: AccountBrief[];
  memberships: AccountBrief[] | null;
  notes: CustomerNote[] | null;
}

export interface TenantListItem {
  id: number;
  name: string;
  slug: string;
  status: string;
  lifecycle_state: string;
  plan_code: string;
  plan_name: string;
  storage_bytes_used: number;
  storage_bytes_max: number;
  user_count: number;
  extra_seats: number;
  subscription_status: string;
  owner_email: string;
  created_at: string;
}

export interface TenantMember {
  user_id: number;
  email: string;
  name: string;
  role: string;
  status: string;
  last_login_at?: string;
  joined_at?: string;
}

export interface WorkspaceRow {
  id: number;
  name: string;
  status: string;
  disk_allocation_bytes: number;
  storage_bytes_used: number;
}

export interface TenantDetail {
  id: number;
  name: string;
  slug: string;
  status: string;
  lifecycle_state: string;
  plan_code: string;
  plan_name: string;
  storage_bytes_used: number;
  storage_bytes_max: number;
  user_count: number;
  extra_seats: number;
  locked_price_cents: number;
  locked_billing_period: string;
  subscription_status: string;
  auto_renew: boolean;
  current_period_end?: string;
  scheduled_plan_code: string;
  lapsed_at?: string;
  purge_at?: string;
  stripe_customer_id: string;
  owner_email: string;
  created_at: string;
  members: TenantMember[];
  workspaces: WorkspaceRow[];
}

// ---- Catalog: Plans (SPEC §6.3) ----

export interface Plan {
  id: number;
  code: string;
  name: string;
  status: string; // draft | available | retired | archived
  tagline: string;
  callout: string;
  is_popular: boolean;
  sort_order: number;
  price_monthly_cents: number;
  annual_discount_pct: number;
  per_seat_cents?: number | null;
  min_seats: number;
  max_users?: number | null;
  max_storage_bytes?: number | null;
  max_transfer_bytes_month?: number | null;
  max_workspaces?: number | null;
  max_file_size_bytes?: number | null;
  max_share_expiry_days?: number | null;
  trash_retention_days?: number | null;
  api_access: boolean;
  support_level: number;
  marketing_lines: string[];
  stripe_product_id: string;
  stripe_price_monthly_id: string;
  stripe_price_annual_id: string;
  account_count: number;
  member_count: number;
  max_subscriptions?: number | null;
  available_from?: string | null;
  available_until?: string | null;
  remaining?: number | null;
}

// ---- Comp / ambassador (SPEC §6.10) ----

export interface CompInvite {
  id: number;
  email: string;
  plan_code: string;
  plan_name: string;
  free_term_days: number;
  status: string; // invited | registered | expired | revoked
  invite_expires_at: string;
  invited_by_email: string;
  promo_note: string;
  tenant_id?: number | null;
  tenant_name?: string | null;
  comp_expires_at?: string | null;
  created_at: string;
  registered_at?: string | null;
}

// ---- Revenue / billing (SPEC §6.4) ----

export interface RevenueOverview {
  mrr_cents: number;
  active_subscriptions: number;
  trialing_subscriptions: number;
  comp_accounts: number;
  past_due_count: number;
  pending_intents: number;
  collected_30d_cents: number;
  refunded_30d_cents: number;
  net_collected_cents: number;
}

export interface Subscription {
  tenant_id: number;
  name: string;
  owner_email: string;
  plan_code: string;
  plan_name: string;
  billing_mode: string; // paid | comp
  subscription_status: string; // active | trialing | past_due | unpaid | canceled | ''
  lifecycle_state: string;
  billing_period: string; // monthly | annual | ''
  monthly_cents: number;
  extra_seats: number;
  auto_renew: boolean;
  has_card_on_file: boolean;
  current_period_end?: string | null;
  comp_expires_at?: string | null;
  stripe_subscription_id: string;
}

export interface BillingTransaction {
  id: number;
  tenant_id: number;
  tenant_name: string;
  plan_code: string;
  billing_period: string;
  amount_cents: number; // negative for refunds
  currency: string;
  status: string; // succeeded | refunded
  order_number: string;
  receipt_number: string;
  receipt_url: string;
  stripe_payment_intent_id: string;
  created_at: string;
}

export interface PastDueAccount {
  tenant_id: number;
  name: string;
  owner_email: string;
  plan_code: string;
  subscription_status: string;
  lifecycle_state: string;
  monthly_cents: number;
  current_period_end?: string | null;
  lapsed_at?: string | null;
  purge_at?: string | null;
}

export interface RevenueSignupIntent {
  id: number;
  email: string;
  account_type: string;
  plan_code: string;
  billing_cycle: string;
  status: string; // pending_email | email_verified | paid
  oauth_provider: string;
  stripe_subscription_id: string;
  created_at: string;
  expires_at: string;
  paid_at?: string | null;
  verified_at?: string | null;
  stuck: boolean;
}

export interface CreditGrant {
  id: number;
  amount_cents: number;
  currency: string;
  reason: string;
  granted_by_email: string;
  created_at: string;
}

// ---- Support desk (SPEC §6.8) ----

export interface SupportTicket {
  id: number;
  number: string;
  tenant_id: number;
  tenant_name: string;
  requester_email: string;
  requester_name: string;
  subject: string;
  category: string;
  severity: string;
  status: string; // open | pending | awaiting_customer | resolved | closed
  message_count: number;
  last_activity_at: string;
  created_at: string;
}

export interface SupportMessage {
  id: number;
  author_type: string; // customer | agent | system
  author_name: string;
  body: string;
  is_internal: boolean;
  created_at: string;
}

export interface SupportTicketDetail extends SupportTicket {
  messages: SupportMessage[];
}

// ---- Notification consent ledger (SPEC §6.8) ----

export interface ConsentChange {
  id: number;
  prefs: string; // raw JSON snapshot
  ip: string;
  country: string;
  region: string;
  city: string;
  created_at: string;
}

// ---- Infrastructure (SPEC §6.5) ----

export interface InfraJob {
  job: string;
  last_node: string;
  last_started_at?: string | null;
  last_finished_at?: string | null;
  last_ok: boolean;
  last_note: string;
  run_count: number;
}

export interface InfraHealth {
  db_version: number;
  db_dirty: boolean;
  master_key_configured: boolean;
  geoip_ready: boolean;
  geoip_source: string;
  files_total: number;
  files_encrypted: number;
  files_legacy: number;
  hot_bytes: number;
  cool_bytes: number;
  cold_bytes: number;
  tiering_savings_usd: number;
  tiering_last_run?: string | null;
  tiering_last_ok: boolean;
  tiering_runs: number;
}

export interface InfraCostRow {
  tenant_id: number;
  tenant_name: string;
  bytes_in: number;
  bytes_out: number;
}

export interface InfraCost {
  period_start: string;
  total_bytes_in: number;
  total_bytes_out: number;
  top_tenants: InfraCostRow[] | null;
}

// ---- Administration (SPEC §6.7) ----

export interface AuditEntry {
  id: number;
  operator_id: number;
  operator_email: string;
  role_snapshot: string;
  action: string;
  target_type: string;
  target_id: string;
  tenant_id?: number | null;
  summary: string;
  meta: unknown;
  ip: string;
  country_code: string;
  region: string;
  created_at: string;
}

export interface VaultEntry {
  id: number;
  provider: string;
  key_name: string;
  environment: string; // test | live
  last4: string;
  is_active: boolean;
  updated_at: string;
}

// ---- Reports & Marketing (SPEC §6.6) ----

export interface PlanMix {
  code: string;
  name: string;
  subscribers: number;
  mrr_cents: number;
}

export interface ReportData {
  mrr_cents: number;
  arr_cents: number;
  arpu_cents: number;
  paying_accounts: number;
  trialing: number;
  comp: number;
  plan_mix: PlanMix[] | null;
  leads: number;
  intents_paid: number;
  churned: number;
  active_total: number;
  intents_completed: number;
  intents_terminal: number;
  conversion_pct: number;
  storage_cost_usd: number;
  gross_margin_usd: number;
}
