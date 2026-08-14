import { useEffect } from "react";
import { createPortal } from "react-dom";
import {
  Check,
  Feather,
  Infinity as InfinityIcon,
  Loader2,
  Plus,
  Sparkles,
  X,
  type LucideIcon,
} from "lucide-react";

import type { CatalogPlan, ChangePreviewResult } from "@/lib/api";

const PLAN_ICONS: Record<string, LucideIcon> = {
  feather: Feather,
  plus: Plus,
  infinity: InfinityIcon,
};

function fmtMoney(cents: number, currency = "usd"): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: (currency || "usd").toUpperCase(),
  }).format((cents ?? 0) / 100);
}

function fmtDate(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" });
}

// PlanChangeConfirmModal sells the upgrade first — it leads with the plan, its
// tagline and everything you unlock — then states the prorated cost quietly as
// a small summary line before a final confirm. (For scheduled downgrades it
// explains there's no charge today and when the switch takes effect.)
export default function PlanChangeConfirmModal({
  plan,
  preview,
  busy,
  error,
  onConfirm,
  onClose,
}: {
  plan: CatalogPlan;
  preview: ChangePreviewResult;
  busy: boolean;
  error: string | null;
  onConfirm: () => void;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, busy]);

  const isUpgrade = preview.effect !== "scheduled";
  const Icon = PLAN_ICONS[plan.icon] ?? Sparkles;
  const periodLabel = preview.billing_period === "monthly" ? "mo" : "yr";
  const renewal = preview.next_renewal_cents ?? 0;
  const renewalDate = fmtDate(preview.next_renewal_at);
  const features = plan.features ?? [];

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div className="w-full max-w-md overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200 ease-out">
        {/* Hero — sells the destination plan */}
        <div className="relative overflow-hidden bg-gradient-to-br from-primary to-[#4f46e5] px-6 pb-5 pt-6 text-primary-foreground">
          {/* soft decorative glow */}
          <div className="pointer-events-none absolute -right-10 -top-12 h-36 w-36 rounded-full bg-white/10 blur-2xl" />
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="absolute right-3.5 top-3.5 text-primary-foreground/70 transition-colors hover:text-primary-foreground disabled:opacity-40"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
          <div className="relative flex items-center gap-3">
            <span className="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-2xl bg-white/15 ring-1 ring-white/25 backdrop-blur">
              <Icon className="h-6 w-6" strokeWidth={2.25} />
            </span>
            <div className="min-w-0">
              <p className="text-[11px] font-medium uppercase tracking-wide text-primary-foreground/75">
                {isUpgrade ? "Upgrade to" : "Switch to"}
              </p>
              <h2 className="text-xl font-bold leading-tight">{plan.name}</h2>
            </div>
          </div>
          {plan.tagline && (
            <p className="relative mt-3 text-[13px] leading-snug text-primary-foreground/90">
              {plan.tagline}
            </p>
          )}
        </div>

        {/* Benefits */}
        <div className="px-6 py-5">
          <p className="flex items-center gap-1.5 text-[11.5px] font-semibold uppercase tracking-wide text-muted-foreground">
            <Sparkles className="h-3.5 w-3.5 text-primary" />
            {isUpgrade ? "What you'll unlock" : "What's included"}
          </p>
          {features.length > 0 && (
            <ul className="mt-3 max-h-[208px] space-y-2 overflow-y-auto pr-1">
              {features.map((f) => (
                <li key={f} className="flex items-start gap-2.5 text-[13px] text-foreground">
                  <span className="mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full bg-emerald-500/15">
                    <Check className="h-3 w-3 text-emerald-600" strokeWidth={3} />
                  </span>
                  <span className="leading-snug">{f}</span>
                </li>
              ))}
            </ul>
          )}

          {/* Quiet cost summary — present, but not in your face */}
          <div className="mt-4 space-y-1 rounded-lg bg-muted/40 px-3.5 py-2.5 text-[12.5px]">
            {isUpgrade ? (
              <>
                <div className="flex items-baseline justify-between">
                  <span className="text-muted-foreground">Due today, prorated</span>
                  <span className="font-semibold tabular-nums text-foreground">
                    {fmtMoney(preview.due_today_cents ?? 0, preview.currency)}
                  </span>
                </div>
                {renewal > 0 && (
                  <div className="flex items-baseline justify-between text-muted-foreground">
                    <span>Renews</span>
                    <span className="tabular-nums">
                      {fmtMoney(renewal, preview.currency)}/{periodLabel}
                      {renewalDate ? ` · ${renewalDate}` : ""}
                    </span>
                  </div>
                )}
              </>
            ) : (
              <p className="text-muted-foreground">
                <span className="font-medium text-foreground">No charge today.</span> Your plan
                switches to {plan.name}
                {fmtDate(preview.effective_at) ? ` on ${fmtDate(preview.effective_at)}` : ""} — you
                keep your current plan until then.
              </p>
            )}
          </div>

          {error && <p className="mt-3 text-center text-[12.5px] text-destructive">{error}</p>}
        </div>

        {/* Centered actions */}
        <div className="flex items-center justify-center gap-2.5 border-t border-border px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="inline-flex h-9 min-w-[7rem] items-center justify-center rounded-md border border-border bg-background px-4 text-[13px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className="inline-flex h-9 min-w-[7rem] items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            {busy ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" /> Processing…
              </>
            ) : isUpgrade ? (
              "Upgrade"
            ) : (
              "Confirm"
            )}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
