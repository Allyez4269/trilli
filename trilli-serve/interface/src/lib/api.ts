// Thin fetch wrapper. The Go binary serves both the SPA and the API from the
// same origin, so we use relative paths and rely on the session cookie.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  return parseResponse<T>(res);
}

async function parseResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const j = await res.json();
      if (j?.error) msg = j.error;
    } catch {
      // ignore JSON parse error; fall back to statusText
    }
    // When an authenticated request comes back 401, the user's session
    // is gone — either it expired or it was revoked (e.g. another
    // user/admin just disabled this account). Kick them to /login so
    // they don't keep clicking around a UI that can't do anything.
    // We only redirect when we're not already on a public auth page
    // and not on the login endpoint itself (avoid redirect loops).
    if (
      res.status === 401 &&
      typeof window !== "undefined" &&
      // The session probe 401 is the normal "not logged in" state for a fresh
      // visitor (e.g. clicking "Log in" from the marketing site). AuthContext +
      // ProtectedRoute already route them to a clean /login — so this must NOT
      // flag a "session ended / disabled" notice. Only genuine 401s on other
      // authenticated requests (a session revoked mid-use) should.
      !res.url.includes("/api/me") &&
      !res.url.includes("/api/auth/login") &&
      !res.url.includes("/api/auth/signup") &&
      !window.location.pathname.startsWith("/login") &&
      !window.location.pathname.startsWith("/signup") &&
      !window.location.pathname.startsWith("/invite") &&
      // Public token pages: the visitor may legitimately have no session (a
      // password reset even revokes it). Don't bounce them to the "disabled"
      // login screen — let the page handle its own state.
      !window.location.pathname.startsWith("/reset-password") &&
      !window.location.pathname.startsWith("/confirm-email") &&
      // Public share landing + drop-portal upload pages — anonymous visitors
      // are expected (token-based access), so a background /api/me 401 must
      // not bounce them to the login screen.
      !window.location.pathname.startsWith("/s/") &&
      !window.location.pathname.startsWith("/p/")
    ) {
      // Carry a "session ended" notice across the hard redirect via
      // sessionStorage instead of a query string, so the URL stays a clean
      // /login. Login reads + clears this flag on mount. This is a generic
      // session-invalid signal (expired/revoked) — NOT "account disabled";
      // Login shows a neutral notice and lets the login attempt determine the
      // specific reason.
      try {
        sessionStorage.setItem("auth:session-ended", "1");
      } catch {
        // sessionStorage can throw in private-mode/edge cases — non-fatal.
      }
      window.location.assign("/login");
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

// saveFileContent replaces a file's bytes in-place (in-preview editors).
export async function saveFileContent(id: number, text: string, contentType = "application/json"): Promise<FileRecord> {
  const res = await fetch(`/api/files/${id}/content`, {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": contentType },
    body: text,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || "Couldn't save the file.");
  return data as FileRecord;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  patch: <T>(path: string, body: unknown) => request<T>("PATCH", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  delete: <T>(path: string, body?: unknown) => request<T>("DELETE", path, body),

  async uploadFiles(
    filesToUpload: File[],
    folderId: number | null,
    onProgress?: (p: UploadProgress) => void,
    workspaceId?: number | null,
  ): Promise<UploadResult> {
    // Upload one file at a time so we can report per-file progress via
    // XMLHttpRequest's upload events (fetch has no upload progress API).
    const uploaded: FileRecord[] = [];
    const total = filesToUpload.length;
    for (let i = 0; i < filesToUpload.length; i++) {
      const f = filesToUpload[i];
      const report = (loaded: number, totalBytes: number) =>
        onProgress?.({
          fileIndex: i + 1,
          totalFiles: total,
          fileName: f.name,
          bytesLoaded: loaded,
          bytesTotal: totalBytes,
        });
      try {
        // Large files go through the resumable chunked path so a dropped
        // connection resumes instead of restarting; small files use one POST.
        const r =
          f.size > RESUMABLE_THRESHOLD
            ? await uploadResumable(f, folderId, workspaceId ?? null, report)
            : await uploadOneFile(f, folderId, workspaceId ?? null, report);
        if (r.files) uploaded.push(...r.files);
      } catch (err) {
        const msg = err instanceof Error ? err.message : "Upload failed";
        return { uploaded, files: uploaded, error: msg, failed: f.name };
      }
    }
    return { uploaded, files: uploaded };
  },
};

function uploadOneFile(
  file: File,
  folderId: number | null,
  workspaceId: number | null,
  onProgress: (loaded: number, total: number) => void,
): Promise<UploadResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/files");
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded, e.total);
    };
    xhr.onerror = () => reject(new ApiError(0, "Network error"));
    xhr.onload = () => {
      let body: { error?: string; files?: FileRecord[] } = {};
      try {
        body = JSON.parse(xhr.responseText);
      } catch {
        // non-JSON response — fall through to status check
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new ApiError(xhr.status, body.error ?? xhr.statusText));
        return;
      }
      resolve(body as UploadResult);
    };
    // Field order matters: the server streams the multipart body and reads parts
    // in order, so metadata must precede the file part (the access check needs
    // folder_id/workspace_id before the file bytes start arriving). Keep "file" last.
    const fd = new FormData();
    if (folderId != null) fd.append("folder_id", String(folderId));
    if (workspaceId != null) fd.append("workspace_id", String(workspaceId));
    fd.append("file", file, file.name);
    xhr.send(fd);
  });
}

