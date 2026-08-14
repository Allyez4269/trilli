import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, LifeBuoy } from "lucide-react";

import { useApiGet } from "@/lib/useApi";
import type { SupportTicket } from "@/lib/api";
import { relativeTime } from "@/lib/format";
import { Badge, toneFor, type Tone } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";

const FILTERS: { key: string; label: string }[] = [
  { key: "open_active", label: "Open" },
  { key: "all", label: "All" },
  { key: "resolved", label: "Resolved" },
  { key: "closed", label: "Closed" },
];

function severityTone(sev: string): Tone {
  if (sev === "urgent") return "red";
  if (sev === "high") return "amber";
  return "neutral";
}

// Cross-tenant support triage (SPEC §6.8). Lists every tenant's tickets;
// operators open one to reply or leave an internal note.
export default function Support() {
  const navigate = useNavigate();
  const [filter, setFilter] = useState("open_active");
  const [q, setQ] = useState("");
  const path = `/api/cmx/support/tickets?status=${filter}${q.trim() ? `&q=${encodeURIComponent(q.trim())}` : ""}`;
  const { data, loading, error } = useApiGet<{ tickets: SupportTicket[] }>(path);
  const tickets = data?.tickets ?? [];
  const pager = usePager(tickets);

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <LifeBuoy className="h-6 w-6 text-primary" /> Support
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Every tenant's support tickets. Open one to reply as Trilli Support or leave an internal note.
        </p>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex gap-1">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              type="button"
              onClick={() => setFilter(f.key)}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                filter === f.key ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted"
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>
        <div className="relative max-w-xs flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Ticket #, subject, or email…" className="h-8 pl-8" />
        </div>
      </div>

      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={6} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : tickets.length === 0 ? (
          <EmptyBlock
            label={filter === "open_active" ? "No open tickets." : "No tickets match."}
            description={q ? "Try clearing the search." : filter !== "all" ? "Try switching to All." : undefined}
          />
        ) : (
          <>
          <table className="w-full text-sm">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Ticket</th>
                <th className="px-4 py-2.5 font-medium">Account</th>
                <th className="px-4 py-2.5 font-medium">Severity</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Activity</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((t) => (
                <tr
                  key={t.id}
                  onClick={() => navigate(`/support/${t.id}`)}
                  className="cursor-pointer border-b border-border/60 last:border-0 transition-colors hover:bg-muted/50"
                >
                  <td className="px-4 py-3">
                    <div className="font-medium text-foreground">{t.subject}</div>
                    <div className="text-xs text-muted-foreground">
                      {t.number} · {t.requester_email} · {t.category}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{t.tenant_name || `tenant ${t.tenant_id}`}</td>
                  <td className="px-4 py-3">
                    <Badge tone={severityTone(t.severity)}>{t.severity}</Badge>
                  </td>
                  <td className="px-4 py-3">
                    <Badge tone={toneFor(t.status === "awaiting_customer" ? "pending" : t.status)}>
                      {t.status.replace(/_/g, " ")}
                    </Badge>
                  </td>
                  <td className="px-4 py-3 text-muted-foreground">{relativeTime(t.last_activity_at)}</td>
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
