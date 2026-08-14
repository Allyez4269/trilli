import { type FormEvent, useState } from "react";
import { Shield, ScrollText, KeyRound, Users } from "lucide-react";

import { api, type AuditEntry, type VaultEntry, type Operator } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { useAuth } from "@/contexts/AuthContext";
import { formatDateTime, relativeTime, shortIP } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { SkeletonRows, ErrorBlock, EmptyBlock } from "@/components/DataState";
import { usePager, Pager } from "@/components/Pager";
import StepUpModal from "@/components/StepUpModal";

type Tab = "audit" | "vault" | "operators";

// Administration (SPEC §6.7, Global). The operator-action audit (Global sees
// all, CMX sees own — scoped server-side) and a read-only credentials/vault
// inventory (never secret material). Operator management + change-password are
// a later slice.
export default function Administration() {
  const { operator } = useAuth();
  const isGlobal = operator?.role === "global";
  const [tab, setTab] = useState<Tab>("audit");

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <Shield className="h-6 w-6 text-primary" /> Administration
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Operator activity audit and the platform credentials vault.
        </p>
      </div>

      <div className="flex items-center gap-3 border-b border-border">
        <div className="flex gap-1">
          <TabButton active={tab === "audit"} onClick={() => setTab("audit")} icon={ScrollText} label="Operator audit" />
          {isGlobal && <TabButton active={tab === "operators"} onClick={() => setTab("operators")} icon={Users} label="Operators" />}
          {isGlobal && <TabButton active={tab === "vault"} onClick={() => setTab("vault")} icon={KeyRound} label="Vault" />}
        </div>
      </div>

      {tab === "audit" && <AuditTab isGlobal={isGlobal} />}
      {tab === "operators" && isGlobal && <OperatorsTab selfId={operator?.id ?? 0} />}
      {tab === "vault" && <VaultTab />}
    </div>
  );
}

