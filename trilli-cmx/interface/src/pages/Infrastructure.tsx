import { useState } from "react";
import { Server, CheckCircle2, XCircle, Database, KeyRound, Globe, Lock, Play, Activity } from "lucide-react";

import { api } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import type { InfraJob, InfraHealth, InfraCost } from "@/lib/api";
import { formatBytes, relativeTime, formatDateTimeSec } from "@/lib/format";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { LoadingBlock, SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";
import StepUpModal from "@/components/StepUpModal";

function pct(n: number, d: number): number {
  return d > 0 ? Math.round((n / d) * 100) : 0;
}

// Infrastructure dashboard (SPEC §6.5): background jobs, system health, storage
// tiering, and Azure transfer/cost. Read-only (Option C); run-now and the
// tiering-mode toggle are a later slice.
export default function Infrastructure() {
  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <Server className="h-6 w-6 text-primary" /> Infrastructure
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Platform health, background jobs, storage tiering, and transfer cost. Read-only for now.
        </p>
      </div>

      <HealthSection />
      <JobsSection />
      <CostSection />
    </div>
  );
}

const GB = 1024 * 1024 * 1024;

function HealthSection() {
  const { data: h, loading, error } = useApiGet<InfraHealth>("/api/cmx/infra/health");
  if (loading) return <LoadingBlock />;
  if (error || !h) return <ErrorBlock message={error || "Health unavailable."} />;
  const encPct = pct(h.files_encrypted, h.files_total);
  const totalBytes = h.hot_bytes + h.cool_bytes + h.cold_bytes;
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">System health</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2.5 text-sm">
          <HealthRow icon={Database} label="App schema version" ok={!h.db_dirty}
            value={`v${h.db_version}${h.db_dirty ? " (dirty!)" : ""}`} />
          <HealthRow icon={KeyRound} label="Master key" ok={h.master_key_configured}
            value={h.master_key_configured ? "configured" : "DEV FALLBACK"} />
          <HealthRow icon={Globe} label="GeoIP" ok={h.geoip_ready}
            value={h.geoip_ready ? "ready" : "not loaded"} />
          <HealthRow icon={Lock} label="Blob encryption" ok={h.files_legacy === 0}
            value={`${encPct}% (${h.files_encrypted.toLocaleString()}/${h.files_total.toLocaleString()})`} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Storage tiering</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <TierPie hot={h.hot_bytes} cool={h.cool_bytes} cold={h.cold_bytes} total={totalBytes} />
          <div className="flex items-center justify-between border-t border-border/60 pt-2.5 text-[12px]">
            <span className="text-muted-foreground">Total stored</span>
            <span className="tabular-nums text-foreground">{(totalBytes / GB).toFixed(2)} GB</span>
          </div>
          <div className="flex items-center justify-between text-[12px]">
            <span className="text-muted-foreground">Last tiering run</span>
            <span className="flex items-center gap-1.5">
              {h.tiering_last_run ? (
                <>
                  <span className="text-foreground">{formatDateTimeSec(h.tiering_last_run)}</span>
                  {h.tiering_last_ok ? (
                    <CheckCircle2 className="h-3.5 w-3.5 text-emerald-600" />
                  ) : (
                    <XCircle className="h-3.5 w-3.5 text-destructive" />
                  )}
                </>
              ) : (
                <span className="text-muted-foreground">never run</span>
              )}
            </span>
          </div>
          <p className="text-[11px] text-muted-foreground/70">
            Tiering runs automatically every cycle. Files re-tier by age &amp; size; access promotes them back to Hot.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

// Tier palette — warm→cool→neutral (Hot amber, Cool sky, Cold slate). Red is
// deliberately avoided so the chart doesn't read as an error/alert.
const TIERS = [
  { key: "hot", label: "Hot", stroke: "stroke-amber-400", dot: "bg-amber-400" },
  { key: "cool", label: "Cool", stroke: "stroke-sky-500", dot: "bg-sky-500" },
  { key: "cold", label: "Cold", stroke: "stroke-slate-400", dot: "bg-slate-400" },
] as const;

// TierPie renders the Hot/Cool/Cold distribution as a donut (r chosen so the
// circumference ≈ 100, letting share-% drive stroke-dasharray directly).
function TierPie({ hot, cool, cold, total }: { hot: number; cool: number; cold: number; total: number }) {
  const vals: Record<string, number> = { hot, cool, cold };
  const share = (b: number) => (total > 0 ? (b / total) * 100 : 0);
  const R = 15.9155; // circumference ≈ 100
  let cursor = 0;
  return (
    <div className="flex items-center gap-5">
      <svg viewBox="0 0 36 36" className="h-24 w-24 flex-shrink-0 -rotate-90">
        <circle cx="18" cy="18" r={R} fill="none" strokeWidth="4" className="stroke-muted" />
        {TIERS.map((t) => {
          const v = share(vals[t.key]);
          if (v <= 0) return null;
          const seg = (
            <circle
              key={t.key}
              cx="18"
              cy="18"
              r={R}
              fill="none"
              strokeWidth="4"
              className={t.stroke}
              strokeDasharray={`${v} ${100 - v}`}
              strokeDashoffset={-cursor}
            />
          );
          cursor += v;
          return seg;
        })}
      </svg>
      <div className="flex-1 space-y-1.5">
        {TIERS.map((t) => (
          <div key={t.key} className="flex items-center gap-2 text-[12px]">
            <span className={`h-2.5 w-2.5 flex-shrink-0 rounded-[3px] ${t.dot}`} />
            <span className="text-muted-foreground">{t.label}</span>
            <span className="ml-auto tabular-nums text-foreground">{share(vals[t.key]).toFixed(0)}%</span>
            <span className="w-16 text-right tabular-nums text-muted-foreground/70">{formatBytes(vals[t.key])}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function HealthRow({
  icon: Icon,
  label,
  value,
  ok,
}: {
  icon: typeof Database;
  label: string;
  value: string;
  ok: boolean;
}) {
  return (
    <div className="flex items-center justify-between">
      <span className="flex items-center gap-2 text-muted-foreground">
        <Icon className="h-4 w-4" /> {label}
      </span>
      <span className="flex items-center gap-1.5 text-foreground">
        {value}
        {ok ? (
          <CheckCircle2 className="h-4 w-4 text-emerald-600" />
        ) : (
          <XCircle className="h-4 w-4 text-destructive" />
        )}
      </span>
    </div>
  );
}

function JobsSection() {
  const { data, loading, error, reload } = useApiGet<{ jobs: InfraJob[] }>("/api/cmx/infra/jobs");
  const jobs = data?.jobs ?? [];
  const pager = usePager(jobs);
  const [pending, setPending] = useState<string | null>(null);

  async function runJob(job: string) {
    const r = await api.post<{ ran: boolean; node: string }>(`/api/cmx/infra/jobs/${job}/run`);
    // ran=false means another node holds the lock — still a valid outcome.
    void r;
    reload();
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-sm">Background jobs</CardTitle>
        <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setPending("diagnostic-ping")}>
          <Activity className="h-3.5 w-3.5" /> Ping
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        {loading ? (
          <SkeletonRows rows={5} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : jobs.length === 0 ? (
          <EmptyBlock label="No jobs have run yet." />
        ) : (
          <>
          <table className="w-full text-[13px]">
            <thead>
              <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Job</th>
                <th className="px-4 py-2.5 font-medium">Last run</th>
                <th className="px-4 py-2.5 font-medium">Node</th>
                <th className="px-4 py-2.5 font-medium">Result</th>
                <th className="px-4 py-2.5 font-medium">Runs</th>
                <th className="px-4 py-2.5 font-medium text-right"></th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((j) => (
                <tr key={j.job} className="border-b border-border/60 text-muted-foreground last:border-0">
                  <td className="px-4 py-2.5 text-foreground">{j.job}</td>
                  <td className="px-4 py-2.5">{relativeTime(j.last_finished_at || undefined)}</td>
                  <td className="px-4 py-2.5 font-mono text-[12px]">{j.last_node || "—"}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={j.last_ok ? "green" : "red"}>{j.last_ok ? "ok" : "failed"}</Badge>
                    {j.last_note && <span className="ml-2 text-xs text-muted-foreground">{j.last_note}</span>}
                  </td>
                  <td className="px-4 py-2.5 tabular-nums">{j.run_count.toLocaleString()}</td>
                  <td className="px-4 py-2.5 text-right">
                    <Button variant="ghost" size="sm" className="h-7 gap-1 text-xs" onClick={() => setPending(j.job)}>
                      <Play className="h-3.5 w-3.5" /> Run now
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </CardContent>

      {pending && (
        <StepUpModal
          title={pending === "diagnostic-ping" ? "Run cluster ping?" : `Run “${pending}” now?`}
          description={
            pending === "diagnostic-ping"
              ? "A no-op self-test that verifies the cluster advisory-lock round-trip on this node."
              : "Triggers this background job immediately under the cluster lock. If another node is mid-run, it's skipped."
          }
          confirmLabel="Run now"
          onConfirm={() => runJob(pending)}
          onClose={() => setPending(null)}
        />
      )}
    </Card>
  );
}

function CostSection() {
  const { data: c, loading, error } = useApiGet<InfraCost>("/api/cmx/infra/cost");
  if (loading) return <LoadingBlock />;
  if (error || !c) return <ErrorBlock message={error || "Cost unavailable."} />;
  const top = c.top_tenants ?? [];
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">
          Transfer / cost {c.period_start ? `· ${c.period_start}` : ""}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {!c.period_start ? (
          <EmptyBlock label="No transfer usage recorded yet." />
        ) : (
          <>
            <div className="flex gap-6 text-sm">
              <div>
                <div className="text-xs uppercase tracking-wide text-muted-foreground">Ingress</div>
                <div className="text-lg font-semibold text-foreground">{formatBytes(c.total_bytes_in)}</div>
              </div>
              <div>
                <div className="text-xs uppercase tracking-wide text-muted-foreground">Egress (billable)</div>
                <div className="text-lg font-semibold text-foreground">{formatBytes(c.total_bytes_out)}</div>
              </div>
            </div>
            <p className="text-[11px] text-muted-foreground/70">
              Egress is not reducible by storage tiering. Azure prices are hard-coded constants (no live billing feed).
            </p>
            {top.length > 0 && (
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                    <th className="px-2 py-2 font-medium">Account</th>
                    <th className="px-2 py-2 font-medium">Egress</th>
                    <th className="px-2 py-2 font-medium">Ingress</th>
                  </tr>
                </thead>
                <tbody>
                  {top.map((r) => (
                    <tr key={r.tenant_id} className="border-b border-border/60 last:border-0">
                      <td className="px-2 py-2 text-foreground">{r.tenant_name || `tenant ${r.tenant_id}`}</td>
                      <td className="px-2 py-2 text-muted-foreground">{formatBytes(r.bytes_out)}</td>
                      <td className="px-2 py-2 text-muted-foreground">{formatBytes(r.bytes_in)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
