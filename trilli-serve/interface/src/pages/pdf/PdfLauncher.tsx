// Trilli PDF home — imagery-forward card grid grouped by category, with a quick
// search and category filter chips. Each card frames a soft illustration in an
// inner bordered panel, with the title + description below.
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, X } from "lucide-react";

import { PDF_CATEGORIES, PDF_TOOLS } from "@/lib/pdf/tools";
import { PdfToolIcon } from "@/lib/pdf/icons";

export default function PdfLauncher() {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [activeCat, setActiveCat] = useState<string | null>(null);

  const q = query.trim().toLowerCase();
  const matches = useMemo(
    () => (t: (typeof PDF_TOOLS)[number]) =>
      (!activeCat || t.category === activeCat) &&
      (!q || t.name.toLowerCase().includes(q) || t.blurb.toLowerCase().includes(q)),
    [activeCat, q],
  );
  const byCat = (cat: string) => PDF_TOOLS.filter((t) => t.category === cat && matches(t));
  // All matching tools in category order, flowing through one grid so categories
  // pack densely (e.g. Convert/Edit/Optimize fill the row after Organize's
  // overflow) instead of each starting its own row. Category nav is the chips.
  const visible = PDF_CATEGORIES.flatMap((c) => byCat(c));

  const GRID = "grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6";

  const cards = (tools: typeof PDF_TOOLS) =>
    tools.map((t) => (
      <button
        key={t.key}
        type="button"
        onClick={() => navigate(`/apps/pdf/${t.key}`)}
        className="group flex flex-col rounded-xl border border-border bg-card p-2 text-left shadow-sm transition-colors duration-200 hover:border-foreground/15"
      >
        {/* inner bordered panel frames the flat tile icon */}
        <div className="flex aspect-[16/9] w-full items-center justify-center overflow-hidden rounded-lg border border-border/70 bg-muted/20 ring-1 ring-inset ring-white/30">
          <PdfToolIcon toolKey={t.key} size={84} />
        </div>
        <div className="px-1 pb-0.5 pt-2">
          <h3 className="font-pdf text-[19px] font-bold tracking-[-0.01em] text-foreground/80">{t.name}</h3>
          <p className="mt-0.5 line-clamp-2 text-[12px] leading-snug text-muted-foreground">{t.blurb}</p>
        </div>
      </button>
    ));

  return (
    <div className="mx-auto max-w-[88rem] px-6 py-8 lg:px-8 [@media(max-height:1100px)]:py-5">
      <header className="mb-5">
        <h1 className="font-pdf text-2xl font-extrabold tracking-[-0.01em] text-foreground">Trilli PDF</h1>
        <p className="mt-1 text-[13px] text-muted-foreground">
          Powerful PDF tools to organize, edit, optimize, and secure your documents — native to Trilli, working on
          files right in your space.
        </p>
      </header>

      {/* search + category filter chips */}
      <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="relative w-full sm:max-w-xs">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search tools…"
            className="w-full rounded-lg border border-border bg-background py-2 pl-9 pr-8 text-[13px] text-foreground outline-none focus:ring-2 focus:ring-primary/30"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery("")}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label="Clear search"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {(["All", ...PDF_CATEGORIES] as const).map((tag) => {
            const active = tag === "All" ? activeCat === null : activeCat === tag;
            return (
              <button
                key={tag}
                type="button"
                onClick={() => setActiveCat(tag === "All" ? null : tag)}
                className={
                  active
                    ? "rounded-full bg-primary px-3 py-1 text-[12px] font-medium text-primary-foreground"
                    : "rounded-full border border-border bg-card px-3 py-1 text-[12px] font-medium text-muted-foreground transition-colors hover:bg-muted"
                }
              >
                {tag}
              </button>
            );
          })}
        </div>
      </div>

      {visible.length === 0 ? (
        <p className="py-10 text-center text-[13px] text-muted-foreground">No tools match “{query}”.</p>
      ) : (
        <div className={GRID}>{cards(visible)}</div>
      )}
    </div>
  );
}
