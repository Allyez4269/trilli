import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { CreditCard, AlertTriangle, Receipt, Repeat, Hourglass } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type {
  RevenueOverview,
  Subscription,
  BillingTransaction,
  PastDueAccount,
  RevenueSignupIntent,
} from "@/lib/api";
import { formatDate, formatDateTime } from "@/lib/format";
import { Badge, toneFor, type Tone } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";

// money always renders a currency figure (formatCents collapses 0 → "—",
// which reads wrong for revenue tiles).
function money(cents: number): string {
  return (cents / 100).toLocaleString(undefined, { style: "currency", currency: "USD" });
}

type Tab = "subscriptions" | "invoices" | "pastdue" | "intents";

const TABS: { key: Tab; label: string; icon: typeof Repeat }[] = [
  { key: "subscriptions", label: "Subscriptions", icon: Repeat },
  { key: "invoices", label: "Invoices & orders", icon: Receipt },
  { key: "pastdue", label: "Past-due", icon: AlertTriangle },
  { key: "intents", label: "Signup intents", icon: Hourglass },
];

export default function Revenue() {
  const [tab, setTab] = useState<Tab>("subscriptions");
  const { data: ov } = useApiGet<RevenueOverview>("/api/cmx/revenue/overview");

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <CreditCard className="h-6 w-6 text-primary" /> Revenue
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Money in — subscriptions, invoices, dunning, and the signup funnel. Read-only; refunds and
          account credit arrive in a later slice.
        </p>
      </div>

      {/* Overview tiles */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Tile label="MRR" value={ov ? money(ov.mrr_cents) : "…"} accent />
        <Tile label="Active subs" value={ov ? String(ov.active_subscriptions) : "…"} />
        <Tile label="Comp" value={ov ? String(ov.comp_accounts) : "…"} />
        <Tile
          label="Past-due"
          value={ov ? String(ov.past_due_count) : "…"}
          warn={!!ov && ov.past_due_count > 0}
        />
        <Tile
          label="Open intents"
          value={ov ? String(ov.pending_intents) : "…"}
        />
        <Tile label="Collected 30d" value={ov ? money(ov.collected_30d_cents) : "…"} />
      </div>
      {ov && ov.refunded_30d_cents > 0 && (
        <p className="text-xs text-muted-foreground">
          {money(ov.refunded_30d_cents)} refunded in the last 30 days · {money(ov.net_collected_cents)} net
          collected all-time.
        </p>
      )}

      {/* Tab switcher */}
      <div className="flex gap-1 border-b border-border">
        {TABS.map((t) => {
          const Icon = t.icon;
          const active = tab === t.key;
          return (
            <button
              key={t.key}
              type="button"
              onClick={() => setTab(t.key)}
              className={`relative flex items-center gap-1.5 px-3 py-2 text-sm font-medium transition-colors ${
                active
                  ? "text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <Icon className="h-4 w-4" /> {t.label}
              {active && <span className="absolute bottom-0 left-3 right-3 h-0.5 rounded-full bg-primary" />}
            </button>
          );
        })}
      </div>

      {tab === "subscriptions" && <SubscriptionsTab />}
      {tab === "invoices" && <InvoicesTab />}
      {tab === "pastdue" && <PastDueTab />}
      {tab === "intents" && <IntentsTab />}
    </div>
  );
}

function Tile({
  label,
  value,
  accent,
  warn,
}: {
  label: string;
  value: string;
  accent?: boolean;
  warn?: boolean;
}) {
  return (
    <Card className={warn ? "border-destructive/40" : accent ? "border-primary/30" : undefined}>
      <CardContent className="p-4">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        <div
          className={`mt-1 text-xl font-semibold ${
            warn ? "text-destructive" : accent ? "text-primary" : "text-foreground"
          }`}
        >
          {value}
        </div>
      </CardContent>
    </Card>
  );
}

function TableShell({
  loading,
  error,
  empty,
  emptyLabel,
  pager,
  children,
}: {
  loading: boolean;
  error: string | null;
  empty: boolean;
  emptyLabel: string;
  pager?: { page: number; pageCount: number; total: number; pageSize: number; setPage: (p: number) => void };
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-clip rounded-xl border border-border bg-card">
      {loading ? (
        <SkeletonRows rows={6} />
      ) : error ? (
        <ErrorBlock message={error} />
      ) : empty ? (
        <EmptyBlock label={emptyLabel} />
      ) : (
        <>
          <table className="w-full text-sm">{children}</table>
          {pager && (
            <Pager
              page={pager.page}
              pageCount={pager.pageCount}
              total={pager.total}
              pageSize={pager.pageSize}
              onPage={pager.setPage}
            />
          )}
        </>
      )}
    </div>
  );
}

const TH = "px-4 py-2.5 font-medium";
const headRow =
  "sticky top-0 z-10 bg-card border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground";
const bodyRow = "border-b border-border/60 last:border-0";

function billingTone(status: string): Tone {
  if (status === "active") return "green";
  if (status === "trialing") return "amber";
  if (status === "past_due" || status === "unpaid") return "red";
  return "neutral";
}

function SubscriptionsTab() {
  const navigate = useNavigate();
  const { data, loading, error } = useApiGet<{ subscriptions: Subscription[] }>(
    "/api/cmx/revenue/subscriptions",
  );
  const subs = data?.subscriptions ?? [];
  const pager = usePager(subs);
  return (
    <TableShell loading={loading} error={error} empty={subs.length === 0} emptyLabel="No subscriptions." pager={pager}>
      <thead>
        <tr className={headRow}>
          <th className={TH}>Account</th>
          <th className={TH}>Plan</th>
          <th className={TH}>Status</th>
          <th className={TH}>Monthly</th>
          <th className={TH}>Renews</th>
          <th className={TH}>Card</th>
        </tr>
      </thead>
      <tbody>
        {pager.rows.map((s) => (
          <tr
            key={s.tenant_id}
            onClick={() => navigate(`/revenue/accounts/${s.tenant_id}`)}
            className={`cursor-pointer transition-colors hover:bg-muted/50 ${bodyRow}`}
          >
            <td className="px-4 py-3">
              <div className="font-medium text-foreground">{s.name}</div>
              <div className="text-xs text-muted-foreground">{s.owner_email || "—"}</div>
            </td>
            <td className="px-4 py-3">
              <div className="text-foreground">{s.plan_name}</div>
              <div className="text-xs text-muted-foreground">
                {s.billing_period || "—"}
                {s.extra_seats > 0 ? ` · +${s.extra_seats} seat${s.extra_seats > 1 ? "s" : ""}` : ""}
              </div>
            </td>
            <td className="px-4 py-3">
              {s.billing_mode === "comp" ? (
                <Badge tone="blue">comp</Badge>
              ) : (
                <Badge tone={billingTone(s.subscription_status)}>
                  {s.subscription_status || "none"}
                </Badge>
              )}
            </td>
            <td className="px-4 py-3 text-muted-foreground">
              {s.billing_mode === "comp" ? "—" : money(s.monthly_cents)}
            </td>
            <td className="px-4 py-3 text-muted-foreground">
              {s.billing_mode === "comp"
                ? s.comp_expires_at
                  ? `ends ${formatDate(s.comp_expires_at)}`
                  : "—"
                : s.auto_renew
                  ? formatDate(s.current_period_end)
                  : `cancels ${formatDate(s.current_period_end)}`}
            </td>
            <td className="px-4 py-3 text-muted-foreground">{s.has_card_on_file ? "on file" : "—"}</td>
          </tr>
        ))}
      </tbody>
    </TableShell>
  );
}

function InvoicesTab() {
  const { data, loading, error } = useApiGet<{ transactions: BillingTransaction[] }>(
    "/api/cmx/revenue/transactions?limit=200",
  );
  const txs = data?.transactions ?? [];
  const pager = usePager(txs);
  return (
    <TableShell loading={loading} error={error} empty={txs.length === 0} emptyLabel="No transactions yet." pager={pager}>
      <thead>
        <tr className={headRow}>
          <th className={TH}>Date</th>
          <th className={TH}>Account</th>
          <th className={TH}>Order / receipt</th>
          <th className={TH}>Amount</th>
          <th className={TH}>Status</th>
          <th className={TH}></th>
        </tr>
      </thead>
      <tbody>
        {pager.rows.map((t) => {
          const refund = t.status === "refunded" || t.amount_cents < 0;
          return (
            <tr key={t.id} className={bodyRow}>
              <td className="px-4 py-3 text-muted-foreground">{formatDateTime(t.created_at)}</td>
              <td className="px-4 py-3">
                <div className="text-foreground">{t.tenant_name}</div>
                <div className="text-xs text-muted-foreground">
                  {t.plan_code} · {t.billing_period}
                </div>
              </td>
              <td className="px-4 py-3 text-xs text-muted-foreground">
                {t.order_number || t.receipt_number || "—"}
              </td>
              <td className={`px-4 py-3 font-medium ${refund ? "text-destructive" : "text-foreground"}`}>
                {money(t.amount_cents)}
              </td>
              <td className="px-4 py-3">
                <Badge tone={refund ? "amber" : "green"}>{t.status}</Badge>
              </td>
              <td className="px-4 py-3 text-right">
                {t.receipt_url && (
                  <a
                    href={t.receipt_url}
                    target="_blank"
                    rel="noreferrer"
                    className="text-xs text-primary hover:underline"
                  >
                    Receipt ↗
                  </a>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </TableShell>
  );
}

function PastDueTab() {
  const navigate = useNavigate();
  const { data, loading, error } = useApiGet<{ accounts: PastDueAccount[] }>(
    "/api/cmx/revenue/past-due",
  );
  const accts = data?.accounts ?? [];
  const pager = usePager(accts);
  return (
    <TableShell
      loading={loading}
      error={error}
      empty={accts.length === 0}
      emptyLabel="No past-due or lapsed accounts. 🎉"
      pager={pager}
    >
      <thead>
        <tr className={headRow}>
          <th className={TH}>Account</th>
          <th className={TH}>Plan</th>
          <th className={TH}>State</th>
          <th className={TH}>Monthly</th>
          <th className={TH}>Period end</th>
          <th className={TH}>Purge at</th>
        </tr>
      </thead>
      <tbody>
        {pager.rows.map((a) => (
          <tr
            key={a.tenant_id}
            onClick={() => navigate(`/revenue/accounts/${a.tenant_id}`)}
            className={`cursor-pointer transition-colors hover:bg-muted/50 ${bodyRow}`}
          >
            <td className="px-4 py-3">
              <div className="font-medium text-foreground">{a.name}</div>
              <div className="text-xs text-muted-foreground">{a.owner_email || "—"}</div>
            </td>
            <td className="px-4 py-3 text-muted-foreground">{a.plan_code}</td>
            <td className="px-4 py-3 space-x-1">
              {a.subscription_status && (
                <Badge tone={billingTone(a.subscription_status)}>{a.subscription_status}</Badge>
              )}
              {a.lifecycle_state !== "current" && (
                <Badge tone={toneFor(a.lifecycle_state)}>{a.lifecycle_state}</Badge>
              )}
            </td>
            <td className="px-4 py-3 text-muted-foreground">{money(a.monthly_cents)}</td>
            <td className="px-4 py-3 text-muted-foreground">{formatDate(a.current_period_end)}</td>
            <td className="px-4 py-3 text-muted-foreground">
              {a.purge_at ? (
                <span className="text-destructive">{formatDate(a.purge_at)}</span>
              ) : (
                "—"
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </TableShell>
  );
}

function IntentsTab() {
  const { data, loading, error } = useApiGet<{ intents: RevenueSignupIntent[] }>(
    "/api/cmx/revenue/signup-intents",
  );
  const intents = data?.intents ?? [];
  const pager = usePager(intents);
  return (
    <TableShell
      loading={loading}
      error={error}
      empty={intents.length === 0}
      emptyLabel="No in-flight signup intents."
      pager={pager}
    >
      <thead>
        <tr className={headRow}>
          <th className={TH}>Email</th>
          <th className={TH}>Plan</th>
          <th className={TH}>Status</th>
          <th className={TH}>Started</th>
          <th className={TH}>Expires</th>
        </tr>
      </thead>
      <tbody>
        {pager.rows.map((i) => (
          <tr key={i.id} className={`${bodyRow} ${i.stuck ? "bg-destructive/[0.04]" : ""}`}>
            <td className="px-4 py-3">
              <div className="font-medium text-foreground">{i.email}</div>
              <div className="text-xs text-muted-foreground">
                {i.account_type}
                {i.oauth_provider ? ` · ${i.oauth_provider}` : ""}
              </div>
            </td>
            <td className="px-4 py-3 text-muted-foreground">
              {i.plan_code || "—"} · {i.billing_cycle}
            </td>
            <td className="px-4 py-3">
              {i.stuck ? (
                <Badge tone="red">paid · stuck</Badge>
              ) : (
                <Badge tone={i.status === "email_verified" ? "amber" : "neutral"}>{i.status}</Badge>
              )}
            </td>
            <td className="px-4 py-3 text-muted-foreground">{formatDateTime(i.created_at)}</td>
            <td className="px-4 py-3 text-muted-foreground">{formatDateTime(i.expires_at)}</td>
          </tr>
        ))}
      </tbody>
    </TableShell>
  );
}
