import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Loader2,
  AlertTriangle,
  Send,
  CheckCircle2,
  RotateCcw,
  Clock,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { cn } from "@/lib/utils";
import {
  Ticket,
  TicketMessage,
  SEVERITY_META,
  STATUS_META,
  CATEGORY_LABELS,
  openDuration,
  messageTime,
} from "@/lib/support";

export default function SupportTicket() {
  const { ref } = useParams<{ ref: string }>();
  const { identity } = useAuth();
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const threadRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    try {
      const t = await api.get<Ticket>(`/api/support/tickets/${ref}`);
      setTicket(t);
    } catch (e) {
      setLoadError(
        e instanceof ApiError ? e.message : "Could not load this ticket.",
      );
    }
  }, [ref]);

  useEffect(() => {
    void load();
  }, [load]);

  // Keep the newest message in view as the thread grows.
  useEffect(() => {
    threadRef.current?.scrollTo({ top: threadRef.current.scrollHeight });
  }, [ticket?.messages?.length]);

  if (!identity) return null;

  if (loadError) {
    return (
      <div className="px-6 py-8">
        <div className="mx-auto max-w-3xl">
          <BackLink />
          <div className="mt-6 flex flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card/50 px-6 py-16 text-center">
            <AlertTriangle className="h-8 w-8 text-muted-foreground/50" />
            <h3 className="mt-3 text-lg font-semibold">Ticket not available</h3>
            <p className="mt-1 text-sm text-muted-foreground">{loadError}</p>
          </div>
        </div>
      </div>
    );
  }

  if (!ticket) {
    return (
      <div className="px-6 py-8">
        <div className="mx-auto max-w-3xl">
          <BackLink />
          <div className="mt-6 animate-pulse space-y-4">
            <div className="h-6 w-2/3 rounded bg-muted" />
            <div className="h-40 rounded-xl bg-muted" />
          </div>
        </div>
      </div>
    );
  }

  const sev = SEVERITY_META[ticket.severity];
  const st = STATUS_META[ticket.status];
  const closed = ticket.status === "closed";

  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-3xl space-y-5">
        <BackLink />

        <header className="rounded-xl border border-border bg-card p-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <span className="font-mono font-medium">{ticket.number}</span>
                <span aria-hidden>·</span>
                <span>{CATEGORY_LABELS[ticket.category]}</span>
              </div>
              <h1 className="mt-1 text-2xl font-semibold leading-tight">
                {ticket.subject}
              </h1>
            </div>
            <div className="flex flex-shrink-0 items-center gap-2">
              <span
                className={cn(
                  "rounded-full px-2.5 py-1 text-xs font-medium",
                  sev.pill,
                )}
              >
                {sev.label} priority
              </span>
              <span
                className={cn(
                  "rounded-full px-2.5 py-1 text-xs font-medium",
                  st.pill,
                )}
              >
                {st.label}
              </span>
            </div>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border pt-3 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <Clock className="h-3.5 w-3.5" />
              Opened {new Date(ticket.created_at).toLocaleDateString("en-US", {
                month: "short",
                day: "numeric",
                year: "numeric",
              })}
            </span>
            <span className="inline-flex items-center gap-1.5">
              {closed || ticket.status === "resolved"
                ? `Open for ${openDuration(ticket)} before ${closed ? "closing" : "resolving"}`
                : `Open for ${openDuration(ticket)}`}
            </span>
          </div>
        </header>

        <div
          ref={threadRef}
          className="max-h-[55vh] space-y-4 overflow-y-auto rounded-xl border border-border bg-card p-5"
        >
          {(ticket.messages ?? []).map((m) => (
            <MessageBubble key={m.id} msg={m} />
          ))}
        </div>

        <Composer ticket={ticket} closed={closed} onUpdated={setTicket} />
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <Link
      to="/support"
      className="inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground transition hover:text-foreground"
    >
      <ArrowLeft className="h-4 w-4" />
      All tickets
    </Link>
  );
}

