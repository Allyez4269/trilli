import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

import type { TenantDetail } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { formatBytes, formatCents, formatDate, formatDateTime, relativeTime, storagePct } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock } from "@/components/DataState";
import { cn } from "@/lib/utils";
import AccountActions from "@/components/AccountActions";

export default function AccountDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { data, loading, error, reload } = useApiGet<TenantDetail>(`/api/cmx/tenants/${id}`);

  if (loading) return <LoadingBlock />;
  if (error) return <ErrorBlock message={error} />;
  if (!data) return <ErrorBlock message="Account not found." />;

  const pct = storagePct(data.storage_bytes_used, data.storage_bytes_max);

  return (
    <div className="mx-auto max-w-[81.6rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate("/accounts")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Accounts
      </button>

      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">{data.name}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {data.slug ? `${data.slug} · ` : ""}owner {data.owner_email || "—"}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {data.plan_code && <Badge tone="slate">{data.plan_name || data.plan_code}</Badge>}
          <Badge tone={toneFor(data.status)}>{data.status}</Badge>
          {data.lifecycle_state && data.lifecycle_state !== "current" && (
            <Badge tone={toneFor(data.lifecycle_state)}>{data.lifecycle_state}</Badge>
          )}
        </div>
      </div>

      <AccountActions tenantId={data.id} status={data.status} onChanged={reload} />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        {/* Plan & storage */}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Plan & storage</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Row label="Plan" value={data.plan_name || data.plan_code || "—"} />
            <Row label="Seats" value={`${data.user_count}${data.extra_seats ? ` (+${data.extra_seats} extra)` : ""}`} />
            <div>
              <div className="flex items-center justify-between">
                <span className="text-muted-foreground">Storage</span>
                <span className="text-foreground">
                  {formatBytes(data.storage_bytes_used)}
                  {data.storage_bytes_max > 0 ? ` / ${formatBytes(data.storage_bytes_max)}` : ""}
                  {pct !== null ? ` · ${pct}%` : ""}
                </span>
              </div>
              {pct !== null && (
                <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                  <div
                    className={cn("h-full rounded-full", pct >= 90 ? "bg-destructive" : "bg-primary")}
                    style={{ width: `${pct}%` }}
                  />
                </div>
              )}
            </div>
          </CardContent>
        </Card>

        {/* Billing snapshot */}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Billing</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <Row
              label="Subscription"
              value={data.subscription_status || "—"}
              badge={data.subscription_status ? toneFor(data.subscription_status) : undefined}
            />
            <Row label="Locked price" value={formatCents(data.locked_price_cents, data.locked_billing_period)} />
            <Row label="Auto-renew" value={data.auto_renew ? "On" : "Off"} />
            <Row label="Period ends" value={formatDate(data.current_period_end)} />
            {data.scheduled_plan_code && <Row label="Scheduled change" value={data.scheduled_plan_code} />}
            {data.lapsed_at && <Row label="Lapsed" value={formatDate(data.lapsed_at)} />}
            {data.purge_at && <Row label="Purge at" value={formatDate(data.purge_at)} />}
            <Row label="Stripe customer" value={data.stripe_customer_id || "—"} mono />
            <Row label="Created" value={formatDate(data.created_at)} />
          </CardContent>
        </Card>
      </div>

      {/* Members */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Members ({data.members.length})</CardTitle>
        </CardHeader>
        <CardContent className="px-0 pb-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-y border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-5 py-2 font-medium">Member</th>
                <th className="px-5 py-2 font-medium">Role</th>
                <th className="px-5 py-2 font-medium">Status</th>
                <th className="px-5 py-2 font-medium">Last seen</th>
              </tr>
            </thead>
            <tbody>
              {data.members.map((m) => (
                <tr key={m.user_id} className="border-b border-border/60 last:border-0">
                  <td className="px-5 py-2.5">
                    <div className="font-medium text-foreground">{m.name}</div>
                    <div className="text-xs text-muted-foreground">{m.email}</div>
                  </td>
                  <td className="px-5 py-2.5 capitalize text-muted-foreground">{m.role}</td>
                  <td className="px-5 py-2.5">
                    <Badge tone={toneFor(m.status)}>{m.status}</Badge>
                  </td>
                  <td className="px-5 py-2.5 text-muted-foreground">{relativeTime(m.last_login_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>

      {/* Workspaces */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Workspaces ({data.workspaces.length})</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {data.workspaces.length === 0 ? (
            <p className="text-sm text-muted-foreground/70">No workspaces.</p>
          ) : (
            <div className="divide-y divide-border/60">
              {data.workspaces.map((w) => {
                const wpct = storagePct(w.storage_bytes_used, w.disk_allocation_bytes);
                return (
                  <div key={w.id} className="py-3 first:pt-0 last:pb-0">
                    <div className="flex items-center justify-between">
                      <span className="text-sm font-medium text-foreground">{w.name}</span>
                      <span className="text-xs text-muted-foreground">
                        {formatBytes(w.storage_bytes_used)}
                        {w.disk_allocation_bytes > 0 ? ` / ${formatBytes(w.disk_allocation_bytes)}` : ""}
                      </span>
                    </div>
                    {wpct !== null && (
                      <div className="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-muted">
                        <div className="h-full rounded-full bg-primary" style={{ width: `${wpct}%` }} />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          <p className="pt-1 text-[11px] text-muted-foreground/60">
            File-level browsing (privacy-first, structure only) arrives in a later phase.
          </p>
        </CardContent>
      </Card>

      <p className="text-center text-[11px] text-muted-foreground/50">Account ID {data.id} · {formatDateTime(data.created_at)}</p>
    </div>
  );
}

function Row({
  label,
  value,
  mono,
  badge,
}: {
  label: string;
  value: string;
  mono?: boolean;
  badge?: "neutral" | "green" | "amber" | "red" | "blue" | "slate";
}) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-muted-foreground">{label}</span>
      {badge ? (
        <Badge tone={badge}>{value}</Badge>
      ) : (
        <span className={mono ? "font-mono text-xs text-foreground" : "text-foreground"}>{value}</span>
      )}
    </div>
  );
}