// ----- Resumable chunked upload (large files) -----
//
// Files larger than RESUMABLE_THRESHOLD are uploaded as a sequence of fixed-size
// chunks (server-driven size). Each chunk is PUT independently with retry, so a
// dropped connection resumes from the last landed chunk instead of restarting.
// The session token is persisted in localStorage keyed by file identity, so a
// page reload can resume too. Falls back to a single POST if the server reports
// resumable uploads unavailable (501).
const RESUMABLE_THRESHOLD = 16 * 1024 * 1024;

interface UploadSessionResp {
  upload_id: string;
  name: string;
  total_size_bytes: number;
  chunk_size_bytes: number;
  total_chunks: number;
  received_chunks: number[];
  completed: boolean;
}

const resumeKey = (f: File, folderId: number | null) =>
  `trilli.upload.${folderId ?? "root"}.${f.name}.${f.size}.${f.lastModified}`;

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

// Byte length of chunk `idx` (the last chunk is the remainder).
function chunkLen(idx: number, chunkSize: number, totalChunks: number, fileSize: number): number {
  return idx === totalChunks - 1 ? fileSize - idx * chunkSize : chunkSize;
}

// localStorage can throw (private mode / quota); resume is strictly best-effort.
function safeGet(k: string): string | null {
  try {
    return localStorage.getItem(k);
  } catch {
    return null;
  }
}
function safeSet(k: string, v: string) {
  try {
    localStorage.setItem(k, v);
  } catch {
    /* ignore */
  }
}
function safeDel(k: string) {
  try {
    localStorage.removeItem(k);
  } catch {
    /* ignore */
  }
}

async function uploadResumable(
  file: File,
  folderId: number | null,
  workspaceId: number | null,
  onProgress: (loaded: number, total: number) => void,
): Promise<UploadResult> {
  const key = resumeKey(file, folderId);

  // Resume a prior session for this exact file, if one is still valid.
  let session: UploadSessionResp | null = null;
  const saved = safeGet(key);
  if (saved) {
    session = await api.get<UploadSessionResp>(`/api/uploads/${saved}`).catch(() => null);
    if (session && (session.completed || session.total_size_bytes !== file.size)) {
      session = null; // stale/mismatched — start fresh
    }
  }

  // Otherwise open a new session (fall back to a single POST if unsupported).
  if (!session) {
    try {
      session = await api.post<UploadSessionResp>("/api/uploads/init", {
        name: file.name,
        content_type: file.type || "application/octet-stream",
        total_size_bytes: file.size,
        folder_id: folderId,
        workspace_id: workspaceId,
      });
    } catch (err) {
      if (err instanceof ApiError && err.status === 501) {
        return uploadOneFile(file, folderId, workspaceId, onProgress);
      }
      throw err;
    }
    safeSet(key, session.upload_id);
  }

  const token = session.upload_id;
  const chunkSize = session.chunk_size_bytes;
  const totalChunks = session.total_chunks;
  const done = new Set<number>(session.received_chunks);

  let baseBytes = 0;
  for (const idx of done) baseBytes += chunkLen(idx, chunkSize, totalChunks, file.size);
  onProgress(baseBytes, file.size);

  for (let idx = 0; idx < totalChunks; idx++) {
    if (done.has(idx)) continue;
    const start = idx * chunkSize;
    const end = Math.min(start + chunkSize, file.size);
    const base = baseBytes;
    await putChunkWithRetry(token, idx, file.slice(start, end), (loaded) =>
      onProgress(base + loaded, file.size),
    );
    baseBytes += end - start;
    onProgress(baseBytes, file.size);
  }

  const record = await api.post<FileRecord>(`/api/uploads/${token}/complete`);
  safeDel(key);
  return { uploaded: [record], files: [record] };
}

