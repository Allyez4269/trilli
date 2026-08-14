import { cn } from "@/lib/utils";

export type Tone = "neutral" | "green" | "amber" | "red" | "blue" | "slate";

const TONES: Record<Tone, string> = {
  neutral: "bg-muted text-muted-foreground ring-foreground/[0.06]",
  green: "bg-emerald-500/10 text-emerald-700 ring-emerald-600/15",
  amber: "bg-amber-500/15 text-amber-700 ring-amber-600/20",
  red: "bg-destructive/10 text-destructive ring-destructive/15",
  blue: "bg-sky-500/10 text-sky-700 ring-sky-600/15",
  slate: "bg-primary/10 text-primary ring-primary/15",
};

export function Badge({
  children,
  tone = "neutral",
  className,
}: {
  children: React.ReactNode;
  tone?: Tone;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium capitalize ring-1 ring-inset",
        TONES[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

// toneFor maps common status/lifecycle strings to a badge tone.
export function toneFor(value: string): Tone {
  const v = value.toLowerCase();
  if (["active", "paying", "current", "ok", "succeeded"].includes(v)) return "green";
  if (["trial", "lead", "pending", "scheduled", "lapsed", "grace", "closing"].includes(v)) return "amber";
  if (["suspended", "locked", "churned", "past_due", "canceled", "cancelled", "failed", "expired"].includes(v))
    return "red";
  return "neutral";
}
