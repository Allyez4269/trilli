import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type { TenantListItem } from "@/lib/api";
import { formatBytes, formatDate, storagePct } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";
import { cn } from "@/lib/utils";

const STATUSES = ["", "active", "suspended"];

export default function Accounts() {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");

  const params = new URLSearchParams();
  if (q.trim()) params.set("q", q.trim());
  if (status) params.set("status", status);
  const qs = params.toString();
  const { data, loading, error } = useApiGet<{ tenants: TenantListItem[] }>(
    `/api/cmx/tenants${qs ? `?${qs}` : ""}`,
  );
  const tenants = data?.tenants ?? [];
  const pager = usePager(tenants);

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Accounts</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Tenant workspaces — the resources customers own and use.
        </p>
      </div>

      <div className="flex items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search by name or slug…"
            className="h-8 pl-8"
          />
        </div>
        <div className="inline-flex overflow-hidden rounded-md border border-border">
          {STATUSES.map((s) => (
            <button
              key={s || "all"}
              type="button"
              onClick={() => setStatus(s)}
              className={cn(
                "px-3 py-1.5 text-xs font-medium capitalize transition-colors",
                status === s ? "bg-primary text-primary-foreground" : "bg-card text-muted-foreground hover:bg-muted",
              )}
            >
              {s || "All"}
            </button>
          ))}
        </div>
      </div>

      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={7} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : tenants.length === 0 ? (
          <EmptyBlock label="No accounts found." description={q || status ? "Try adjusting your filters." : undefined} />
        ) : (
          <>
          <table className="w-full text-sm">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Account</th>
                <th className="px-4 py-2.5 font-medium">Plan</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Storage</th>
                <th className="px-4 py-2.5 font-medium">Seats</th>
                <th className="px-4 py-2.5 font-medium">Created</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((t) => {
                const pct = storagePct(t.storage_bytes_used, t.storage_bytes_max);
                return (
                  <tr
                    key={t.id}
                    onClick={() => navigate(`/accounts/${t.id}`)}
                    className="cursor-pointer border-b border-border/60 last:border-0 transition-colors hover:bg-muted/50"
                  >
                    <td className="px-4 py-3">
                      <div className="font-medium text-foreground">{t.name}</div>
                      <div className="text-xs text-muted-foreground">{t.owner_email}</div>
                    </td>
                    <td className="px-4 py-3">
                      {t.plan_code ? <Badge tone="slate">{t.plan_code}</Badge> : <span className="text-muted-foreground">—</span>}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-1.5">
                        <Badge tone={toneFor(t.status)}>{t.status}</Badge>
                        {t.lifecycle_state && t.lifecycle_state !== "current" && (
                          <Badge tone={toneFor(t.lifecycle_state)}>{t.lifecycle_state}</Badge>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="text-xs text-muted-foreground">
                        {formatBytes(t.storage_bytes_used)}
                        {pct !== null ? ` · ${pct}%` : ""}
                      </div>
                      {pct !== null && (
                        <div className="mt-1 h-1 w-24 overflow-hidden rounded-full bg-muted">
                          <div
                            className={cn("h-full rounded-full", pct >= 90 ? "bg-destructive" : "bg-primary")}
                            style={{ width: `${pct}%` }}
                          />
                        </div>
                      )}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {t.user_count}
                      {t.extra_seats ? ` (+${t.extra_seats})` : ""}
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">{formatDate(t.created_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>
    </div>
  );
}