function putChunk(
  token: string,
  index: number,
  blob: Blob,
  onProgress: (loaded: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", `/api/uploads/${token}/chunk/${index}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded);
    };
    xhr.onerror = () => reject(new ApiError(0, "Network error"));
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
        return;
      }
      let msg = xhr.statusText;
      try {
        msg = JSON.parse(xhr.responseText).error ?? msg;
      } catch {
        /* non-JSON error body */
      }
      reject(new ApiError(xhr.status, msg));
    };
    xhr.send(blob);
  });
}

// Retry a chunk on transient failures (network blips / 5xx) with exponential
// backoff. Client errors (4xx) are not retryable and surface immediately.
async function putChunkWithRetry(
  token: string,
  index: number,
  blob: Blob,
  onProgress: (loaded: number) => void,
): Promise<void> {
  const maxRetries = 6;
  for (let attempt = 0; ; attempt++) {
    try {
      await putChunk(token, index, blob, onProgress);
      return;
    } catch (err) {
      const status = err instanceof ApiError ? err.status : 0;
      const retryable = status === 0 || status >= 500;
      if (!retryable || attempt >= maxRetries) throw err;
      onProgress(0); // rewind this chunk's partial progress on the bar
      await sleep(Math.min(1000 * 2 ** attempt, 15000));
    }
  }
}

// ----- Domain types -----

export interface User {
  id: number;
  email: string;
  first_name?: string | null;
  last_name?: string | null;
  full_name?: string | null;
  avatar_url?: string | null;
  is_superuser: boolean;
  status: string;
  created_at: string;
  last_login_at?: string;
}

export interface Tenant {
  id: number;
  name: string;
  slug: string;
  plan_id: number;
  plan_name: string;
  max_storage_bytes: number;
  api_access?: boolean;
  status: string;
  storage_bytes_used: number;
  user_count: number;
  created_at: string;
  locked_price_cents?: number | null; // snapshot-locked price at plan assignment
  locked_billing_period?: string | null; // 'monthly' | 'annual'
  lifecycle_state?: string; // 'current' | 'lapsed' (read-only before purge)
  purge_at?: string | null; // when a lapsed account's data is deleted
}

export interface Membership {
  tenant_id: number;
  role: string;
  status: string;
}

// TenantSummary is one account the user can access (owned or member-of), used
// by the account selector + in-app account switcher.
export interface TenantSummary {
  id: number;
  name: string;
  slug: string;
  role: string;
  plan_name: string;
  is_owner: boolean;
}

export interface Identity {
  user: User;
  tenant?: Tenant;
  membership?: Membership;
  // Inactivity window (seconds) after which the session auto-logs-out. Sourced
  // from the server (session.DefaultIdleTimeout) so the client matches it.
  idle_timeout_seconds?: number;
  // Every account this user can access. >1 ⇒ show the account selector/switcher.
  tenants?: TenantSummary[];
}

export interface Workspace {
  id: number;
  tenant_id: number;
  name: string;
  disk_allocation_bytes: number;
  storage_bytes_used: number;
  status: string;
  created_at: string;
}

export interface AccountQuota {
  total_bytes: number;
  allocated_bytes: number;
  remaining_bytes: number;
}

export interface WorkspacesResponse {
  workspaces: Workspace[];
  account?: AccountQuota;
}

export interface FileRecord {
  id: number;
  tenant_id: number;
  parent_folder_id?: number;
  name: string;
  size_bytes: number;
  content_type: string;
  // "trilli-sign" for envelope deposits — undeletable from Files
  protected_source?: string;
  uploaded_by_user_id: number;
  status: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
  // Populated only by the recent + search endpoints — looks like
  // "/Documents/Reports/" or "/" for root.
  path?: string;
  // ISO timestamp when the user starred this file. Absent = not starred.
  starred_at?: string | null;
}

export interface FolderRecord {
  id: number;
  tenant_id: number;
  parent_folder_id?: number;
  name: string;
  created_by_user_id: number;
  status: string;
  created_at: string;
  updated_at: string;
  starred_at?: string | null;
  workspace_id?: number;
  size_bytes: number;
  item_count: number;
  file_count: number; // files directly in this folder (this level only)
}

export interface BreadcrumbItem {
  id: number;
  name: string;
  protected_source?: string; // "trilli-sign" for system folders
}

export interface BrowseResponse {
  folder: FolderRecord | null;
  breadcrumb: BreadcrumbItem[] | null;
  folders: FolderRecord[];
  files: FileRecord[];
}

export interface SearchResponse {
  files: FileRecord[];
  query: string;
}

