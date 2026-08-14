import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Loader2, Minus, Plus, UserPlus, X } from "lucide-react";
import { Link } from "react-router-dom";

import { api, ApiError, type SeatPreviewResult, type SeatSummary } from "@/lib/api";

function money(cents: number, currency = "usd"): string {
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

// AddSeatsModal lets an admin buy extra seats. A stepper picks the quantity, a
// live prorated preview shows the amount due today + what renews, and Confirm
// commits the purchase. If the account has no live subscription yet, it nudges
// them to start a plan instead.
export default function AddSeatsModal({
  seats,
  onClose,
  onDone,
}: {
  seats: SeatSummary;
  onClose: () => void;
  onDone: () => void;
}) {
  const [qty, setQty] = useState(1);
  const [preview, setPreview] = useState<SeatPreviewResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadPreview = useCallback(async (quantity: number) => {
    setLoading(true);
    setError(null);
    try {
      const r = await api.post<SeatPreviewResult>("/api/billing/preview-seats", {
        quantity,
      });
      setPreview(r);
    } catch (e) {
      setError(e instanceof ApiError && e.message ? e.message : "Couldn't price the seats.");
      setPreview(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadPreview(qty);
  }, [qty, loadPreview]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, busy]);

  const needsCheckout = preview?.needs_checkout === true;
  const periodLabel = preview?.billing_period === "monthly" ? "mo" : "yr";

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.post<{ needs_checkout?: boolean }>("/api/billing/add-seats", {
        quantity: qty,
      });
      if (r.needs_checkout) {
        setBusy(false);
        void loadPreview(qty); // surface the "start a plan" state
        return;
      }
      onDone();
    } catch (e) {
      setError(e instanceof ApiError && e.message ? e.message : "Couldn't add seats. Please try again.");
      setBusy(false);
    }
  };

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div className="w-full max-w-md overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200 ease-out">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <UserPlus className="h-4 w-4 text-primary" />
            Add seats
          </h2>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="text-muted-foreground transition-colors hover:text-foreground disabled:opacity-40"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 px-5 py-5">
          {needsCheckout ? (
            <div className="rounded-xl border border-border bg-muted/30 p-4 text-center text-[13px] text-muted-foreground">
              <p>
                Start a paid plan to add seats. Once you have an active subscription, extra seats
                are billed per month and prorated instantly.
              </p>
              <Link
                to="/settings/plan"
                className="mt-3 inline-block text-[13px] font-semibold text-primary hover:underline"
              >
                Choose a plan →
              </Link>
            </div>
          ) : (
            <>
              {/* quantity stepper */}
              <div className="flex items-center justify-between">
                <span className="text-[13px] font-medium text-foreground">How many seats?</span>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setQty((q) => Math.max(1, q - 1))}
                    disabled={qty <= 1 || busy}
                    className="flex h-8 w-8 items-center justify-center rounded-md border border-border text-foreground transition-colors hover:bg-muted disabled:opacity-40"
                    aria-label="Fewer seats"
                  >
                    <Minus className="h-4 w-4" />
                  </button>
                  <span className="w-8 text-center text-[15px] font-semibold tabular-nums text-foreground">
                    {qty}
                  </span>
                  <button
                    type="button"
                    onClick={() => setQty((q) => Math.min(99, q + 1))}
                    disabled={qty >= 99 || busy}
                    className="flex h-8 w-8 items-center justify-center rounded-md border border-border text-foreground transition-colors hover:bg-muted disabled:opacity-40"
                    aria-label="More seats"
                  >
                    <Plus className="h-4 w-4" />
                  </button>
                </div>
              </div>

              {/* prorated cost */}
              <div className="space-y-1 rounded-lg bg-muted/40 px-3.5 py-3 text-[12.5px]">
                <div className="flex items-baseline justify-between">
                  <span className="text-muted-foreground">Due today, prorated</span>
                  <span className="font-semibold tabular-nums text-foreground">
                    {loading ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
                    ) : (
                      money(preview?.due_today_cents ?? 0, preview?.currency)
                    )}
                  </span>
                </div>
                <div className="flex items-baseline justify-between text-muted-foreground">
                  <span>Adds to renewal</span>
                  <span className="tabular-nums">
                    +{money(preview?.next_renewal_cents ?? 0, preview?.currency)}/{periodLabel}
                    {fmtDate(preview?.next_renewal_at) ? ` · ${fmtDate(preview?.next_renewal_at)}` : ""}
                  </span>
                </div>
              </div>
              <p className="text-center text-[11.5px] text-muted-foreground">
                You'll have {seats.used} of {(seats.total ?? 0) + qty} seats after this.
              </p>
            </>
          )}

          {error && <p className="text-center text-[12.5px] text-destructive">{error}</p>}
        </div>

        <div className="flex items-center justify-center gap-2.5 border-t border-border px-5 py-4">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="inline-flex h-9 min-w-[7rem] items-center justify-center rounded-md border border-border bg-background px-4 text-[13px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          {!needsCheckout && (
            <button
              type="button"
              onClick={confirm}
              disabled={busy || loading}
              className="inline-flex h-9 min-w-[7rem] items-center justify-center gap-1.5 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
            >
              {busy ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" /> Processing…
                </>
              ) : (
                `Add ${qty} seat${qty === 1 ? "" : "s"}`
              )}
            </button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
