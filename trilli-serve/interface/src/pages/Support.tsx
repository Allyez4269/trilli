import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  LifeBuoy,
  Plus,
  Loader2,
  AlertTriangle,
  MessageSquare,
  ChevronRight,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { cn } from "@/lib/utils";
import { formatRelative } from "@/lib/files-meta";
import {
  Ticket,
  TicketCategory,
  TicketSeverity,
  SEVERITY_META,
  STATUS_META,
  CATEGORY_LABELS,
} from "@/lib/support";
import { Overlay } from "@/components/Overlay";

export default function Support() {
  const { identity } = useAuth();
  const navigate = useNavigate();
  const [tickets, setTickets] = useState<Ticket[] | null>(null);
  const [scope, setScope] = useState<"mine" | "tenant">("mine");
  const [newOpen, setNewOpen] = useState(false);

  const load = useCallback(async () => {
    const res = await api.get<{ tickets: Ticket[]; scope: "mine" | "tenant" }>(
      "/api/support/tickets",
    );
    setTickets(res.tickets ?? []);
    setScope(res.scope ?? "mine");
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!identity) return null;

  const open = (tickets ?? []).filter(
    (t) => t.status !== "closed" && t.status !== "resolved",
  );
  const done = (tickets ?? []).filter(
    (t) => t.status === "closed" || t.status === "resolved",
  );

  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-4xl space-y-6">
        <header className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h1 className="flex items-center gap-2 text-3xl font-semibold">
              <LifeBuoy className="h-7 w-7 text-primary" />
              Support
            </h1>
            <p className="mt-1 text-muted-foreground">
              Need a hand? Open a ticket and follow the whole conversation right
              here — we'll email you whenever there's an update.
              {scope === "tenant" && (
                <span className="ml-1 text-muted-foreground/80">
                  You're seeing every ticket in this workspace.
                </span>
              )}
            </p>
          </div>
          <button
            onClick={() => setNewOpen(true)}
            className="inline-flex h-10 flex-shrink-0 items-center gap-1.5 rounded-lg bg-primary px-4 text-sm font-semibold text-primary-foreground shadow-sm transition hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" />
            New ticket
          </button>
        </header>

        {tickets === null ? (
          <ListSkeleton />
        ) : tickets.length === 0 ? (
          <EmptyState onNew={() => setNewOpen(true)} />
        ) : (
          <div className="space-y-6">
            {open.length > 0 && (
              <Section title="Active" tickets={open} />
            )}
            {done.length > 0 && (
              <Section title="Resolved & closed" tickets={done} muted />
            )}
          </div>
        )}
      </div>

      {newOpen && (
        <NewTicketModal
          onClose={() => setNewOpen(false)}
          onCreated={(t) => {
            setNewOpen(false);
            navigate(`/support/${t.number}`);
          }}
        />
      )}
    </div>
  );
}

function Section({
  title,
  tickets,
  muted = false,
}: {
  title: string;
  tickets: Ticket[];
  muted?: boolean;
}) {
  return (
    <section className="space-y-2">
      <h2 className="px-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </h2>
      <div
        className={cn(
          "overflow-hidden rounded-xl border border-border bg-card",
          muted && "opacity-90",
        )}
      >
        {tickets.map((t, i) => (
          <TicketRow key={t.id} ticket={t} first={i === 0} />
        ))}
      </div>
    </section>
  );
}

function TicketRow({ ticket, first }: { ticket: Ticket; first: boolean }) {
  const sev = SEVERITY_META[ticket.severity];
  const st = STATUS_META[ticket.status];
  return (
    <Link
      to={`/support/${ticket.number}`}
      className={cn(
        "flex items-center gap-3 px-4 py-3.5 transition hover:bg-muted/50",
        !first && "border-t border-border",
      )}
    >
      <span className={cn("h-2 w-2 flex-shrink-0 rounded-full", st.dot)} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-foreground">
            {ticket.subject}
          </span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          <span className="font-mono">{ticket.number}</span>
          <span aria-hidden>·</span>
          <span>Opened {formatRelative(ticket.created_at)}</span>
          {ticket.message_count > 1 && (
            <>
              <span aria-hidden>·</span>
              <span className="inline-flex items-center gap-1">
                <MessageSquare className="h-3 w-3" />
                {ticket.message_count}
              </span>
            </>
          )}
        </div>
      </div>
      <span
        className={cn(
          "hidden flex-shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium sm:inline-block",
          sev.pill,
        )}
      >
        {sev.label}
      </span>
      <span
        className={cn(
          "flex-shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
          st.pill,
        )}
      >
        {st.label}
      </span>
      <ChevronRight className="h-4 w-4 flex-shrink-0 text-muted-foreground/50" />
    </Link>
  );
}

