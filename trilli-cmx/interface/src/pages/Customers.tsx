import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, Gift, LifeBuoy } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type { CustomerListItem } from "@/lib/api";
import { formatDate, relativeTime } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";

export default function Customers() {
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const path = `/api/cmx/customers${q.trim() ? `?q=${encodeURIComponent(q.trim())}` : ""}`;
  const { data, loading, error } = useApiGet<{ customers: CustomerListItem[] }>(path);
  const customers = data?.customers ?? [];
  const pager = usePager(customers);

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div className="flex items-start justify-between animate-fade-up">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Customers</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            The people and organizations Trilli sells to — across every account.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" className="h-8 gap-1.5" onClick={() => navigate("/support")}>
            <LifeBuoy className="h-4 w-4" /> Support
          </Button>
          <Button variant="outline" className="h-8 gap-1.5" onClick={() => navigate("/ambassadors")}>
            <Gift className="h-4 w-4" /> Ambassadors
          </Button>
        </div>
      </div>

      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search by name, email, or company…"
          className="h-8 pl-8"
        />
      </div>

      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={7} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : customers.length === 0 ? (
          <EmptyBlock label="No customers found." description={q ? "Try a different search." : undefined} />
        ) : (
          <>
          <table className="w-full text-sm">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Customer</th>
                <th className="px-4 py-2.5 font-medium">Stage</th>
                <th className="px-4 py-2.5 font-medium">Accounts</th>
                <th className="px-4 py-2.5 font-medium">Last seen</th>
                <th className="px-4 py-2.5 font-medium">Joined</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((c) => (
                <tr
                  key={c.identity_user_id}
                  onClick={() => navigate(`/customers/${c.identity_user_id}`)}
                  className="cursor-pointer border-b border-border/60 last:border-0 transition-colors hover:bg-muted/50"
                >
                  <td className="px-4 py-3">
                    <div className="font-medium text-foreground">{c.name}</div>
                    <div className="text-xs text-muted-foreground">
                      {c.email}
                      {c.company ? ` · ${c.company}` : ""}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <Badge tone={toneFor(c.lifecycle_stage)}>{c.lifecycle_stage}</Badge>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{c.account_count}</td>
                  <td className="px-4 py-3 text-muted-foreground">{relativeTime(c.last_login_at)}</td>
                  <td className="px-4 py-3 text-muted-foreground">{formatDate(c.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>
    </div>
  );
}
