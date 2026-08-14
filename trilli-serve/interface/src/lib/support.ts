// Shared types + presentation metadata for the support ticketing UI, kept in
// one place so the list and detail views stay in sync with the backend
// (system/supporttickets).

export type TicketSeverity = "low" | "normal" | "high" | "urgent";
export type TicketStatus =
  | "open"
  | "pending"
  | "awaiting_customer"
  | "resolved"
  | "closed";
export type TicketCategory =
  | "billing"
  | "technical"
  | "account"
  | "sharing"
  | "feature"
  | "other";

export interface TicketMessage {
  id: number;
  ticket_id: number;
  author_type: "customer" | "agent" | "system";
  author_name: string;
  body: string;
  created_at: string;
}

export interface Ticket {
  id: number;
  number: string;
  created_by_user_id: number;
  requester_email: string;
  requester_name: string;
  subject: string;
  category: TicketCategory;
  severity: TicketSeverity;
  status: TicketStatus;
  last_activity_at: string;
  created_at: string;
  resolved_at?: string;
  closed_at?: string;
  message_count: number;
  messages?: TicketMessage[];
}

// Severity → label + pill classes (matching the inline-badge convention used
// elsewhere, e.g. Shared.tsx).
export const SEVERITY_META: Record<
  TicketSeverity,
  { label: string; pill: string }
> = {
  low: { label: "Low", pill: "bg-muted text-muted-foreground" },
  normal: { label: "Normal", pill: "bg-primary/10 text-primary" },
  high: { label: "High", pill: "bg-amber-500/15 text-amber-700" },
  urgent: { label: "Urgent", pill: "bg-red-500/15 text-red-700" },
};

export const STATUS_META: Record<
  TicketStatus,
  { label: string; pill: string; dot: string }
> = {
  open: {
    label: "Open",
    pill: "bg-primary/10 text-primary",
    dot: "bg-primary",
  },
  pending: {
    label: "In progress",
    pill: "bg-violet-500/15 text-violet-700",
    dot: "bg-violet-500",
  },
  awaiting_customer: {
    label: "Awaiting your reply",
    pill: "bg-amber-500/15 text-amber-700",
    dot: "bg-amber-500",
  },
  resolved: {
    label: "Resolved",
    pill: "bg-emerald-500/15 text-emerald-700",
    dot: "bg-emerald-500",
  },
  closed: {
    label: "Closed",
    pill: "bg-muted text-muted-foreground",
    dot: "bg-muted-foreground",
  },
};

export const CATEGORY_LABELS: Record<TicketCategory, string> = {
  billing: "Billing & payments",
  technical: "Technical issue",
  account: "Account & login",
  sharing: "Sharing & links",
  feature: "Feature request",
  other: "Something else",
};

// How long a ticket has been open, phrased for the customer. Counts up to the
// resolution/close time when set, otherwise to now.
export function openDuration(t: Ticket): string {
  const start = new Date(t.created_at).getTime();
  const end = t.closed_at
    ? new Date(t.closed_at).getTime()
    : t.resolved_at
      ? new Date(t.resolved_at).getTime()
      : Date.now();
  const mins = Math.max(0, Math.floor((end - start) / 60000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min${mins === 1 ? "" : "s"}`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs} hour${hrs === 1 ? "" : "s"}`;
  const days = Math.floor(hrs / 24);
  return `${days} day${days === 1 ? "" : "s"}`;
}

// A precise timestamp for a message ("Jun 11, 2026, 1:32 PM").
export function messageTime(iso: string): string {
  const d = new Date(iso);
  return (
    d.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
    }) +
    ", " +
    d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" })
  );
}