function EmptyState({ onNew }: { onNew: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card/50 px-6 py-16 text-center">
      <span className="flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary">
        <LifeBuoy className="h-6 w-6" />
      </span>
      <h3 className="mt-4 text-lg font-semibold text-foreground">
        No tickets yet
      </h3>
      <p className="mt-1 max-w-sm text-sm text-muted-foreground">
        Run into a problem or have a question? Open a ticket and track our
        responses right here — we'll email you whenever there's an update.
      </p>
      <button
        onClick={onNew}
        className="mt-5 inline-flex h-10 items-center gap-1.5 rounded-lg bg-primary px-4 text-sm font-semibold text-primary-foreground shadow-sm transition hover:bg-primary/90"
      >
        <Plus className="h-4 w-4" />
        Open your first ticket
      </button>
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      {[0, 1, 2].map((i) => (
        <div
          key={i}
          className={cn(
            "flex items-center gap-3 px-4 py-4",
            i > 0 && "border-t border-border",
          )}
        >
          <div className="h-2 w-2 rounded-full bg-muted" />
          <div className="flex-1 space-y-2">
            <div className="h-3.5 w-1/2 rounded bg-muted" />
            <div className="h-2.5 w-1/3 rounded bg-muted" />
          </div>
          <div className="h-5 w-16 rounded-full bg-muted" />
        </div>
      ))}
    </div>
  );
}

const CATEGORIES: TicketCategory[] = [
  "technical",
  "billing",
  "account",
  "sharing",
  "feature",
  "other",
];
const SEVERITIES: TicketSeverity[] = ["low", "normal", "high", "urgent"];

const SEVERITY_HINT: Record<TicketSeverity, string> = {
  low: "Minor — a question or small annoyance.",
  normal: "Standard — something's not right but you can work around it.",
  high: "Important — a feature is broken or blocking your work.",
  urgent: "Critical — you can't access your data or account.",
};

function NewTicketModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (t: Ticket) => void;
}) {
  const [subject, setSubject] = useState("");
  const [category, setCategory] = useState<TicketCategory>("technical");
  const [severity, setSeverity] = useState<TicketSeverity>("normal");
  const [body, setBody] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const subjectRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const t = setTimeout(() => subjectRef.current?.focus(), 40);
    return () => clearTimeout(t);
  }, []);

  const valid = subject.trim().length > 0 && body.trim().length > 0;

  const submit = async () => {
    if (busy || !valid) return;
    setBusy(true);
    setError(null);
    try {
      const t = await api.post<Ticket>("/api/support/tickets", {
        subject: subject.trim(),
        body: body.trim(),
        category,
        severity,
      });
      onCreated(t);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not create the ticket.");
      setBusy(false);
    }
  };

  const field =
    "w-full rounded-md border border-border bg-background px-3 text-[13px] outline-none focus-visible:ring-1 focus-visible:ring-primary/40";

  return (
    <Overlay onClose={() => !busy && onClose()} className="max-w-lg">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div className="border-b border-border px-5 py-4">
          <h2 className="flex items-center gap-2 text-base font-semibold text-foreground">
            <LifeBuoy className="h-4.5 w-4.5 text-primary" />
            Open a support ticket
          </h2>
          <p className="mt-0.5 text-[12.5px] text-muted-foreground">
            Tell us what's going on. We'll reply right here in your ticket and
            email you a notification whenever there's an update.
          </p>
        </div>

        <div className="space-y-4 px-5 py-4">
          <div className="space-y-1.5">
            <label className="block text-[12px] font-medium text-foreground">
              Subject
            </label>
            <input
              ref={subjectRef}
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              placeholder="Briefly, what do you need help with?"
              maxLength={200}
              className={cn(field, "h-9")}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="block text-[12px] font-medium text-foreground">
                Category
              </label>
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value as TicketCategory)}
                className={cn(field, "h-9")}
              >
                {CATEGORIES.map((c) => (
                  <option key={c} value={c}>
                    {CATEGORY_LABELS[c]}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="block text-[12px] font-medium text-foreground">
                Priority
              </label>
              <select
                value={severity}
                onChange={(e) => setSeverity(e.target.value as TicketSeverity)}
                className={cn(field, "h-9")}
              >
                {SEVERITIES.map((s) => (
                  <option key={s} value={s}>
                    {SEVERITY_META[s].label}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <p className="-mt-2 text-[11.5px] text-muted-foreground">
            {SEVERITY_HINT[severity]}
          </p>

          <div className="space-y-1.5">
            <label className="block text-[12px] font-medium text-foreground">
              Description
            </label>
            <textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={5}
              placeholder="Describe the problem in as much detail as you can — what you expected, what happened, and any steps to reproduce it."
              className={cn(field, "resize-none py-2 leading-relaxed")}
            />
          </div>

          {error && (
            <p className="flex items-center gap-1.5 text-[12px] text-destructive">
              <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
              {error}
            </p>
          )}
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground hover:bg-muted disabled:opacity-60"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy || !valid}
            className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Submit ticket
          </button>
        </div>
      </form>
    </Overlay>
  );
}
