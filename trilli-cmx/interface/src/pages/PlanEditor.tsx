import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

import { api, ApiError, type Plan } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock } from "@/components/DataState";
import StepUpModal from "@/components/StepUpModal";

const GB = 1024 * 1024 * 1024;
const MB = 1024 * 1024;

// Form state mirrors the editable plan fields, but storage/transfer are entered
// in GB, file size in MB, and prices in dollars — converted to the API's
// bytes/cents on submit. Empty allowance fields mean Unlimited (null).
interface Form {
  code: string;
  name: string;
  tagline: string;
  callout: string;
  priceMonthly: string; // dollars
  annualDiscountPct: string;
  perSeat: string; // dollars (blank = not sold)
  minSeats: string;
  maxUsers: string; // blank = unlimited
  storageGB: string;
  transferGB: string;
  maxWorkspaces: string;
  fileSizeMB: string;
  shareExpiryDays: string;
  trashRetentionDays: string;
  apiAccess: boolean;
  supportLevel: string;
  isPopular: boolean;
  sortOrder: string;
  marketing: string; // newline-separated
  maxSubscriptions: string; // blank = unlimited inventory
  availableFrom: string; // datetime-local, blank = always
  availableUntil: string; // datetime-local, blank = no expiry
}

const EMPTY: Form = {
  code: "", name: "", tagline: "", callout: "", priceMonthly: "", annualDiscountPct: "0",
  perSeat: "", minSeats: "1", maxUsers: "", storageGB: "", transferGB: "", maxWorkspaces: "",
  fileSizeMB: "", shareExpiryDays: "", trashRetentionDays: "", apiAccess: false,
  supportLevel: "1", isPopular: false, sortOrder: "0", marketing: "",
  maxSubscriptions: "", availableFrom: "", availableUntil: "",
};

