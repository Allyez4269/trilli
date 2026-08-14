import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";

// usePager paginates a list client-side. Returns the current page's rows plus
// the controls a <Pager> needs. Default page size is 7 — tables that exceed it
// get pagination throughout CMX.
export function usePager<T>(items: T[], pageSize = 7) {
  const [page, setPage] = useState(1);
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize));
  const safe = Math.min(page, pageCount);
  return {
    page: safe,
    setPage,
    pageCount,
    total: items.length,
    pageSize,
    rows: items.slice((safe - 1) * pageSize, safe * pageSize),
  };
}

// Pager renders Prev / "Page X of Y" / Next, anchored as a table footer. It
// renders nothing when everything fits on one page.
export function Pager({
  page,
  pageCount,
  total,
  pageSize,
  onPage,
}: {
  page: number;
  pageCount: number;
  total: number;
  pageSize: number;
  onPage: (p: number) => void;
}) {
  if (total <= pageSize) return null;
  const btn =
    "inline-flex h-7 items-center gap-1 rounded-md border border-input bg-card px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50";
  return (
    <div className="flex items-center justify-between border-t border-border/60 px-4 py-2.5">
      <span className="text-xs text-muted-foreground">
        Page {page} of {pageCount}
      </span>
      <div className="flex items-center gap-1.5">
        <button type="button" onClick={() => onPage(page - 1)} disabled={page <= 1} className={btn}>
          <ChevronLeft className="h-3.5 w-3.5" /> Prev
        </button>
        <button type="button" onClick={() => onPage(page + 1)} disabled={page >= pageCount} className={btn}>
          Next <ChevronRight className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}