function MessageBubble({ msg }: { msg: TicketMessage }) {
  if (msg.author_type === "system") {
    return (
      <div className="flex justify-center">
        <span className="rounded-full bg-muted px-3 py-1 text-[11px] font-medium text-muted-foreground">
          {msg.body} · {messageTime(msg.created_at)}
        </span>
      </div>
    );
  }
  const isCustomer = msg.author_type === "customer";
  return (
    <div className={cn("flex", isCustomer ? "justify-end" : "justify-start")}>
      <div className={cn("max-w-[82%]")}>
        <div
          className={cn(
            "mb-1 flex items-center gap-2 text-[11px] text-muted-foreground",
            isCustomer && "justify-end",
          )}
        >
          <span className="font-medium text-foreground/80">
            {isCustomer ? msg.author_name || "You" : msg.author_name || "Trilli Support"}
          </span>
          <span>{messageTime(msg.created_at)}</span>
        </div>
        <div
          className={cn(
            "whitespace-pre-wrap rounded-2xl px-4 py-2.5 text-sm leading-relaxed",
            isCustomer
              ? "rounded-br-md bg-primary text-primary-foreground"
              : "rounded-bl-md border border-border bg-background text-foreground",
          )}
        >
          {msg.body}
        </div>
      </div>
    </div>
  );
}

function Composer({
  ticket,
  closed,
  onUpdated,
}: {
  ticket: Ticket;
  closed: boolean;
  onUpdated: (t: Ticket) => void;
}) {
  const [body, setBody] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [statusBusy, setStatusBusy] = useState(false);

  const send = async () => {
    const text = body.trim();
    if (!text || busy) return;
    setBusy(true);
    setError(null);
    try {
      const t = await api.post<Ticket>(
        `/api/support/tickets/${ticket.number}/messages`,
        { body: text },
      );
      onUpdated(t);
      setBody("");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not send your reply.");
    } finally {
      setBusy(false);
    }
  };

  const changeStatus = async (status: "closed" | "open") => {
    setStatusBusy(true);
    setError(null);
    try {
      const t = await api.post<Ticket>(
        `/api/support/tickets/${ticket.number}/status`,
        { status },
      );
      onUpdated(t);
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : "Could not update the ticket.",
      );
    } finally {
      setStatusBusy(false);
    }
  };

  if (closed) {
    return (
      <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border bg-card/50 px-6 py-8 text-center">
        <CheckCircle2 className="h-7 w-7 text-muted-foreground/60" />
        <div>
          <p className="text-sm font-medium text-foreground">
            This ticket is closed
          </p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Still need help with this? Reopen it to continue the conversation.
          </p>
        </div>
        <button
          onClick={() => void changeStatus("open")}
          disabled={statusBusy}
          className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-background px-3.5 text-[13px] font-medium text-foreground transition hover:bg-muted disabled:opacity-60"
        >
          {statusBusy ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <RotateCcw className="h-3.5 w-3.5" />
          )}
          Reopen ticket
        </button>
      </div>
    );
  }

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
            e.preventDefault();
            void send();
          }
        }}
        rows={3}
        placeholder="Add a reply…"
        className="w-full resize-none rounded-md border border-border bg-background px-3 py-2 text-[13px] leading-relaxed outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
      />
      {error && (
        <p className="mt-2 flex items-center gap-1.5 text-[12px] text-destructive">
          <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
          {error}
        </p>
      )}
      <div className="mt-3 flex items-center justify-between">
        <button
          onClick={() => void changeStatus("closed")}
          disabled={statusBusy}
          className="text-[12.5px] font-medium text-muted-foreground transition hover:text-foreground disabled:opacity-60"
        >
          {statusBusy ? "Closing…" : "Close ticket"}
        </button>
        <button
          onClick={() => void send()}
          disabled={busy || body.trim().length === 0}
          className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground transition hover:bg-primary/90 disabled:opacity-50"
        >
          {busy ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Send className="h-3.5 w-3.5" />
          )}
          Send reply
        </button>
      </div>
    </div>
  );
}
