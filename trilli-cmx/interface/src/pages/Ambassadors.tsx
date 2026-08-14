import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  Send,
  Gift,
  Infinity as InfinityIcon,
  Ban,
  Trash2,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";

import { api, type CompInvite, type Plan } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { useAuth } from "@/contexts/AuthContext";
import { formatDate, formatDateTime } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { LoadingBlock, ErrorBlock, EmptyBlock } from "@/components/DataState";
import StepUpModal from "@/components/StepUpModal";
import ConfirmModal from "@/components/ConfirmModal";

const PAGE_SIZE = 7;

export default function Ambassadors() {
  const navigate = useNavigate();
  const { operator } = useAuth();
  const isGlobal = operator?.role === "global";
  const { data, loading, error, reload } = useApiGet<{ invites: CompInvite[] }>("/api/cmx/comp-invites");
  const { data: plansData } = useApiGet<{ plans: Plan[] }>("/api/cmx/plans");
  const invites = data?.invites ?? [];
  const plans = (plansData?.plans ?? []).filter((p) => p.status === "available" || p.status === "draft");

  // Send-invite form
  const [email, setEmail] = useState("");
  const [planCode, setPlanCode] = useState("");
  const [termDays, setTermDays] = useState("90");
  const [inviteDays, setInviteDays] = useState("14");
  const [formErr, setFormErr] = useState<string | null>(null);
  const [showSend, setShowSend] = useState(false);
  // Revoke/delete confirmation (no step-up 2FA — low-risk registry actions).
  const [pending, setPending] = useState<{ id: number; mode: "revoke" | "delete"; email: string } | null>(null);
  const [page, setPage] = useState(1);

  const pageCount = Math.max(1, Math.ceil(invites.length / PAGE_SIZE));
  const safePage = Math.min(page, pageCount);
  const pageRows = invites.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);

  const term = Number(termDays);
  const invite = Number(inviteDays);
  const indefinite = term === 0;

  function openSend(e: FormEvent) {
    e.preventDefault();
    setFormErr(null);
    if (!email.trim() || !planCode) {
      setFormErr("Add a recipient email and pick a plan.");
      return;
    }
    if (!Number.isFinite(term) || term < 0) {
      setFormErr("Duration must be 0 (never expires) or a positive number of days.");
      return;
    }
    if (!Number.isFinite(invite) || invite <= 0) {
      setFormErr("Invite expiration must be a positive number of days.");
      return;
    }
    setShowSend(true);
  }

  async function sendInvite() {
    await api.post("/api/cmx/comp-invites", {
      email: email.trim(),
      plan_code: planCode,
      free_term_days: term,
      invite_valid_for_days: invite,
    });
    setEmail("");
    setPlanCode("");
    reload();
  }

  const planName = plans.find((p) => p.code === planCode)?.name ?? planCode;
  const sendSummary = `Grant ${email.trim() || "this person"} a free ${planName || "plan"} account ${
    indefinite ? "with no expiry" : `for ${term} days`
  }. They'll get an email to set it up — no payment required.`;

  return (
    <div className="mx-auto max-w-[81.6rem] space-y-6">
      <button
        type="button"
        onClick={() => navigate("/customers")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Customers
      </button>

      {/* Program header */}
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <Gift className="h-6 w-6 text-primary" /> Ambassadors
        </h1>
        <p className="mt-1.5 max-w-2xl text-sm leading-relaxed text-muted-foreground">
          The Ambassador program gives complimentary Trilli accounts to VIPs, partners, and promoters.
          An invite bypasses payment entirely — the recipient sets a password and lands on a fully
          provisioned account on the plan you choose, free for the duration you set. When the term ends
          the account drops to read-only; set duration to{" "}
          <span className="font-medium text-foreground">0</span> to grant permanent access.
        </p>
      </div>

      {/* Send a comp invite — wide form */}
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="border-b border-border bg-muted/30 px-6 py-4">
          <h2 className="text-sm font-semibold text-foreground">Send a comp invite</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            All fields except the recipient are pre-filled with sensible defaults.
          </p>
        </div>
        <form onSubmit={openSend} className="space-y-5 p-6">
          <div className="grid grid-cols-1 gap-5 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label className="text-xs">Recipient email</Label>
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="vip@example.com"
                className="h-8"
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Plan</Label>
              <select
                value={planCode}
                onChange={(e) => setPlanCode(e.target.value)}
                className="h-8 w-full rounded-md border border-input bg-background px-2.5 text-sm text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
              >
                <option value="">Select a plan…</option>
                {plans.map((p) => (
                  <option key={p.id} value={p.code}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Duration (days)</Label>
              <Input
                type="number"
                min={0}
                value={termDays}
                onChange={(e) => setTermDays(e.target.value)}
                className="h-8"
              />
              <p className="flex items-center gap-1 text-[11px] text-muted-foreground">
                {indefinite ? (
                  <>
                    <InfinityIcon className="h-3 w-3 text-primary" />
                    Never expires — access is permanent.
                  </>
                ) : (
                  <>How long the free account stays active. Enter 0 to never expire.</>
                )}
              </p>
            </div>
            <div className="space-y-1.5">
              <Label className="text-xs">Invite expiration (days)</Label>
              <Input
                type="number"
                min={1}
                value={inviteDays}
                onChange={(e) => setInviteDays(e.target.value)}
                className="h-8"
              />
              <p className="text-[11px] text-muted-foreground">
                If the invite isn't accepted within this window, the link stops working.
              </p>
            </div>
          </div>

          {formErr && <p className="text-sm text-destructive">{formErr}</p>}

          <div className="flex justify-end">
            <Button type="submit" className="h-8 gap-1.5">
              <Send className="h-4 w-4" /> Send invite
            </Button>
          </div>
        </form>
      </div>

      {/* Sent-invite registry */}
      <div>
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-sm font-semibold text-foreground">
            {isGlobal ? "All comp invites" : "Your comp invites"}
          </h2>
          {invites.length > 0 && (
            <span className="text-xs text-muted-foreground">
              {invites.length} {invites.length === 1 ? "invite" : "invites"}
            </span>
          )}
        </div>
        <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
          {loading ? (
            <LoadingBlock />
          ) : error ? (
            <ErrorBlock message={error} />
          ) : invites.length === 0 ? (
            <EmptyBlock label="No comp invites sent yet." />
          ) : (
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-card">
                <tr className="text-left text-[10.2px] uppercase tracking-wide text-muted-foreground">
                  <th className="px-4 py-2.5 font-medium">Recipient</th>
                  <th className="px-4 py-2.5 font-medium">Plan / duration</th>
                  <th className="px-4 py-2.5 font-medium">Status</th>
                  <th className="px-4 py-2.5 font-medium">Account</th>
                  <th className="px-4 py-2.5 font-medium">Sent by</th>
                  <th className="px-4 py-2.5 text-center font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {pageRows.map((iv) => (
                  <tr key={iv.id} className="border-b border-border/60 transition-colors last:border-0 hover:bg-muted/40">
                    <td className="px-4 py-3 font-medium text-foreground">{iv.email}</td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {iv.plan_name}
                      <span className="mx-1 text-border">·</span>
                      {iv.free_term_days === 0 ? (
                        <span className="inline-flex items-center gap-0.5 text-foreground">
                          <InfinityIcon className="h-3 w-3" /> no expiry
                        </span>
                      ) : (
                        `${iv.free_term_days}d`
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <Badge tone={toneFor(iv.status === "registered" ? "active" : iv.status)}>{iv.status}</Badge>
                    </td>
                    <td className="px-4 py-3 text-muted-foreground">
                      {iv.tenant_id ? (
                        <>
                          <button
                            type="button"
                            onClick={() => navigate(`/accounts/${iv.tenant_id}`)}
                            className="text-primary hover:underline"
                          >
                            {iv.tenant_name || `tenant ${iv.tenant_id}`}
                          </button>
                          <div className="text-[11px] text-muted-foreground/60">
                            {iv.comp_expires_at ? `expires ${formatDate(iv.comp_expires_at)}` : "no expiry"}
                          </div>
                        </>
                      ) : iv.status === "invited" ? (
                        <span className="text-[11px] text-muted-foreground/60">
                          link expires {formatDate(iv.invite_expires_at)}
                        </span>
                      ) : (
                        <span className="text-muted-foreground/60">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">
                      {iv.invited_by_email}
                      <div className="text-[11px] text-muted-foreground/60">{formatDateTime(iv.created_at)}</div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-center gap-1">
                        {iv.status === "invited" && (
                          <button
                            type="button"
                            title="Revoke invite"
                            aria-label="Revoke invite"
                            onClick={() => setPending({ id: iv.id, mode: "revoke", email: iv.email })}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-amber-600"
                          >
                            <Ban className="h-4 w-4" />
                          </button>
                        )}
                        <button
                          type="button"
                          title="Delete invite"
                          aria-label="Delete invite"
                          onClick={() => setPending({ id: iv.id, mode: "delete", email: iv.email })}
                          className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-destructive"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* Pagination — only when the registry exceeds one page (7 records). */}
        {invites.length > PAGE_SIZE && (
          <div className="mt-3 flex items-center justify-between">
            <span className="text-xs text-muted-foreground">
              Page {safePage} of {pageCount}
            </span>
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                onClick={() => setPage(safePage - 1)}
                disabled={safePage <= 1}
                className="inline-flex h-7 items-center gap-1 rounded-md border border-border bg-card px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
              >
                <ChevronLeft className="h-3.5 w-3.5" /> Prev
              </button>
              <button
                type="button"
                onClick={() => setPage(safePage + 1)}
                disabled={safePage >= pageCount}
                className="inline-flex h-7 items-center gap-1 rounded-md border border-border bg-card px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
              >
                Next <ChevronRight className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        )}
      </div>

      {showSend && (
        <StepUpModal
          title="Send comp invite?"
          description={sendSummary}
          confirmLabel="Send invite"
          onConfirm={sendInvite}
          onClose={() => setShowSend(false)}
        />
      )}
      {pending && (
        <ConfirmModal
          title={pending.mode === "revoke" ? "Revoke this invite?" : "Delete this invite?"}
          description={
            pending.mode === "revoke"
              ? `The setup link for ${pending.email} will stop working. You can send a new invite later.`
              : `Permanently remove the comp invite for ${pending.email} from this list. This does not affect any account that's already been created.`
          }
          confirmLabel={pending.mode === "revoke" ? "Revoke invite" : "Delete invite"}
          destructive={pending.mode === "delete"}
          icon={pending.mode === "revoke" ? Ban : Trash2}
          onConfirm={async () => {
            await api.post(`/api/cmx/comp-invites/${pending.id}/${pending.mode}`);
            reload();
          }}
          onClose={() => setPending(null)}
        />
      )}
    </div>
  );
}