// toLocalInput converts an ISO timestamp to a value for <input type=datetime-local>.
function toLocalInput(iso?: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function planToForm(p: Plan): Form {
  const div = (n: number | null | undefined, by: number) => (n == null ? "" : String(Math.round(n / by)));
  return {
    code: p.code,
    name: p.name,
    tagline: p.tagline,
    callout: p.callout,
    priceMonthly: p.price_monthly_cents ? String(p.price_monthly_cents / 100) : "0",
    annualDiscountPct: String(p.annual_discount_pct),
    perSeat: p.per_seat_cents ? String(p.per_seat_cents / 100) : "",
    minSeats: String(p.min_seats),
    maxUsers: p.max_users == null ? "" : String(p.max_users),
    storageGB: div(p.max_storage_bytes, GB),
    transferGB: div(p.max_transfer_bytes_month, GB),
    maxWorkspaces: p.max_workspaces == null ? "" : String(p.max_workspaces),
    fileSizeMB: div(p.max_file_size_bytes, MB),
    shareExpiryDays: p.max_share_expiry_days == null ? "" : String(p.max_share_expiry_days),
    trashRetentionDays: p.trash_retention_days == null ? "" : String(p.trash_retention_days),
    apiAccess: p.api_access,
    supportLevel: String(p.support_level),
    isPopular: p.is_popular,
    sortOrder: String(p.sort_order),
    marketing: (p.marketing_lines ?? []).join("\n"),
    maxSubscriptions: p.max_subscriptions == null ? "" : String(p.max_subscriptions),
    availableFrom: toLocalInput(p.available_from),
    availableUntil: toLocalInput(p.available_until),
  };
}

// numOrNull turns a blank field into null and a value into a scaled integer.
function numOrNull(v: string, scale = 1): number | null {
  if (v.trim() === "") return null;
  const n = Number(v);
  if (isNaN(n)) return null;
  return Math.round(n * scale);
}

function formToPayload(f: Form): Record<string, unknown> {
  return {
    code: f.code.trim().toLowerCase(),
    name: f.name.trim(),
    tagline: f.tagline,
    callout: f.callout,
    is_popular: f.isPopular,
    sort_order: Number(f.sortOrder) || 0,
    price_monthly_cents: Math.round((Number(f.priceMonthly) || 0) * 100),
    annual_discount_pct: Number(f.annualDiscountPct) || 0,
    per_seat_cents: numOrNull(f.perSeat, 100),
    min_seats: Number(f.minSeats) || 0,
    max_users: numOrNull(f.maxUsers),
    max_storage_bytes: numOrNull(f.storageGB, GB),
    max_transfer_bytes_month: numOrNull(f.transferGB, GB),
    max_workspaces: numOrNull(f.maxWorkspaces),
    max_file_size_bytes: numOrNull(f.fileSizeMB, MB),
    max_share_expiry_days: numOrNull(f.shareExpiryDays),
    trash_retention_days: numOrNull(f.trashRetentionDays),
    api_access: f.apiAccess,
    support_level: Number(f.supportLevel) || 0,
    marketing_lines: f.marketing.split("\n").map((s) => s.trim()).filter(Boolean),
    max_subscriptions: f.maxSubscriptions.trim() === "" ? null : Math.round(Number(f.maxSubscriptions)),
    available_from: f.availableFrom ? new Date(f.availableFrom).toISOString() : null,
    available_until: f.availableUntil ? new Date(f.availableUntil).toISOString() : null,
  };
}

export default function PlanEditor() {
  const { id } = useParams();
  const isEdit = !!id;
  const navigate = useNavigate();
  const { operator } = useAuth();
  const [form, setForm] = useState<Form>(EMPTY);
  const [loading, setLoading] = useState(isEdit);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [showStepUp, setShowStepUp] = useState(false);

  useEffect(() => {
    if (!isEdit) return;
    api
      .get<Plan>(`/api/cmx/plans/${id}`)
      .then((p) => setForm(planToForm(p)))
      .catch((e) => setLoadErr(e instanceof ApiError ? e.message : "Failed to load plan"))
      .finally(() => setLoading(false));
  }, [id, isEdit]);

  if (operator?.role !== "global") {
    return <ErrorBlock message="Plan editing is restricted to Global admins." />;
  }
  if (loading) return <LoadingBlock />;
  if (loadErr) return <ErrorBlock message={loadErr} />;

  async function save() {
    const payload = formToPayload(form);
    if (isEdit) {
      await api.patch(`/api/cmx/plans/${id}`, payload);
    } else {
      const res = await api.post<{ id: number }>("/api/cmx/plans", payload);
      navigate(`/catalog/${res.id}`, { replace: true });
      return;
    }
    navigate(`/catalog/${id}`, { replace: true });
  }

  const set = (k: keyof Form, v: string | boolean) => setForm((f) => ({ ...f, [k]: v }));

  return (
    <div className="mx-auto max-w-[61.2rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate(isEdit ? `/catalog/${id}` : "/catalog")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> {isEdit ? "Plan" : "Catalog"}
      </button>

      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        {isEdit ? "Edit plan" : "New plan"}
      </h1>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Identity & presentation</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-4">
          <Field label="Code (immutable)" hint={isEdit ? "Can't change after creation" : "lowercase, unique"}>
            <Input value={form.code} disabled={isEdit} onChange={(e) => set("code", e.target.value)} className="h-8" />
          </Field>
          <Field label="Name">
            <Input value={form.name} onChange={(e) => set("name", e.target.value)} className="h-8" />
          </Field>
          <Field label="Tagline">
            <Input value={form.tagline} onChange={(e) => set("tagline", e.target.value)} className="h-8" />
          </Field>
          <Field label="Callout">
            <Input value={form.callout} onChange={(e) => set("callout", e.target.value)} className="h-8" />
          </Field>
          <Field label="Sort order">
            <Input type="number" value={form.sortOrder} onChange={(e) => set("sortOrder", e.target.value)} className="h-8" />
          </Field>
          <div className="flex items-end gap-4">
            <Check label="Popular" checked={form.isPopular} onChange={(v) => set("isPopular", v)} />
            <Check label="API access" checked={form.apiAccess} onChange={(v) => set("apiAccess", v)} />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Pricing & seats</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-3 gap-4">
          <Field label="Monthly ($)"><Input type="number" value={form.priceMonthly} onChange={(e) => set("priceMonthly", e.target.value)} className="h-8" /></Field>
          <Field label="Annual discount (%)"><Input type="number" value={form.annualDiscountPct} onChange={(e) => set("annualDiscountPct", e.target.value)} className="h-8" /></Field>
          <Field label="Extra seat ($/mo)" hint="blank = not sold"><Input type="number" value={form.perSeat} onChange={(e) => set("perSeat", e.target.value)} className="h-8" /></Field>
          <Field label="Min seats"><Input type="number" value={form.minSeats} onChange={(e) => set("minSeats", e.target.value)} className="h-8" /></Field>
          <Field label="Included seats" hint="blank = unlimited"><Input type="number" value={form.maxUsers} onChange={(e) => set("maxUsers", e.target.value)} className="h-8" /></Field>
          <Field label="Support" hint="0 std · 1 priority · 2 dedicated"><Input type="number" value={form.supportLevel} onChange={(e) => set("supportLevel", e.target.value)} className="h-8" /></Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Allowances (blank = unlimited)</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-3 gap-4">
          <Field label="Storage (GB)"><Input type="number" value={form.storageGB} onChange={(e) => set("storageGB", e.target.value)} className="h-8" /></Field>
          <Field label="Transfer/mo (GB)"><Input type="number" value={form.transferGB} onChange={(e) => set("transferGB", e.target.value)} className="h-8" /></Field>
          <Field label="Max file (MB)"><Input type="number" value={form.fileSizeMB} onChange={(e) => set("fileSizeMB", e.target.value)} className="h-8" /></Field>
          <Field label="Workspaces"><Input type="number" value={form.maxWorkspaces} onChange={(e) => set("maxWorkspaces", e.target.value)} className="h-8" /></Field>
          <Field label="Share expiry (days)"><Input type="number" value={form.shareExpiryDays} onChange={(e) => set("shareExpiryDays", e.target.value)} className="h-8" /></Field>
          <Field label="Trash retention (days)"><Input type="number" value={form.trashRetentionDays} onChange={(e) => set("trashRetentionDays", e.target.value)} className="h-8" /></Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Availability & inventory (blank = no limit)</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-3 gap-4">
          <Field label="Max subscriptions" hint="sold out when reached"><Input type="number" value={form.maxSubscriptions} onChange={(e) => set("maxSubscriptions", e.target.value)} className="h-8" /></Field>
          <Field label="Available from"><Input type="datetime-local" value={form.availableFrom} onChange={(e) => set("availableFrom", e.target.value)} className="h-8" /></Field>
          <Field label="Available until" hint="auto-retires from offer"><Input type="datetime-local" value={form.availableUntil} onChange={(e) => set("availableUntil", e.target.value)} className="h-8" /></Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Marketing lines (one per line)</CardTitle>
        </CardHeader>
        <CardContent>
          <textarea
            value={form.marketing}
            onChange={(e) => set("marketing", e.target.value)}
            rows={4}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
          />
        </CardContent>
      </Card>

      <div className="flex items-center justify-end gap-3">
        <Button variant="ghost" onClick={() => navigate(isEdit ? `/catalog/${id}` : "/catalog")} className="h-8">
          Cancel
        </Button>
        <Button onClick={() => setShowStepUp(true)} disabled={!form.code.trim() || !form.name.trim()} className="h-8">
          {isEdit ? "Save changes" : "Create plan"}
        </Button>
      </div>

      {showStepUp && (
        <StepUpModal
          title={isEdit ? "Save plan changes?" : "Create this plan?"}
          description="Catalog edits require a fresh 2FA confirmation and are audited."
          confirmLabel={isEdit ? "Save" : "Create"}
          onConfirm={save}
          onClose={() => setShowStepUp(false)}
        />
      )}
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs">{label}</Label>
      {children}
      {hint && <p className="text-[10px] text-muted-foreground/60">{hint}</p>}
    </div>
  );
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm text-foreground">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} className="h-3.5 w-3.5 rounded border-border text-primary" />
      {label}
    </label>
  );
}
