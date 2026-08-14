import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Send, StickyNote } from "lucide-react";

import { api, type SupportTicketDetail } from "@/lib/api";
import { useApiGet } from "@/lib/useApi";
import { formatDateTime } from "@/lib/format";
import { Badge, toneFor } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { LoadingBlock, ErrorBlock } from "@/components/DataState";
import StepUpModal from "@/components/StepUpModal";

const STATUSES = ["awaiting_customer", "pending", "resolved", "closed"];

export default function SupportTicket() {
  const { id } = useParams();
  const navigate = useNavigate();
  const tid = Number(id);
  const { data: t, loading, error, reload } = useApiGet<SupportTicketDetail>(`/api/cmx/support/tickets/${tid}`);

  const [body, setBody] = useState("");
  const [status, setStatus] = useState("awaiting_customer");
  const [internal, setInternal] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  function openConfirm() {
    setErr(null);
    if (!body.trim()) {
      setErr("Write a message first.");
      return;
    }
    setConfirm(true);
  }

  async function submit() {
    await api.post(`/api/cmx/support/tickets/${tid}/reply`, {
      body: body.trim(),
      status: internal ? "" : status,
      internal,
    });
    setBody("");
    setInternal(false);
    reload();
  }

  if (loading) return <LoadingBlock />;
  if (error || !t) return <ErrorBlock message={error || "Ticket not found."} />;

  return (
    <div className="mx-auto max-w-[61.2rem] space-y-5">
      <button
        type="button"
        onClick={() => navigate("/support")}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Support
      </button>

      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-foreground">{t.subject}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {t.number} · {t.requester_name || t.requester_email} ·{" "}
            <button className="text-primary hover:underline" onClick={() => navigate(`/accounts/${t.tenant_id}`)}>
              {t.tenant_name || `tenant ${t.tenant_id}`}
            </button>
          </p>
        </div>
        <Badge tone={toneFor(t.status === "awaiting_customer" ? "pending" : t.status)}>
          {t.status.replace(/_/g, " ")}
        </Badge>
      </div>

      {/* Thread */}
      <div className="space-y-3">
        {t.messages.map((m) => {
          const mine = m.author_type === "agent";
          if (m.is_internal) {
            return (
              <div key={m.id} className="rounded-lg border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-sm">
                <div className="mb-0.5 flex items-center gap-1.5 text-xs font-medium text-amber-600">
                  <StickyNote className="h-3.5 w-3.5" /> Internal note · {m.author_name} · {formatDateTime(m.created_at)}
                </div>
                <div className="whitespace-pre-wrap text-foreground/80">{m.body}</div>
              </div>
            );
          }
          if (m.author_type === "system") {
            return (
              <div key={m.id} className="text-center text-xs text-muted-foreground">
                {m.body} · {formatDateTime(m.created_at)}
              </div>
            );
          }
          return (
            <div key={m.id} className={`flex ${mine ? "justify-end" : "justify-start"}`}>
              <div className={`max-w-[80%] rounded-lg px-3 py-2 text-sm ${mine ? "bg-primary/10" : "bg-muted"}`}>
                <div className="mb-0.5 text-xs text-muted-foreground">
                  {mine ? m.author_name || "Trilli Support" : m.author_name || "Customer"} · {formatDateTime(m.created_at)}
                </div>
                <div className="whitespace-pre-wrap text-foreground">{m.body}</div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Reply composer */}
      <Card>
        <CardContent className="space-y-3 p-4">
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={4}
            placeholder={internal ? "Internal note (operators only)…" : "Reply to the customer as Trilli Support…"}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
          />
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <input type="checkbox" checked={internal} onChange={(e) => setInternal(e.target.checked)} />
              Internal note (not emailed)
            </label>
            {!internal && (
              <div className="flex items-center gap-1.5">
                <Label className="text-xs">Set status</Label>
                <select
                  value={status}
                  onChange={(e) => setStatus(e.target.value)}
                  className="h-8 rounded-md border border-input bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
                >
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {s.replace(/_/g, " ")}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <div className="ml-auto">
              <Button onClick={openConfirm} className="h-8 gap-1.5">
                {internal ? <StickyNote className="h-4 w-4" /> : <Send className="h-4 w-4" />}
                {internal ? "Add note" : "Send reply"}
              </Button>
            </div>
          </div>
          {err && <p className="text-sm text-destructive">{err}</p>}
        </CardContent>
      </Card>

      {confirm && (
        <StepUpModal
          title={internal ? "Add internal note?" : "Send reply to customer?"}
          description={
            internal
              ? "Visible only to operators — the customer is not notified."
              : "The customer will receive this reply as Trilli Support and be emailed an update."
          }
          confirmLabel={internal ? "Add note" : "Send reply"}
          onConfirm={submit}
          onClose={() => setConfirm(false)}
        />
      )}
    </div>
  );
}
