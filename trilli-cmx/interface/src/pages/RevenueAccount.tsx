import { type FormEvent, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, RefreshCw, Gift, Undo2, Ban, RotateCcw } from "lucide-react";

import { api, type TenantDetail, type BillingTransaction, type CreditGrant, type Plan } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { formatDate, formatDateTime } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock, EmptyBlock } from "@/components/DataState";
import StepUpModal from "@/components/StepUpModal";

function money(cents: number): string {
  return (cents / 100).toLocaleString(undefined, { style: "currency", currency: "USD" });
}

type Pending =
  | { kind: "reconcile" }
  | { kind: "credit"; cents: number; reason: string }
  | { kind: "refund"; tx: BillingTransaction }
  | { kind: "autorenew"; enabled: boolean }
  | { kind: "changeplan"; planCode: string; period: string; planName: string }
  | null;

// Per-account billing console (SPEC §6.4). Global-only money actions: reconcile
// the Stripe subscription, grant account credit, and refund a charge — each
// step-up gated and proxied to the app's admin surface.
export default function RevenueAccount() {
  const { id } = useParams();
  const navigate = useNavigate();
  const tid = Number(id);

  const { data: t, loading, error, reload: reloadTenant } = useApiGet<TenantDetail>(`/api/cmx/tenants/${tid}`);
  const { data: txData, reload: reloadTx } = useApiGet<{ transactions: BillingTransaction[] }>(
    `/api/cmx/revenue/transactions?tenant_id=${tid}`,
  );
  const { data: creditData, reload: reloadCredits } = useApiGet<{ credits: CreditGrant[]; total_cents: number }>(
    `/api/cmx/revenue/tenants/${tid}/credits`,
  );
  const { data: plansData } = useApiGet<{ plans: Plan[] }>("/api/cmx/plans");
  const availablePlans = (plansData?.plans ?? []).filter((p) => p.status === "available");

  const [pending, setPending] = useState<Pending>(null);
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const [planCode, setPlanCode] = useState("");
  const [period, setPeriod] = useState("monthly");

  const txs = txData?.transactions ?? [];
  const credits = creditData?.credits ?? [];

  function reloadAll() {
    reloadTenant();
    reloadTx();
    reloadCredits();
  }

  function submitCredit(e: FormEvent) {
    e.preventDefault();
    const dollars = Number(amount);
    if (!(dollars > 0)) return;
    setPending({ kind: "credit", cents: Math.round(dollars * 100), reason: reason.trim() });
  }

  async function run(p: Exclude<Pending, null>) {
    if (p.kind === "reconcile") {
      await api.post(`/api/cmx/revenue/tenants/${tid}/reconcile-subscription`);
    } else if (p.kind === "credit") {
      await api.post(`/api/cmx/revenue/tenants/${tid}/credits`, { amount_cents: p.cents, reason: p.reason });
      setAmount("");
      setReason("");
    } else if (p.kind === "refund") {
      // amount_cents 0 = full remaining refund of this charge.
      await api.post(`/api/cmx/revenue/tenants/${tid}/refund`, { transaction_id: p.tx.id, amount_cents: 0 });
    } else if (p.kind === "autorenew") {
      await api.post(`/api/cmx/revenue/tenants/${tid}/auto-renew`, { enabled: p.enabled });
    } else if (p.kind === "changeplan") {
      await api.post(`/api/cmx/revenue/tenants/${tid}/change-plan`, { plan_code: p.planCode, billing_period: p.period });
      setPlanCode("");
    }
    reloadAll();
  }

  function modalProps() {
    switch (pending?.kind) {
      case "reconcile":
        return {
          title: "Reconcile subscription?",
          description: "Force a fresh pull from Stripe and rewrite this account's local subscription status, period end, and auto-renew.",
          confirmLabel: "Reconcile",
          destructive: false,
        };
      case "credit":
        return {
          title: `Grant ${money(pending.cents)} credit?`,
          description: "Posts a credit to the account's Stripe balance; it's applied automatically to the next invoice. Recorded in the credit ledger.",
          confirmLabel: "Grant credit",
          destructive: false,
        };
      case "refund":
        return {
          title: `Refund ${money(pending.tx.amount_cents)}?`,
          description: "Refunds this charge in full at the payment processor. This cannot be undone.",
          confirmLabel: "Issue refund",
          destructive: true,
        };
      case "autorenew":
        return pending.enabled
          ? {
              title: "Resume auto-renew?",
              description: "The subscription will renew at term end and the customer will be charged.",
              confirmLabel: "Resume auto-renew",
              destructive: false,
            }
          : {
              title: "Cancel at period end?",
              description: "Auto-renew turns off; the subscription lapses to read-only grace at term end. No immediate charge or refund. Reversible until then.",
              confirmLabel: "Cancel at period end",
              destructive: true,
            };
      case "changeplan":
        return {
          title: `Change plan to ${pending.planName}?`,
          description: "Upgrades take effect immediately and are prorated (the customer is charged now); downgrades are scheduled to term end.",
          confirmLabel: "Change plan",
          destructive: false,
        };
      default:
        return null;
    }
  }
  const mp = modalProps();

  if (loading) return <LoadingBlock />;
  if (error || !t) return <ErrorBlock message={error || "Account not found."} />;

  return (
    <div className="mx-auto max-w-[81.6rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate("/revenue")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Revenue
      </button>

      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">{t.name}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t.owner_email} · billing console
          </p>
        </div>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => navigate(`/accounts/${tid}`)}>
          Account detail
        </Button>
      </div>

      {/* Subscription summary */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Subscription</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
          <Field label="Plan" value={t.plan_name || t.plan_code} />
          <Field
            label="Status"
            value={<Badge tone={toneFor(t.subscription_status || "")}>{t.subscription_status || "none"}</Badge>}
          />
          <Field label="Monthly (locked)" value={t.locked_price_cents ? money(t.locked_price_cents) : "—"} />
          <Field label="Seats" value={`${t.user_count}${t.extra_seats ? ` (+${t.extra_seats})` : ""}`} />
          <Field label="Auto-renew" value={t.auto_renew ? "on" : "off"} />
          <Field label="Period end" value={formatDate(t.current_period_end)} />
          <Field label="Card" value={t.stripe_customer_id ? "customer on file" : "—"} />
          <Field label="Credit granted" value={money(creditData?.total_cents ?? 0)} />
        </CardContent>
      </Card>

      {/* Money actions */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Billing actions</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-3">
            <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setPending({ kind: "reconcile" })}>
              <RefreshCw className="h-4 w-4" /> Reconcile from Stripe
            </Button>
            {t.auto_renew ? (
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 text-destructive hover:text-destructive"
                onClick={() => setPending({ kind: "autorenew", enabled: false })}
              >
                <Ban className="h-4 w-4" /> Cancel at period end
              </Button>
            ) : (
              <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setPending({ kind: "autorenew", enabled: true })}>
                <RotateCcw className="h-4 w-4" /> Resume auto-renew
              </Button>
            )}
          </div>

          <div className="flex flex-wrap items-end gap-3">
            <div className="space-y-1.5">
              <Label className="text-xs">Change plan</Label>
              <select
                value={planCode}
                onChange={(e) => setPlanCode(e.target.value)}
                className="h-8 w-44 rounded-md border border-input bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
              >
                <option value="">Select plan…</option>
                {availablePlans.map((p) => (
                  <option key={p.id} value={p.code}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Period</Label>
              <select
                value={period}
                onChange={(e) => setPeriod(e.target.value)}
                className="h-8 w-32 rounded-md border border-input bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
              >
                <option value="monthly">Monthly</option>
                <option value="annual">Annual</option>
              </select>
            </div>
            <Button
              size="sm"
              variant="secondary"
              className="h-8"
              disabled={!planCode}
              onClick={() =>
                setPending({
                  kind: "changeplan",
                  planCode,
                  period,
                  planName: availablePlans.find((p) => p.code === planCode)?.name ?? planCode,
                })
              }
            >
              Change plan
            </Button>
          </div>

          <form onSubmit={submitCredit} className="flex flex-wrap items-end gap-3">
            <div className="space-y-1.5">
              <Label className="text-xs">Grant credit (USD)</Label>
              <Input
                type="number"
                min={0}
                step="0.01"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="0.00"
                className="h-8 w-32"
              />
            </div>
            <div className="space-y-1.5 flex-1 min-w-[12rem]">
              <Label className="text-xs">Reason (optional)</Label>
              <Input value={reason} onChange={(e) => setReason(e.target.value)} className="h-8" />
            </div>
            <Button type="submit" size="sm" className="h-8 gap-1.5" disabled={!(Number(amount) > 0)}>
              <Gift className="h-4 w-4" /> Grant credit
            </Button>
          </form>

          <p className="text-[11px] text-muted-foreground/60">
            Each action requires a fresh 2FA confirmation and is written to the operator audit.
          </p>
        </CardContent>
      </Card>

      {/* Credit ledger */}
      {credits.length > 0 && (
        <div className="overflow-hidden rounded-xl border border-border bg-card">
          <div className="border-b border-border px-4 py-2.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Credit ledger
          </div>
          <table className="w-full text-sm">
            <tbody>
              {credits.map((c) => (
                <tr key={c.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5 font-medium text-foreground">{money(c.amount_cents)}</td>
                  <td className="px-4 py-2.5 text-muted-foreground">{c.reason || "—"}</td>
                  <td className="px-4 py-2.5 text-xs text-muted-foreground">{c.granted_by_email}</td>
                  <td className="px-4 py-2.5 text-right text-xs text-muted-foreground">{formatDateTime(c.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Transactions + refund */}
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="border-b border-border px-4 py-2.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Transactions
        </div>
        {txs.length === 0 ? (
          <EmptyBlock label="No transactions." />
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Date</th>
                <th className="px-4 py-2.5 font-medium">Order</th>
                <th className="px-4 py-2.5 font-medium">Amount</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {txs.map((tx) => {
                const refund = tx.status === "refunded" || tx.amount_cents < 0;
                return (
                  <tr key={tx.id} className="border-b border-border/60 last:border-0">
                    <td className="px-4 py-3 text-muted-foreground">{formatDateTime(tx.created_at)}</td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">{tx.order_number || tx.receipt_number || "—"}</td>
                    <td className={`px-4 py-3 font-medium ${refund ? "text-destructive" : "text-foreground"}`}>
                      {money(tx.amount_cents)}
                    </td>
                    <td className="px-4 py-3">
                      <Badge tone={refund ? "amber" : "green"}>{tx.status}</Badge>
                    </td>
                    <td className="px-4 py-3 text-right">
                      {!refund && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 gap-1 text-destructive hover:text-destructive"
                          onClick={() => setPending({ kind: "refund", tx })}
                        >
                          <Undo2 className="h-3.5 w-3.5" /> Refund
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {pending && mp && (
        <StepUpModal
          title={mp.title}
          description={mp.description}
          confirmLabel={mp.confirmLabel}
          destructive={mp.destructive}
          onConfirm={() => run(pending)}
          onClose={() => setPending(null)}
        />
      )}
    </div>
  );
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className="mt-0.5 text-foreground">{value}</div>
    </div>
  );
}
