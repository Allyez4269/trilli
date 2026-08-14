// Shared table paginator — the standard Trilli pagination control (range
// summary + numbered pages with ellipsis + prev/next). Used by Files and the
// Trilli Sign envelope table so pagination looks identical everywhere.
import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export function Paginator({
  currentPage,
  totalPages,
  rangeStart,
  rangeEnd,
  total,
  onJump,
  // Top divider — on inside a bordered card (Files), off when standalone.
  bordered = true,
}: {
  currentPage: number;
  totalPages: number;
  rangeStart: number;
  rangeEnd: number;
  total: number;
  onJump: (p: number) => void;
  bordered?: boolean;
}) {
  const pages: (number | "…")[] = [];
  if (totalPages <= 7) {
    for (let p = 1; p <= totalPages; p++) pages.push(p);
  } else {
    const wanted = new Set<number>([1, totalPages]);
    for (let p = currentPage - 1; p <= currentPage + 1; p++) {
      if (p >= 1 && p <= totalPages) wanted.add(p);
    }
    const sorted = [...wanted].sort((a, b) => a - b);
    sorted.forEach((p, i) => {
      if (i > 0 && p - sorted[i - 1] > 1) pages.push("…");
      pages.push(p);
    });
  }

  return (
    <div className={cn("flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 text-xs text-muted-foreground", bordered && "border-t border-border")}>
      <span className="tabular-nums">
        <span className="font-medium text-foreground">{currentPage}</span> of {totalPages}
      </span>
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={currentPage === 1}
          onClick={() => onJump(currentPage - 1)}
          title="Previous page"
        >
          <ChevronLeft className="h-4 w-4" />
        </Button>
        {pages.map((p, i) =>
          p === "…" ? (
            <span
              key={`gap-${i}`}
              className="select-none px-1 text-muted-foreground/70"
              aria-hidden="true"
            >
              …
            </span>
          ) : (
            <button
              key={p}
              type="button"
              onClick={() => onJump(p)}
              className={cn(
                "min-w-[28px] rounded px-2 py-1 text-xs tabular-nums transition-colors",
                p === currentPage
                  ? "bg-primary font-medium text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
              aria-current={p === currentPage ? "page" : undefined}
            >
              {p}
            </button>
          ),
        )}
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={currentPage >= totalPages}
          onClick={() => onJump(currentPage + 1)}
          title="Next page"
        >
          <ChevronRight className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}