export interface UploadResult {
  files?: FileRecord[];
  error?: string;
  uploaded?: FileRecord[];
  failed?: string;
}

export interface RecentPage {
  files: FileRecord[];
  total: number;
}

export interface FileStats {
  total: number;
  by_extension: Record<string, number>;
}

// ----- Invite types --------------------------------------------------------

export interface InviteLookup {
  token: string;
  tenant_id: number;
  tenant_name: string;
  email: string;
  role: string;
  inviter_email: string;
  expires_at: string;
  email_has_account: boolean;
}

export interface InvitePayload {
  id: number;
  email: string;
  role: string;
  token: string;
  link: string;
  status: string;
  expires_at: string;
}

export interface InviteCreateResponse {
  invites: InvitePayload[];
}

export interface PendingInvitesResponse {
  invites: InvitePayload[];
}

export interface InviteAcceptResponse {
  tenant_id: number;
  role: string;
  is_new_user: boolean;
  // True when the account has 2FA enrolled: the membership was created but no
  // session was issued — the user must sign in (with their second factor).
  twofa_required?: boolean;
}

export interface UploadProgress {
  fileIndex: number; // 1-based current file
  totalFiles: number;
  fileName: string;
  bytesLoaded: number;
  bytesTotal: number;
  // Aggregate across a multi-file batch (folder uploads) — when present the
  // modal leads with the overall bar instead of the per-file one.
  overallLoaded?: number;
  overallTotal?: number;
}

// One UTC day's metrics within a span — powers the tile sparklines.
export interface StatsDayPoint {
  day: string; // YYYY-MM-DD
  transfer: number; // bytes in+out
  files: number;
  members: number;
}

// Quick-stats summary for a date span (GET /api/stats?from=&to=).
export interface StatsSummary {
  transfer_in: number;
  transfer_out: number;
  files_added: number;
  members_joined: number;
  series?: StatsDayPoint[];
}

export interface Quota {
  plan_name: string;
  storage_bytes_used: number;
  max_storage_bytes: number;
  max_file_size_bytes: number;
  // Current calendar-month transfer (bandwidth). transfer_cap_bytes is absent
  // when the plan has unlimited transfer.
  transfer_used_bytes: number;
  transfer_cap_bytes?: number;
}

export function downloadUrl(fileId: number, version?: string): string {
  return `/api/files/${fileId}/download${version ? `?v=${encodeURIComponent(version)}` : ""}`;
}

// previewUrl serves the same bytes inline (Content-Disposition: inline) for the
// in-app viewer — images/PDF render in the browser instead of downloading, and
// it isn't logged as a download. The optional `version` param (file.updated_at)
// cache-busts so an overwritten file shows fresh content.
export function previewUrl(fileId: number, version?: string): string {
  const q = version ? `?inline=1&v=${encodeURIComponent(version)}` : "?inline=1";
  return `/api/files/${fileId}/download${q}`;
}

// previewPdfUrl serves a PDF rendering of an Office document (Word/Excel/
// PowerPoint) for in-app preview. The server converts on first request and
// caches the result; the original file is never modified.
export function previewPdfUrl(fileId: number): string {
  return `/api/files/${fileId}/preview.pdf`;
}

// ----- Sharing -------------------------------------------------------------

export interface Share {
  id: number;
  token: string;
  url: string; // relative, e.g. /s/<token>
  resource_type: "file" | "folder";
  file_id?: number;
  folder_id?: number;
  resource_name: string;
  grantee_type: "member" | "email" | "public";
  grantee_user_id?: number;
  grantee_email?: string;
  grantee_name?: string;
  capability: "view" | "download";
  has_password: boolean;
  expires_at?: string | null;
  created_by_id: number;
  created_by_name?: string;
  created_at: string;
  revoked_at?: string | null;
  last_accessed_at?: string | null;
  access_count: number;
  direction: "out" | "in";
}

export interface CreateShareInput {
  resource_type: "file" | "folder";
  file_id?: number;
  folder_id?: number;
  grantee_type: "member" | "email" | "public";
  grantee_user_id?: number;
  grantee_email?: string;
  capability?: "view" | "download";
  expires_in_days?: number | null;
  password?: string;
  notify?: boolean;
}

export interface ShareRecipient {
  user_id: number;
  name: string;
  email: string;
}

// A drop portal (file request): a public upload point into a folder.
export interface Portal {
  id: number;
  token: string;
  url: string; // relative /p/<token>
  folder_id: number;
  folder_name: string;
  title: string;
  has_password: boolean;
  notify_on_upload: boolean;
  expires_at?: string | null;
  created_by_id: number;
  created_by_name?: string;
  created_at: string;
  revoked_at?: string | null;
  upload_count: number;
  last_upload_at?: string | null;
}

