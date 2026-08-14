import { type FormEvent, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Building2, Send } from "lucide-react";

import { api, ApiError, type CustomerDetail, type CustomerNote, type AccountBrief, type ConsentChange } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { formatDate, formatDateTime, relativeTime } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock } from "@/components/DataState";

export default function CustomerDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { data, loading, error, reload } = useApiGet<CustomerDetail>(`/api/cmx/customers/${id}`);

  if (loading) return <LoadingBlock />;
  if (error) return <ErrorBlock message={error} />;
  if (!data) return <ErrorBlock message="Customer not found." />;

  return (
    <div className="mx-auto max-w-[81.6rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate("/customers")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Customers
      </button>

      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">{data.name}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {data.email}
            {data.company ? ` · ${data.company}` : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge tone={toneFor(data.lifecycle_stage)}>{data.lifecycle_stage}</Badge>
          <Badge tone={toneFor(data.status)}>{data.status}</Badge>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
        <Card className="lg:col-span-1">
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Identity</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <Row label="Email verified" value={data.email_verified ? "Yes" : "No"} />
            <Row label="Joined" value={formatDate(data.created_at)} />
            <Row label="Last seen" value={relativeTime(data.last_login_at)} />
            <Row label="User ID" value={String(data.identity_user_id)} mono />
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Owned accounts</CardTitle>
          </CardHeader>
          <CardContent>
            <AccountList accounts={data.owned_accounts} emptyLabel="Owns no accounts." />
          </CardContent>
        </Card>
      </div>

      {data.memberships && data.memberships.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Member of (not owner)</CardTitle>
          </CardHeader>
          <CardContent>
            <AccountList accounts={data.memberships} emptyLabel="" />
          </CardContent>
        </Card>
      )}

      <NotesCard customerId={data.identity_user_id} notes={data.notes ?? []} onAdded={reload} />

      <ConsentCard customerId={data.identity_user_id} />
    </div>
  );
}

// ConsentCard renders the notification-consent ledger (proof-of-consent for
// CAN-SPAM/GDPR, SPEC §6.8) — a write-only audit of every preference change
// with the time, IP, and geo it was captured at.
function ConsentCard({ customerId }: { customerId: number }) {
  const { data } = useApiGet<{ changes: ConsentChange[] }>(`/api/cmx/customers/${customerId}/consent`);
  const changes = data?.changes ?? [];
  if (changes.length === 0) return null;
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Notification consent ledger</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        <p className="text-xs text-muted-foreground">
          Append-only proof-of-consent — each preference change with the time, IP, and location it was captured at.
        </p>
        <div className="divide-y divide-border/60">
          {changes.map((c) => {
            const where = [c.city, c.region, c.country].filter(Boolean).join(", ");
            return (
              <div key={c.id} className="py-2 text-xs first:pt-0 last:pb-0">
                <div className="flex items-center justify-between text-muted-foreground">
                  <span>{formatDateTime(c.created_at)}</span>
                  <span>
                    {c.ip || "—"}
                    {where ? ` · ${where}` : ""}
                  </span>
                </div>
                <pre className="mt-1 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px] text-foreground/80">
                  {prettyPrefs(c.prefs)}
                </pre>
              </div>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}

function prettyPrefs(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-xs text-foreground" : "text-foreground"}>{value}</span>
    </div>
  );
}

function AccountList({ accounts, emptyLabel }: { accounts: AccountBrief[]; emptyLabel: string }) {
  if (accounts.length === 0) {
    return <p className="py-2 text-sm text-muted-foreground/70">{emptyLabel}</p>;
  }
  return (
    <ul className="divide-y divide-border/60">
      {accounts.map((a) => (
        <li key={a.tenant_id}>
          <Link
            to={`/accounts/${a.tenant_id}`}
            className="flex items-center justify-between py-2.5 transition-colors hover:text-primary"
          >
            <span className="flex items-center gap-2">
              <Building2 className="h-4 w-4 text-muted-foreground" />
              <span className="font-medium text-foreground">{a.name}</span>
              {a.plan_code && <Badge tone="slate">{a.plan_code}</Badge>}
            </span>
            <span className="flex items-center gap-2">
              <Badge tone={toneFor(a.tenant_status)}>{a.tenant_status}</Badge>
              <span className="text-xs capitalize text-muted-foreground">{a.role}</span>
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function NotesCard({
  customerId,
  notes,
  onAdded,
}: {
  customerId: number;
  notes: CustomerNote[];
  onAdded: () => void;
}) {
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!body.trim() || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.post(`/api/cmx/customers/${customerId}/notes`, { body: body.trim() });
      setBody("");
      onAdded();
    } catch (e2) {
      setErr(e2 instanceof ApiError ? e2.message : "Failed to add note");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Operator notes</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <form onSubmit={submit} className="flex items-start gap-2">
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Add an internal note about this customer…"
            rows={2}
            className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
          />
          <Button type="submit" size="sm" disabled={busy || !body.trim()} className="h-8 gap-1.5">
            <Send className="h-3.5 w-3.5" />
            {busy ? "…" : "Add"}
          </Button>
        </form>
        {err && <p className="text-sm text-destructive">{err}</p>}
        {notes.length === 0 ? (
          <p className="text-sm text-muted-foreground/70">No notes yet.</p>
        ) : (
          <ul className="divide-y divide-border/60">
            {notes.map((n) => (
              <li key={n.id} className="py-3 first:pt-0 last:pb-0">
                <p className="text-sm text-foreground">{n.body}</p>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {n.operator_email || `operator ${n.operator_id}`} · {formatDateTime(n.created_at)}
                </p>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
