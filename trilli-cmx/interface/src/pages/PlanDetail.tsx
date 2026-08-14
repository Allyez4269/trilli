import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Star, Pencil } from "lucide-react";

import { api, type Plan } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { useAuth } from "@/contexts/AuthContext";
import { formatBytes, formatCents } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock } from "@/components/DataState";
import StepUpModal from "@/components/StepUpModal";

const SUPPORT = ["Standard", "Priority", "Dedicated"];

// Status transitions offered per current status (SPEC §5.4.3 lifecycle).
const NEXT_STATUS: Record<string, { to: string; label: string; destructive?: boolean }[]> = {
  draft: [{ to: "available", label: "Publish" }],
  available: [{ to: "retired", label: "Retire" }],
  retired: [
    { to: "available", label: "Re-publish" },
    { to: "archived", label: "Archive", destructive: true },
  ],
  archived: [],
};

export default function PlanDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { operator } = useAuth();
  const { data, loading, error, reload } = useApiGet<Plan>(`/api/cmx/plans/${id}`);
  const [pendingStatus, setPendingStatus] = useState<{ to: string; label: string; destructive?: boolean } | null>(null);
  const isGlobal = operator?.role === "global";

  if (loading) return <LoadingBlock />;
  if (error) return <ErrorBlock message={error} />;
  if (!data) return <ErrorBlock message="Plan not found." />;

  const cap = (n?: number | null, fmt?: (v: number) => string) =>
    n == null ? "Unlimited" : fmt ? fmt(n) : String(n);

  return (
    <div className="mx-auto max-w-[71.4rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate("/catalog")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Catalog
      </button>

      <div className="flex items-start justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
            {data.name}
            {data.is_popular && <Star className="h-5 w-5 fill-primary text-primary" />}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {data.code}
            {data.tagline ? ` · ${data.tagline}` : ""}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge tone={toneFor(data.status === "available" ? "active" : data.status)}>{data.status}</Badge>
          {isGlobal && (
            <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => navigate(`/catalog/${id}/edit`)}>
              <Pencil className="h-3.5 w-3.5" /> Edit
            </Button>
          )}
        </div>
      </div>

      {isGlobal && NEXT_STATUS[data.status]?.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">Lifecycle:</span>
          {NEXT_STATUS[data.status].map((s) => (
            <Button
              key={s.to}
              variant={s.destructive ? "destructive" : "secondary"}
              size="sm"
              className="h-8"
              onClick={() => setPendingStatus(s)}
            >
              {s.label}
            </Button>
          ))}
        </div>
      )}

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Pricing</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2.5 text-sm">
            <Row label="Monthly" value={data.price_monthly_cents ? formatCents(data.price_monthly_cents, "monthly") : "Free"} />
            <Row label="Annual discount" value={data.annual_discount_pct ? `${data.annual_discount_pct}%` : "—"} />
            <Row label="Included seats" value={cap(data.max_users)} />
            <Row label="Min seats" value={String(data.min_seats || 0)} />
            <Row label="Extra seat" value={data.per_seat_cents ? `${formatCents(data.per_seat_cents)}/mo` : "Not sold"} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Allowances</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2.5 text-sm">
            <Row label="Storage" value={cap(data.max_storage_bytes, formatBytes)} />
            <Row label="Transfer / mo" value={cap(data.max_transfer_bytes_month, formatBytes)} />
            <Row label="Workspaces" value={cap(data.max_workspaces)} />
            <Row label="Max file size" value={cap(data.max_file_size_bytes, formatBytes)} />
            <Row label="Trash retention" value={data.trash_retention_days != null ? `${data.trash_retention_days} days` : "Unlimited"} />
            <Row label="API access" value={data.api_access ? "Yes" : "No"} />
            <Row label="Support" value={SUPPORT[data.support_level] ?? `Level ${data.support_level}`} />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Availability & inventory</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2.5 text-sm">
          <Row
            label="Inventory"
            value={
              data.max_subscriptions == null
                ? "Unlimited"
                : `${data.account_count} sold · ${data.remaining ?? 0} remaining of ${data.max_subscriptions}`
            }
          />
          <Row label="Available from" value={data.available_from ? new Date(data.available_from).toLocaleString() : "Always"} />
          <Row label="Available until" value={data.available_until ? new Date(data.available_until).toLocaleString() : "No expiry"} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Stripe mapping & footprint</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2.5 text-sm">
          <Row label="Subscribers" value={`${data.account_count} accounts · ${data.member_count} members`} />
          <Row label="Stripe product" value={data.stripe_product_id || "—"} mono />
          <Row label="Price (monthly)" value={data.stripe_price_monthly_id || "—"} mono />
          <Row label="Price (annual)" value={data.stripe_price_annual_id || "—"} mono />
        </CardContent>
      </Card>

      {data.marketing_lines.length > 0 && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Marketing lines</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="list-inside list-disc space-y-1 text-sm text-muted-foreground">
              {data.marketing_lines.map((l, i) => (
                <li key={i}>{l}</li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      {pendingStatus && (
        <StepUpModal
          title={`${pendingStatus.label} this plan?`}
          description={
            pendingStatus.to === "archived"
              ? "Archiving is blocked if any account still rides on this plan."
              : `Change the plan's status to "${pendingStatus.to}".`
          }
          confirmLabel={pendingStatus.label}
          destructive={pendingStatus.destructive}
          onConfirm={async () => {
            await api.post(`/api/cmx/plans/${id}/status`, { status: pendingStatus.to });
            reload();
          }}
          onClose={() => setPendingStatus(null)}
        />
      )}
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "truncate font-mono text-xs text-foreground" : "text-foreground"}>{value}</span>
    </div>
  );
}