// Public portal metadata (uploader-side, no auth).
export interface PortalMeta {
  title: string;
  folder_name: string;
  has_password: boolean;
}

// Public share metadata (recipient-side, no auth).
export interface ShareMeta {
  resource_type: "file" | "folder";
  resource_name: string;
  capability: "view" | "download";
  has_password: boolean;
}

export interface ShareBrowseEntry {
  id: number;
  kind: "file" | "folder";
  name: string;
  size_bytes: number;
  modified: string;
}

// ----- Plan catalog --------------------------------------------------------

export interface CatalogPlan {
  id: number;
  code: string;
  name: string;
  icon: string; // icon key — mapped to a lucide component in the UI
  callout: string;
  tagline: string;
  price_monthly: number; // dollars
  annual_discount_pct: number;
  popular: boolean;
  features: string[];
  included_seats: number | null; // seats bundled with the plan (null = unlimited)
  per_seat_monthly: number | null; // dollars/mo per extra seat (null = not sold)
}

export interface PlansResponse {
  plans: CatalogPlan[];
  annual_discount_pct: number; // catalog-level (max across plans) for the toggle badge
}

// A saved card from the tenant's Stripe customer (brand/last4/exp only — never the PAN).
export interface SavedCard {
  id: string;
  brand: string;
  last4: string;
  exp_month: number;
  exp_year: number;
  default: boolean;
}

export interface PaymentMethodsResponse {
  enabled: boolean;
  has_card: boolean;
  cards: SavedCard[];
}

// Our order record for a completed purchase. order_number is our friendly
// reference (TRL-YYYY-NNNNNN); receipt_url is Stripe's hosted receipt.
export interface BillingTransaction {
  order_number: string;
  plan_code: string;
  billing_period: string;
  amount_cents: number;
  currency: string;
  receipt_url: string;
  receipt_number: string;
  created_at: string;
}

export interface SubscriptionSummary {
  status: string; // active, past_due, canceled, ""
  current_period_end: string | null;
  auto_renew: boolean;
  past_due: boolean;
  scheduled_plan: string; // pending downgrade plan code ("" if none)
  scheduled_plan_name: string;
  scheduled_change_at: string | null;
  lifecycle_state: string; // 'current' | 'lapsed'
  purge_at: string | null; // when a lapsed account's data is deleted
  over_quota: boolean; // usage exceeds current plan caps (downgrade grace)
  storage_over: boolean;
  seats_over: boolean;
}

// Result of POST /api/billing/change-plan.
export interface ChangePlanResult {
  needs_checkout?: boolean;
  effect?: "upgraded" | "scheduled";
  plan_name?: string;
  effective_at?: string;
}

// Result of POST /api/billing/preview-change — the read-only summary shown in
// the confirmation modal before a plan change is committed.
export interface ChangePreviewResult {
  needs_checkout?: boolean;
  effect?: "upgraded" | "scheduled";
  plan_name?: string;
  current_plan_name?: string;
  currency?: string;
  due_today_cents?: number;
  next_renewal_cents?: number;
  next_renewal_at?: string;
  effective_at?: string;
  billing_period?: "monthly" | "annual";
}

// GET /api/billing/seats — seat-usage snapshot for the Users-area meter.
export interface SeatSummary {
  used: number;
  included: number | null; // seats bundled with the plan (null = unlimited)
  extra: number; // purchased add-on seats
  total: number | null; // included + extra (null = unlimited)
  per_seat_monthly: number | null; // dollars/mo per extra seat (null = not sold)
  currency: string;
  plan_code: string;
  plan_name: string;
  sellable: boolean; // more seats can be purchased
}

// POST /api/billing/preview-seats — prorated cost of adding N seats.
export interface SeatPreviewResult {
  needs_checkout?: boolean;
  quantity?: number;
  plan_name?: string;
  currency?: string;
  due_today_cents?: number;
  next_renewal_cents?: number;
  next_renewal_at?: string;
  billing_period?: "monthly" | "annual";
}

export interface OpenInvoice {
  id: string;
  hosted_invoice_url: string;
  amount_cents: number;
  currency: string;
}

// Everything the Billing settings tab renders, from GET /api/billing/overview.
export interface BillingOverview {
  enabled: boolean;
  auto_renew: boolean;
  cards: SavedCard[];
  transactions: BillingTransaction[];
  subscription: SubscriptionSummary;
  open_invoice?: OpenInvoice;
}

export interface FileListPayload {
  files: FileRecord[];
}
