// A reusable, app-wide transfer-progress dialog: title, a percentage bar, the
// item currently moving, and an optional Cancel. Use it for any long-running
// transfer (cloud import, bulk move, etc.) so progress looks consistent.
//
// `indeterminate` adds a continuously sliding highlight on top of the (per-item)
// fill — for transfers whose exact byte progress we can't measure, so the user
// always sees motion even while a single large item is in flight.
import { Loader2, X } from "lucide-react";

export function TransferProgressModal({
  title,
  done,
  total,
  currentLabel,
  onCancel,
  indeterminate = false,
}: {
  title: string;
  done: number;
  total: number;
  currentLabel?: string;
  onCancel?: () => void;
  indeterminate?: boolean;
}) {
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
  const subtitle = indeterminate ? `Transferring ${Math.min(done + 1, total)} of ${total}…` : `${done} of ${total} · ${pct}%`;
  return (
    <div className="fixed inset-0 z-[200] flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-foreground/50 backdrop-blur-[2px]" />
      <div className="relative w-full max-w-sm rounded-2xl border border-border bg-card p-6 shadow-2xl">
        <div className="mb-4 flex items-center gap-3">
          <span className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
            <Loader2 className="h-5 w-5 animate-spin" />
          </span>
          <div className="min-w-0">
            <p className="truncate text-[14px] font-semibold text-foreground">{title}</p>
            <p className="text-[12px] text-muted-foreground">{subtitle}</p>
          </div>
        </div>

        <div className="relative h-2 w-full overflow-hidden rounded-full bg-secondary">
          {/* fill for completed items */}
          <div className="absolute inset-y-0 left-0 rounded-full bg-primary transition-all duration-300 ease-out" style={{ width: `${pct}%` }} />
          {/* sliding highlight while an item is in flight */}
          {indeterminate && <div className="progress-indeterminate bg-primary/60" />}
        </div>

        {currentLabel && <p className="mt-3 truncate text-[12px] text-muted-foreground">{currentLabel}</p>}

        {onCancel && (
          <div className="mt-5 flex justify-end">
            <button
              type="button"
              onClick={onCancel}
              className="inline-flex items-center gap-1.5 rounded-lg border border-border px-3.5 py-1.5 text-[13px] font-medium text-foreground transition-colors hover:bg-muted"
            >
              <X className="h-3.5 w-3.5" /> Cancel
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
