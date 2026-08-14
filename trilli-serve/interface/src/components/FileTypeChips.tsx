import { File as FileGeneric } from "lucide-react";

import { fileIconColorFor, fileIconFor } from "@/lib/files-meta";
import { cn } from "@/lib/utils";

// How many individual extension chips to show before everything else collapses
// into the single "Other" catch-all (keeps the row from overflowing when a
// workspace has lots of non-standard file types).
const TOP_N = 6;

// Sentinel value for the catch-all chip. The backend understands ?ext=__other__
// together with ?exclude=<top exts> to mean "everything not shown as a chip".
export const OTHER_EXT = "__other__";

// FileTypeChips renders the per-extension breakdown as small, clickable chips.
// Selecting a chip filters the file list below to that type; the catch-all
// "Other" chip filters to every type not shown individually. onSelect passes
// the current top extensions so the caller can build the exclude list.
export function FileTypeChips({
  byExtension,
  active,
  onSelect,
}: {
  byExtension: Record<string, number>;
  active: string | null;
  onSelect: (selection: string | null, topExts: string[]) => void;
}) {
  const entries = Object.entries(byExtension)
    .filter(([ext]) => ext !== "")
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const top = entries.slice(0, TOP_N);
  const topExts = top.map(([ext]) => ext);
  // Catch-all = the no-extension bucket plus every extension past the top N.
  const otherCount =
    (byExtension[""] ?? 0) + entries.slice(TOP_N).reduce((sum, [, n]) => sum + n, 0);

  if (top.length === 0 && otherCount === 0) return null;

  const toggle = (sel: string) => onSelect(active === sel ? null : sel, topExts);

  const chipClass = (isActive: boolean) =>
    cn(
      "inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium transition-colors",
      isActive
        ? "bg-primary text-primary-foreground"
        : "bg-secondary text-foreground hover:bg-secondary/70",
    );

  return (
    <div className="flex flex-1 flex-wrap items-center gap-1.5 sm:justify-end">
      {top.map(([ext, n]) => {
        const Icon = fileIconFor("", `x.${ext}`);
        const color = fileIconColorFor("", `x.${ext}`);
        const isActive = active === ext;
        return (
          <button
            key={ext}
            type="button"
            onClick={() => toggle(ext)}
            title={
              isActive
                ? `Showing ${ext} files — click to clear`
                : `${n} ${ext} file${n === 1 ? "" : "s"} — click to filter`
            }
            className={chipClass(isActive)}
          >
            <Icon
              className={cn("h-4 w-4 flex-shrink-0", isActive ? "text-primary-foreground" : color)}
            />
            <span className="tabular-nums">{n}</span>
            <span className={cn("uppercase", isActive ? "text-primary-foreground/80" : "text-muted-foreground")}>
              {ext}
            </span>
          </button>
        );
      })}
      {otherCount > 0 && (
        <button
          type="button"
          onClick={() => toggle(OTHER_EXT)}
          title={
            active === OTHER_EXT
              ? "Showing other file types — click to clear"
              : `${otherCount} other file${otherCount === 1 ? "" : "s"} — click to filter`
          }
          className={chipClass(active === OTHER_EXT)}
        >
          <FileGeneric
            className={cn(
              "h-4 w-4 flex-shrink-0",
              active === OTHER_EXT ? "text-primary-foreground" : "text-muted-foreground",
            )}
          />
          <span className="tabular-nums">{otherCount}</span>
          <span
            className={cn(
              "uppercase",
              active === OTHER_EXT ? "text-primary-foreground/80" : "text-muted-foreground",
            )}
          >
            Other
          </span>
        </button>
      )}
    </div>
  );
}
