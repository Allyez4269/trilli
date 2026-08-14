import { UserPlus, Users } from "lucide-react";

import type { SeatSummary } from "@/lib/api";
import { cn } from "@/lib/utils";

// SeatMeter is the persistent seat dashboard at the top of the Users area: a
// usage bar that quietly reinforces the seat allowance and turns into an
// upsell as the team fills its seats. Management lives below it, untouched.
export default function SeatMeter({
  seats,
  canManage,
  onAddSeats,
}: {
  seats: SeatSummary;
  canManage: boolean;
  onAddSeats: () => void;
}) {
  // Unlimited plans: nothing to meter or sell.
  if (seats.total == null) {
    return (
      <div className="flex items-center gap-2.5 rounded-xl border border-border bg-card px-4 py-3 text-[13px] text-muted-foreground">
        <Users className="h-4 w-4 text-primary" />
        <span>
          <span className="font-medium text-foreground">{seats.used}</span> user
          {seats.used === 1 ? "" : "s"} · unlimited seats on {seats.plan_name}
        </span>
      </div>
    );
  }

  const total = seats.total;
  const used = Math.min(seats.used, total);
  const pct = total > 0 ? Math.min(100, Math.round((seats.used / total) * 100)) : 0;
  const remaining = Math.max(0, total - seats.used);
  const full = seats.used >= total;
  const low = !full && remaining <= 1; // 1 seat or fewer left

  // Only the progress indicator changes color to signal state — the container
  // box and the Add seats button stay constant (no size/tone change).
  const barColor = full ? "bg-destructive" : low ? "bg-amber-500" : "bg-primary";

  return (
    <div className="rounded-xl border border-border bg-card px-4 py-3.5 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Users className="h-4 w-4 text-primary" />
          <span className="text-[13px] font-medium text-foreground">
            {seats.used} of {total} seats used
          </span>
          {seats.extra > 0 && (
            <span className="text-[11.5px] text-muted-foreground">
              · {seats.included} included + {seats.extra} purchased
            </span>
          )}
        </div>

        <div className="flex items-center gap-3">
          {full ? (
            <span className="text-[12.5px] font-medium text-destructive">All seats are full</span>
          ) : low ? (
            <span className="text-[12.5px] font-medium text-amber-600">
              {remaining} seat{remaining === 1 ? "" : "s"} left
            </span>
          ) : (
            <span className="text-[12.5px] text-muted-foreground">
              {remaining} seat{remaining === 1 ? "" : "s"} left
            </span>
          )}
          {seats.sellable && canManage && (
            <button
              type="button"
              onClick={onAddSeats}
              className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-background px-2.5 text-[12px] font-semibold text-foreground transition-colors hover:bg-muted"
            >
              <UserPlus className="h-3.5 w-3.5" />
              Add seats
            </button>
          )}
        </div>
      </div>

      {/* progress track — the indicator color is the only state signal */}
      <div className="mt-2.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full transition-all", barColor)}
          style={{ width: `${Math.max(pct, used > 0 ? 6 : 0)}%` }}
        />
      </div>
    </div>
  );
}
