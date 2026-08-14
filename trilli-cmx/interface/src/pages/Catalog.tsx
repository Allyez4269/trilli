import { useNavigate } from "react-router-dom";
import { Star, Plus } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type { Plan } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { formatBytes, formatCents } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";

export default function Catalog() {
  const navigate = useNavigate();
  const { operator } = useAuth();
  const { data, loading, error } = useApiGet<{ plans: Plan[] }>("/api/cmx/plans");
  const plans = data?.plans ?? [];
  const pager = usePager(plans);

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Catalog</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Everything Trilli sells. <span className="text-foreground">Plans</span> · Add-ons and Products
            arrive later.
          </p>
        </div>
        {operator?.role === "global" && (
          <Button onClick={() => navigate("/catalog/new")} className="h-8 gap-1.5">
            <Plus className="h-4 w-4" /> New plan
          </Button>
        )}
      </div>

      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={5} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : plans.length === 0 ? (
          <EmptyBlock label="No plans yet." description="Global admins can create the first plan." />
        ) : (
          <>
          <table className="w-full text-sm">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Plan</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Price</th>
                <th className="px-4 py-2.5 font-medium">Storage</th>
                <th className="px-4 py-2.5 font-medium">Seats</th>
                <th className="px-4 py-2.5 font-medium">Subscribers</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((p) => (
                <tr
                  key={p.id}
                  onClick={() => navigate(`/catalog/${p.id}`)}
                  className="cursor-pointer border-b border-border/60 last:border-0 transition-colors hover:bg-muted/50"
                >
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-1.5 font-medium text-foreground">
                      {p.name}
                      {p.is_popular && <Star className="h-3.5 w-3.5 fill-primary text-primary" />}
                    </div>
                    <div className="text-xs text-muted-foreground">{p.code}</div>
                  </td>
                  <td className="px-4 py-3">
                    <Badge tone={toneFor(p.status === "available" ? "active" : p.status)}>{p.status}</Badge>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">
                    {p.price_monthly_cents ? formatCents(p.price_monthly_cents, "monthly") : "Free"}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">
                    {p.max_storage_bytes ? formatBytes(p.max_storage_bytes) : "Unlimited"}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">
                    {p.max_users ?? "∞"}
                    {p.per_seat_cents ? ` · +${formatCents(p.per_seat_cents)}/seat` : ""}
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">
                    {p.account_count} {p.account_count === 1 ? "account" : "accounts"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>
      <p className="text-[11px] text-muted-foreground/60">
        Global admins can create and edit plans. Availability/inventory and comp invites land in
        upcoming slices.
      </p>
    </div>
  );
}
