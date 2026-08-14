import { BarChart3 } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type { ReportData } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock, EmptyBlock, SkeletonRows } from "@/components/DataState";

function money(cents: number): string {
  return (cents / 100).toLocaleString(undefined, { style: "currency", currency: "USD" });
}
function usd(n: number): string {
  return n.toLocaleString(undefined, { style: "currency", currency: "USD" });
}

// Reports & Marketing (SPEC §6.6): business reporting + a first-cut unit-economics
// view. Read-only; email/segment tooling deferred (platform email is unbuilt).
export default function Reports() {
  const { data: r, loading, error } = useApiGet<ReportData>("/api/cmx/reports");

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <BarChart3 className="h-6 w-6 text-primary" /> Reports &amp; Marketing
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Recurring revenue, plan mix, the signup funnel, and unit economics.
        </p>
      </div>

      {loading ? (
        <LoadingBlock />
      ) : error || !r ? (
        <ErrorBlock message={error || "Unavailable."} />
      ) : (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
            <Tile label="MRR" value={money(r.mrr_cents)} accent />
            <Tile label="ARR" value={money(r.arr_cents)} />
            <Tile label="ARPU" value={money(r.arpu_cents)} />
            <Tile label="Paying" value={String(r.paying_accounts)} />
            <Tile label="Trialing" value={String(r.trialing)} />
            <Tile label="Comp" value={String(r.comp)} />
          </div>

          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">Plan mix</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                {!r.plan_mix || r.plan_mix.length === 0 ? (
                  <EmptyBlock label="No subscribers yet." />
                ) : (
                  <table className="w-full text-sm">
                    <thead className="sticky top-0 z-10 bg-card">
                      <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                        <th className="px-4 py-2.5 font-medium">Plan</th>
                        <th className="px-4 py-2.5 font-medium">Subscribers</th>
                        <th className="px-4 py-2.5 font-medium">MRR</th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.plan_mix.map((p) => (
                        <tr key={p.code} className="border-b border-border/60 last:border-0">
                          <td className="px-4 py-2.5 font-medium text-foreground">{p.name}</td>
                          <td className="px-4 py-2.5 text-muted-foreground">{p.subscribers}</td>
                          <td className="px-4 py-2.5 text-muted-foreground">{money(p.mrr_cents)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </CardContent>
            </Card>

            <div className="space-y-3">
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-sm">Signup funnel</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2 text-sm">
                  <Row label="Leads in flight" value={String(r.leads)} />
                  <Row label="Paid, not completed (stuck)" value={String(r.intents_paid)} warn={r.intents_paid > 0} />
                  <Row label="Completed signups" value={String(r.intents_completed)} />
                  <Row label="Conversion rate" value={`${r.conversion_pct.toFixed(1)}%`} />
                  <Row label="Active accounts" value={String(r.active_total)} />
                  <Row label="Churned (lapsed/closing)" value={String(r.churned)} warn={r.churned > 0} />
                </CardContent>
              </Card>

              <Card className="border-primary/20">
                <CardHeader className="pb-2">
                  <CardTitle className="text-sm">Unit economics (monthly)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2 text-sm">
                  <Row label="Recurring revenue (MRR)" value={money(r.mrr_cents)} />
                  <Row label="Est. storage cost" value={`− ${usd(r.storage_cost_usd)}`} />
                  <div className="flex items-center justify-between border-t border-border/60 pt-2">
                    <span className="font-medium text-foreground">Gross margin</span>
                    <span className="font-semibold text-primary">{usd(r.gross_margin_usd)}/mo</span>
                  </div>
                  <p className="text-[11px] text-muted-foreground/70">
                    Storage cost is estimated from tier byte distribution at hard-coded Azure prices. Egress and
                    compute are not included.
                  </p>
                </CardContent>
              </Card>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function Tile({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <Card className={accent ? "border-primary/30" : undefined}>
      <CardContent className="p-4">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        <div className={`mt-1 text-xl font-semibold ${accent ? "text-primary" : "text-foreground"}`}>{value}</div>
      </CardContent>
    </Card>
  );
}

function Row({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-muted-foreground">{label}</span>
      <span className={warn ? "font-medium text-destructive" : "text-foreground"}>{value}</span>
    </div>
  );
}