function OperatorsTab({ selfId }: { selfId: number }) {
  const { data, loading, error, reload } = useApiGet<{ operators: Operator[] }>("/api/cmx/admin/operators");
  const operators = data?.operators ?? [];
  const pager = usePager(operators);

  // Create form
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState("cmx");
  const [pw, setPw] = useState("");
  const [formErr, setFormErr] = useState<string | null>(null);
  const [pending, setPending] = useState<null | { run: () => Promise<void>; title: string; desc: string; label: string; destructive?: boolean }>(null);

  function gate(p: { run: () => Promise<void>; title: string; desc: string; label: string; destructive?: boolean }) {
    setPending(p);
  }

  function submitCreate(e: FormEvent) {
    e.preventDefault();
    setFormErr(null);
    if (!email.trim() || pw.length < 12) {
      setFormErr("Email and a 12+ char password are required.");
      return;
    }
    gate({
      title: `Create ${role} operator?`,
      desc: `${email.trim()} will be able to sign in and must enroll 2FA on first login.`,
      label: "Create operator",
      run: async () => {
        await api.post("/api/cmx/admin/operators", { email: email.trim(), name: name.trim(), role, password: pw });
        setEmail(""); setName(""); setPw(""); setRole("cmx");
        reload();
      },
    });
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="p-4">
          <form onSubmit={submitCreate} className="grid grid-cols-1 gap-3 sm:grid-cols-5">
            <div className="space-y-1.5 sm:col-span-2">
              <Label className="text-xs">Email</Label>
              <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} className="h-8" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Name</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} className="h-8" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Role</Label>
              <select value={role} onChange={(e) => setRole(e.target.value)}
                className="h-8 w-full rounded-md border border-input bg-background px-2 text-sm">
                <option value="cmx">CMX</option>
                <option value="global">Global</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Temp password</Label>
              <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} className="h-8" />
            </div>
            <div className="sm:col-span-5">
              {formErr && <p className="mb-2 text-sm text-destructive">{formErr}</p>}
              <Button type="submit" size="sm" className="h-8">Add operator</Button>
            </div>
          </form>
        </CardContent>
      </Card>

      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? <SkeletonRows rows={4} /> : error ? <ErrorBlock message={error} /> : operators.length === 0 ? (
          <EmptyBlock label="No operators yet." />
        ) : (
          <>
          <table className="w-full text-[13px]">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Operator</th>
                <th className="px-4 py-2.5 font-medium">Role</th>
                <th className="px-4 py-2.5 font-medium">Status</th>
                <th className="px-4 py-2.5 font-medium">Last login</th>
                <th className="px-4 py-2.5 font-medium text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((o) => {
                const self = o.id === selfId;
                return (
                  <tr key={o.id} className="border-b border-border/60 last:border-0">
                    <td className="px-4 py-3">
                      <div className="font-medium text-foreground">{o.email}{self && <span className="ml-1 text-xs text-muted-foreground">(you)</span>}</div>
                      <div className="text-xs text-muted-foreground">{o.name || "—"}</div>
                    </td>
                    <td className="px-4 py-3"><Badge tone={o.role === "global" ? "slate" : "neutral"}>{o.role}</Badge></td>
                    <td className="px-4 py-3"><Badge tone={toneFor(o.status)}>{o.status}</Badge></td>
                    <td className="px-4 py-3 text-muted-foreground">{relativeTime(o.last_login_at)}</td>
                    <td className="px-4 py-3 text-right">
                      {!self && (
                        <div className="flex justify-end gap-1.5">
                          {o.status === "locked" && (
                            <ActBtn label="Unlock" onClick={() => gate({
                              title: "Unlock operator?", desc: `Clear the lockout for ${o.email}.`, label: "Unlock",
                              run: async () => { await api.post(`/api/cmx/admin/operators/${o.id}/unlock`); reload(); },
                            })} />
                          )}
                          {o.status !== "suspended" ? (
                            <ActBtn label="Suspend" destructive onClick={() => gate({
                              title: "Suspend operator?", desc: `${o.email} will not be able to sign in.`, label: "Suspend", destructive: true,
                              run: async () => { await api.post(`/api/cmx/admin/operators/${o.id}/status`, { status: "suspended" }); reload(); },
                            })} />
                          ) : (
                            <ActBtn label="Activate" onClick={() => gate({
                              title: "Activate operator?", desc: `Restore sign-in for ${o.email}.`, label: "Activate",
                              run: async () => { await api.post(`/api/cmx/admin/operators/${o.id}/status`, { status: "active" }); reload(); },
                            })} />
                          )}
                          <ActBtn label={o.role === "global" ? "→ CMX" : "→ Global"} onClick={() => gate({
                            title: "Change role?", desc: `Set ${o.email} to ${o.role === "global" ? "CMX" : "Global"}.`, label: "Change role",
                            run: async () => { await api.post(`/api/cmx/admin/operators/${o.id}/role`, { role: o.role === "global" ? "cmx" : "global" }); reload(); },
                          })} />
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>

      {pending && (
        <StepUpModal
          title={pending.title}
          description={pending.desc}
          confirmLabel={pending.label}
          destructive={pending.destructive}
          onConfirm={pending.run}
          onClose={() => setPending(null)}
        />
      )}
    </div>
  );
}

function ActBtn({ label, onClick, destructive }: { label: string; onClick: () => void; destructive?: boolean }) {
  return (
    <Button variant="ghost" size="sm" className={`h-7 text-xs ${destructive ? "text-destructive hover:text-destructive" : ""}`} onClick={onClick}>
      {label}
    </Button>
  );
}

function TabButton({
  active,
  onClick,
  icon: Icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof ScrollText;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`relative flex items-center gap-1.5 px-3 py-2 text-sm font-medium transition-colors ${
        active ? "text-foreground" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      <Icon className="h-4 w-4" /> {label}
      {active && <span className="absolute bottom-0 left-3 right-3 h-0.5 rounded-full bg-primary" />}
    </button>
  );
}

function AuditTab({ isGlobal }: { isGlobal: boolean }) {
  const { data, loading, error } = useApiGet<{ entries: AuditEntry[] }>("/api/cmx/admin/audit?limit=200");
  const entries = data?.entries ?? [];
  const pager = usePager(entries);
  return (
    <>
      <p className="text-xs text-muted-foreground">
        {isGlobal ? "All operator actions across CMX." : "Your own operator actions."} Append-only.
      </p>
      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={6} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : entries.length === 0 ? (
          <EmptyBlock label="No operator actions recorded yet." />
        ) : (
          <>
            <table className="w-full text-[13px]">
              <thead className="sticky top-0 z-10 bg-card">
                <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                  <th className="px-4 py-2.5 font-medium">When</th>
                  <th className="px-4 py-2.5 font-medium">Operator</th>
                  <th className="px-4 py-2.5 font-medium">Action</th>
                  <th className="px-4 py-2.5 font-medium">Summary</th>
                  <th className="px-4 py-2.5 font-medium">From</th>
                </tr>
              </thead>
              <tbody>
                {pager.rows.map((e) => (
                  <tr key={e.id} className="border-b border-border/60 text-muted-foreground last:border-0">
                    <td className="whitespace-nowrap px-4 py-2.5">{formatDateTime(e.created_at)}</td>
                    <td className="px-4 py-2.5">
                      <span className="text-foreground">{e.operator_email}</span>
                      <span className="ml-1.5 capitalize text-muted-foreground/70">· {e.role_snapshot}</span>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="font-mono text-[12px] text-foreground">{e.action}</span>
                    </td>
                    <td className="px-4 py-2.5">{e.summary || "—"}</td>
                    <td className="whitespace-nowrap px-4 py-2.5">
                      <span className="font-mono text-[12px]">{shortIP(e.ip)}</span>
                      {(e.country_code || e.region) && (
                        <span className="ml-1.5 text-muted-foreground/70">
                          · {[e.country_code, e.region].filter(Boolean).join(", ")}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>
    </>
  );
}

function VaultTab() {
  const { data, loading, error } = useApiGet<{ entries: VaultEntry[] }>("/api/cmx/admin/vault");
  const entries = data?.entries ?? [];
  const pager = usePager(entries);
  return (
    <>
      <p className="text-xs text-muted-foreground">
        Stored secrets — provider, key, environment, and last 4 only. Secret material is never displayed.
      </p>
      <div className="overflow-clip rounded-xl border border-border bg-card">
        {loading ? (
          <SkeletonRows rows={4} />
        ) : error ? (
          <ErrorBlock message={error} />
        ) : entries.length === 0 ? (
          <EmptyBlock label="No credentials stored." />
        ) : (
          <>
          <table className="w-full text-[13px]">
            <thead className="sticky top-0 z-10 bg-card">
              <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Provider</th>
                <th className="px-4 py-2.5 font-medium">Key</th>
                <th className="px-4 py-2.5 font-medium">Env</th>
                <th className="px-4 py-2.5 font-medium">Last 4</th>
                <th className="px-4 py-2.5 font-medium">Active</th>
                <th className="px-4 py-2.5 font-medium">Updated</th>
              </tr>
            </thead>
            <tbody>
              {pager.rows.map((e) => (
                <tr key={e.id} className="border-b border-border/60 text-muted-foreground last:border-0">
                  <td className="px-4 py-2.5 text-foreground">{e.provider}</td>
                  <td className="px-4 py-2.5">{e.key_name}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={e.environment === "live" ? "green" : "neutral"}>{e.environment}</Badge>
                  </td>
                  <td className="px-4 py-2.5 font-mono text-[12px]">{e.last4 ? `••••${e.last4}` : "—"}</td>
                  <td className="px-4 py-2.5">
                    <Badge tone={e.is_active ? "green" : "neutral"}>{e.is_active ? "active" : "inactive"}</Badge>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">{formatDateTime(e.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager {...pager} onPage={pager.setPage} />
          </>
        )}
      </div>
    </>
  );
}
