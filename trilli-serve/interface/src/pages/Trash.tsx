import { useEffect, useMemo, useState } from "react";
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  ChevronUp,
  Clock,
  FileText,
  Folder,
  Loader2,
  RotateCcw,
  Search,
  Trash2,
  X,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { api, ApiError } from "@/lib/api";
import { emitTrashChanged } from "@/lib/events";
import { PageHeader } from "@/components/PageHeader";
import { ConfirmDialog } from "@/components/Dialogs";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/Checkbox";

// ===========================================================================
// Trash — soft-deleted files/folders with a 7-day retention window, restore,
// and permanent delete. Backed by GET /api/trash + POST .../restore +
// DELETE /api/trash/{batch}. One row per delete operation (a folder + its
// subtree is a single batch).
// ===========================================================================

const RETENTION_DAYS = 7;

type ItemKind = "file" | "folder";

// Raw row from GET /api/trash.
interface TrashEntry {
  batch: number;
  kind: ItemKind;
  id: number;
  name: string;
  location: string;
  size_bytes: number;
  item_count: number;
  deleted_at: string;
  purge_at: string;
  deleted_by_email: string;
}

interface TrashResponse {
  items: TrashEntry[];
}

// View-model the table renders.
interface TrashItem {
  batch: number;
  id: number;
  kind: ItemKind;
  name: string;
  typeLabel: string;
  location: string;
  deletedAt: string; // formatted
  purgeInDays: number;
  size: string; // formatted
  sizeBytes: number; // raw, for sorting
}

const PAGE_SIZE = 10;

type SortKey = "item" | "type" | "location" | "size" | "deleted" | "purges";
type SortDir = "asc" | "desc";

function compareItems(a: TrashItem, b: TrashItem, key: SortKey): number {
  switch (key) {
    case "item":
      return a.name.localeCompare(b.name);
    case "type":
      return a.typeLabel.localeCompare(b.typeLabel);
    case "location":
      return a.location.localeCompare(b.location);
    case "size":
      return a.sizeBytes - b.sizeBytes;
    case "deleted":
      return a.purgeInDays - b.purgeInDays;
    case "purges":
      return a.purgeInDays - b.purgeInDays;
    default:
      return 0;
  }
}

export default function Trash() {
  const { identity } = useAuth();

  const [items, setItems] = useState<TrashItem[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [actionBusy, setActionBusy] = useState<number | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [purgeTarget, setPurgeTarget] = useState<TrashItem | null>(null);
  // Bulk selection is tracked by batch (restore/purge operate per batch).
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkPurgeOpen, setBulkPurgeOpen] = useState(false);

  const [searchInput, setSearchInput] = useState("");
  const [activeQuery, setActiveQuery] = useState("");
  const [offset, setOffset] = useState(0);
  const [sortBy, setSortBy] = useState<SortKey>("deleted");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const load = () => {
    setLoading(true);
    api
      .get<TrashResponse>("/api/trash")
      .then((res) => {
        setItems((res.items ?? []).map(toItem));
        setError(null);
      })
      .catch((err) => {
        setError(err instanceof ApiError ? err.message : "Couldn't load trash");
        setItems([]);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onRestore = async (batch: number) => {
    if (actionBusy !== null) return;
    setActionBusy(batch);
    setActionError(null);
    try {
      await api.post<void>(`/api/trash/${batch}/restore`);
      load();
      emitTrashChanged();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "Restore failed");
    } finally {
      setActionBusy(null);
    }
  };

  // Purge opens a styled confirmation modal (no more browser confirm()).
  const onPurge = (item: TrashItem) => {
    if (actionBusy !== null) return;
    setPurgeTarget(item);
  };

  const onSort = (key: SortKey) => {
    setOffset(0);
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(key);
      setSortDir(key === "deleted" || key === "size" ? "desc" : "asc");
    }
  };

  const filtered = useMemo(() => {
    let rows = (items ?? []).slice();
    const q = activeQuery.trim().toLowerCase();
    if (q) {
      rows = rows.filter(
        (r) =>
          r.name.toLowerCase().includes(q) ||
          r.location.toLowerCase().includes(q) ||
          r.typeLabel.toLowerCase().includes(q),
      );
    }
    rows.sort((a, b) => {
      const base = compareItems(a, b, sortBy);
      return sortDir === "asc" ? base : -base;
    });
    return rows;
  }, [items, activeQuery, sortBy, sortDir]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const currentPage = Math.min(totalPages, Math.floor(offset / PAGE_SIZE) + 1);
  const visible = filtered.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const inSearch = activeQuery.trim() !== "";

  // ----- bulk selection (by batch) -----
  const filteredBatches = useMemo(
    () => Array.from(new Set(filtered.map((r) => r.batch))),
    [filtered],
  );
  const allSelected =
    filteredBatches.length > 0 && filteredBatches.every((b) => selected.has(b));
  const someSelected = selected.size > 0 && !allSelected;

  // Drop selections when the visible set changes out from under them.
  useEffect(() => {
    setSelected(new Set());
  }, [activeQuery]);

  const toggleAll = () => {
    setSelected(allSelected ? new Set() : new Set(filteredBatches));
  };
  const toggleOne = (batch: number) => {
    const next = new Set(selected);
    if (next.has(batch)) next.delete(batch);
    else next.add(batch);
    setSelected(next);
  };

  const bulkRestore = async () => {
    const batches = [...selected];
    if (batches.length === 0) return;
    setBulkBusy(true);
    setActionError(null);
    try {
      for (const b of batches) await api.post<void>(`/api/trash/${b}/restore`);
      setSelected(new Set());
      load();
      emitTrashChanged();
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "Restore failed");
    } finally {
      setBulkBusy(false);
    }
  };

  const bulkPurge = async () => {
    const batches = [...selected];
    for (const b of batches) await api.delete<void>(`/api/trash/${b}`);
    setSelected(new Set());
    load();
    emitTrashChanged();
  };

  // Initial-load skeleton — mirrors the whole layout, like Activity / Recent.
  if (items === null && !error) {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
          <div className="space-y-2">
            <Skeleton className="h-4 w-44" />
            <Skeleton className="h-8 w-36" />
          </div>
          <Skeleton className="h-6 w-72 rounded-full" />
          <div className="flex items-center justify-between gap-3">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="h-7 w-full max-w-72 rounded-[11px]" />
          </div>
          <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
            <div className="px-4">
              <table className="w-full table-fixed text-sm">
                <TrashColgroup />
                <TrashHead
                  sortBy={sortBy}
                  sortDir={sortDir}
                  onSort={onSort}
                  allSelected={allSelected}
                  someSelected={someSelected}
                  onToggleAll={toggleAll}
                />
                <tbody>
                  <tr aria-hidden="true">
                    <td colSpan={8} className="h-3 p-0" />
                  </tr>
                  {Array.from({ length: 6 }).map((_, i) => (
                    <TrashSkeletonRow key={`init-sk-${i}`} />
                  ))}
                  <tr aria-hidden="true">
                    <td colSpan={8} className="h-3 p-0" />
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        <PageHeader
          title="Trash"
          icon={<Trash2 className="h-6 w-6 text-sidebar-foreground" />}
          subtitle={`${identity?.tenant?.name ?? "Workspace"} · ${
            items?.length ?? 0
          } item${(items?.length ?? 0) === 1 ? "" : "s"} in trash`}
        />

        <div className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-[11px] text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
          Items are permanently deleted {RETENTION_DAYS} days after they're trashed
        </div>


        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-medium text-foreground">Items</h2>
            <span className="inline-flex items-center rounded-full bg-secondary px-2 py-0.5 text-[10.5px] font-semibold tabular-nums text-foreground/80">
              {filtered.length}
            </span>
          </div>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              setActiveQuery(searchInput.trim());
              setOffset(0);
            }}
            className="w-full sm:w-72"
          >
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search trashed items…"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                className="h-7 rounded-[11px] border border-border bg-card pl-10 pr-9 text-xs focus-visible:ring-1 focus-visible:ring-primary/40"
              />
              {(searchInput || inSearch) && (
                <button
                  type="button"
                  onClick={() => {
                    setSearchInput("");
                    setActiveQuery("");
                    setOffset(0);
                  }}
                  className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"
                  title="Clear search"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          </form>
        </div>

        {actionError && (
          <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {actionError}
          </div>
        )}

        <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
          {/* Bulk-actions bar — stays mounted and smoothly elevates into view
              (grid-rows + opacity transition) so it eases open/closed rather
              than popping. */}
          <div
            className={cn(
              "grid overflow-hidden transition-all duration-300 ease-out",
              selected.size > 0 ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0",
            )}
          >
            <div className="min-h-0">
              <div className="flex items-center gap-2 border-b border-border bg-primary/5 px-4 py-1.5">
                <span className="text-[12.5px] font-medium text-foreground">
                  {selected.size} selected
                </span>
                <div className="ml-auto flex items-center gap-1.5">
                  <button
                    type="button"
                    onClick={() => void bulkRestore()}
                    disabled={bulkBusy}
                    className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted disabled:opacity-50"
                  >
                    {bulkBusy ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <RotateCcw className="h-3.5 w-3.5" />
                    )}
                    Restore
                  </button>
                  <button
                    type="button"
                    onClick={() => setBulkPurgeOpen(true)}
                    disabled={bulkBusy}
                    className="inline-flex h-7 items-center gap-1.5 rounded-md bg-destructive px-2.5 text-[12px] font-semibold text-white hover:bg-destructive/90 disabled:opacity-50"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> Delete permanently
                  </button>
                  <button
                    type="button"
                    onClick={() => setSelected(new Set())}
                    className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                    title="Clear selection"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>
            </div>
          </div>
          <div className="max-h-[640px] overflow-y-auto">
            <div className="px-4">
              <table className="w-full table-fixed text-sm">
                <TrashColgroup />
                <TrashHead
                  sortBy={sortBy}
                  sortDir={sortDir}
                  onSort={onSort}
                  allSelected={allSelected}
                  someSelected={someSelected}
                  onToggleAll={toggleAll}
                />
                <tbody>
                  <tr aria-hidden="true">
                    <td colSpan={8} className="h-3 p-0" />
                  </tr>
                  {loading ? (
                    Array.from({ length: 6 }).map((_, i) => <TrashSkeletonRow key={`sk-${i}`} />)
                  ) : error ? (
                    <tr>
                      <td colSpan={8} className="px-4 py-10 text-center text-xs text-destructive">
                        {error}
                      </td>
                    </tr>
                  ) : visible.length === 0 ? (
                    <tr>
                      <td colSpan={8} className="px-4 py-10 text-center text-xs text-muted-foreground">
                        {inSearch ? "No trashed items match your search." : "Trash is empty."}
                      </td>
                    </tr>
                  ) : (
                    visible.map((it) => (
                      <TrashRow
                        key={it.batch}
                        it={it}
                        busy={actionBusy === it.batch}
                        disabled={(actionBusy !== null && actionBusy !== it.batch) || bulkBusy}
                        selected={selected.has(it.batch)}
                        onToggle={() => toggleOne(it.batch)}
                        onRestore={() => void onRestore(it.batch)}
                        onPurge={() => void onPurge(it)}
                      />
                    ))
                  )}
                  <tr aria-hidden="true">
                    <td colSpan={8} className="h-3 p-0" />
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>

        {filtered.length > PAGE_SIZE && (
          <Paginator
            currentPage={currentPage}
            totalPages={totalPages}
            onJump={(p) => setOffset((p - 1) * PAGE_SIZE)}
          />
        )}

        <p className="pt-2 text-[11px] text-muted-foreground">
          Restoring an item puts it back in its original location. After{" "}
          {RETENTION_DAYS} days in trash an item is permanently deleted and can't
          be recovered.
        </p>
      </div>

      {purgeTarget && (
        <ConfirmDialog
          title="Delete permanently?"
          danger
          confirmLabel="Delete permanently"
          message={
            <>
              “{purgeTarget.name}” will be permanently deleted. This can&apos;t be
              undone.
            </>
          }
          onConfirm={async () => {
            await api.delete<void>(`/api/trash/${purgeTarget.batch}`);
            load();
            emitTrashChanged();
          }}
          onClose={() => setPurgeTarget(null)}
        />
      )}

      {bulkPurgeOpen && (
        <ConfirmDialog
          title="Delete permanently?"
          danger
          confirmLabel={`Delete ${selected.size} permanently`}
          message={
            <>
              {selected.size} selected item{selected.size === 1 ? "" : "s"} will be
              permanently deleted. This can&apos;t be undone.
            </>
          }
          onConfirm={bulkPurge}
          onClose={() => setBulkPurgeOpen(false)}
        />
      )}
    </div>
  );
}

// ----- mapping helpers ----------------------------------------------------

function toItem(e: TrashEntry): TrashItem {
  return {
    batch: e.batch,
    id: e.id,
    kind: e.kind,
    name: e.name,
    typeLabel: deriveType(e.kind, e.name),
    location: e.location,
    deletedAt: formatDeletedAt(e.deleted_at),
    purgeInDays: daysUntil(e.purge_at),
    size: formatBytes(e.size_bytes),
    sizeBytes: e.size_bytes,
  };
}

function deriveType(kind: ItemKind, name: string): string {
  if (kind === "folder") return "Folder";
  const ext = name.includes(".") ? name.split(".").pop()!.toLowerCase() : "";
  const map: Record<string, string> = {
    pdf: "PDF",
    doc: "Document", docx: "Document", txt: "Document", md: "Document", rtf: "Document",
    xls: "Spreadsheet", xlsx: "Spreadsheet", csv: "Spreadsheet",
    ppt: "Presentation", pptx: "Presentation", key: "Presentation",
    png: "Image", jpg: "Image", jpeg: "Image", gif: "Image", svg: "Image", webp: "Image", heic: "Image",
    mp4: "Video", mov: "Video", avi: "Video", mkv: "Video", webm: "Video",
    mp3: "Audio", wav: "Audio", flac: "Audio", m4a: "Audio",
    zip: "Archive", rar: "Archive", "7z": "Archive", tar: "Archive", gz: "Archive",
    psd: "Design", ai: "Design", fig: "Design", sketch: "Design",
  };
  return map[ext] ?? (ext ? ext.toUpperCase() : "File");
}

function daysUntil(iso: string): number {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.ceil((t - Date.now()) / 86_400_000));
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function formatDeletedAt(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const eventDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const dayDiff = Math.round((startOfToday.getTime() - eventDay.getTime()) / 86_400_000);
  if (dayDiff === 0) return `Today · ${time}`;
  if (dayDiff === 1) return `Yesterday · ${time}`;
  const datePart = d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    ...(eventDay.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}),
  });
  return `${datePart} · ${time}`;
}

// ----- table pieces -------------------------------------------------------

function TrashColgroup() {
  return (
    <colgroup>
      <col style={{ width: "4%" }} />
      <col style={{ width: "20%" }} />
      <col style={{ width: "11%" }} />
      <col style={{ width: "17%" }} />
      <col style={{ width: "10%" }} />
      <col style={{ width: "14%" }} />
      <col style={{ width: "12%" }} />
      <col style={{ width: "12%" }} />
    </colgroup>
  );
}

function TrashHead({
  sortBy,
  sortDir,
  onSort,
  allSelected,
  someSelected,
  onToggleAll,
}: {
  sortBy: SortKey;
  sortDir: SortDir;
  onSort: (k: SortKey) => void;
  allSelected: boolean;
  someSelected: boolean;
  onToggleAll: () => void;
}) {
  return (
    <thead className="sticky top-0 z-10 bg-card">
      <tr className="border-b border-border text-[11.7px] uppercase tracking-wide text-muted-foreground">
        <th className="px-4 py-2.5 text-left">
          <Checkbox
            size="lg"
            checked={allSelected}
            ref={(el) => {
              if (el) el.indeterminate = someSelected;
            }}
            onChange={onToggleAll}
            aria-label="Select all"
          />
        </th>
        <SortHeader label="Item" col="item" sortBy={sortBy} sortDir={sortDir} onSort={onSort} />
        <SortHeader label="Type" col="type" sortBy={sortBy} sortDir={sortDir} onSort={onSort} className="hidden md:table-cell" />
        <SortHeader label="Location" col="location" sortBy={sortBy} sortDir={sortDir} onSort={onSort} className="hidden lg:table-cell" />
        <SortHeader label="Size" col="size" sortBy={sortBy} sortDir={sortDir} onSort={onSort} className="hidden sm:table-cell" />
        <SortHeader label="Deleted" col="deleted" sortBy={sortBy} sortDir={sortDir} onSort={onSort} className="hidden md:table-cell" />
        <SortHeader label="Purges in" col="purges" sortBy={sortBy} sortDir={sortDir} onSort={onSort} />
        <th className="px-4 py-2.5 text-left">Action</th>
      </tr>
    </thead>
  );
}

function TrashRow({
  it,
  busy,
  disabled,
  selected,
  onToggle,
  onRestore,
  onPurge,
}: {
  it: TrashItem;
  busy: boolean;
  disabled: boolean;
  selected: boolean;
  onToggle: () => void;
  onRestore: () => void;
  onPurge: () => void;
}) {
  const isFolder = it.kind === "folder";
  return (
    <tr className={cn("group transition-colors hover:bg-muted/40", busy && "opacity-60", selected && "bg-primary/5")}>
      <td className="px-4 py-1.5">
        <Checkbox
          size="lg"
          checked={selected}
          onChange={onToggle}
          aria-label={`Select ${it.name}`}
        />
      </td>
      <td className="px-4 py-1.5">
        <div className="flex min-w-0 items-center gap-2">
          {isFolder ? (
            <Folder className="h-4 w-4 flex-shrink-0 fill-amber-200/70 text-amber-500" />
          ) : (
            <FileText className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
          )}
          <span className="truncate text-xs font-medium text-foreground" title={it.name}>
            {it.name}
          </span>
        </div>
        <div className="truncate pl-6 text-[11px] text-muted-foreground lg:hidden" title={it.location}>
          {prettyPath(it.location)}
        </div>
      </td>
      <td className="hidden px-4 py-1.5 text-xs text-muted-foreground md:table-cell">{it.typeLabel}</td>
      <td className="hidden px-4 py-1.5 lg:table-cell">
        <span className="block truncate text-[12.5px] text-muted-foreground" title={it.location}>
          {prettyPath(it.location)}
        </span>
      </td>
      <td className="hidden px-4 py-1.5 text-[12.5px] tabular-nums text-muted-foreground sm:table-cell">
        {it.size}
      </td>
      <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground md:table-cell">
        <span className="block truncate" title={it.deletedAt}>
          {it.deletedAt}
        </span>
      </td>
      <td className="px-4 py-1.5">
        <PurgeChip days={it.purgeInDays} />
      </td>
      <td className="px-4 py-1.5">
        {/* Static icon actions, consistent with the share/portal action columns. */}
        <div className="flex items-center gap-0.5">
          <button
            type="button"
            onClick={onRestore}
            disabled={busy || disabled}
            className="rounded p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
            title={`Restore to ${prettyPath(it.location)}`}
          >
            {busy ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RotateCcw className="h-4 w-4" />
            )}
          </button>
          <button
            type="button"
            onClick={onPurge}
            disabled={busy || disabled}
            className="rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
            title="Delete permanently"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </td>
    </tr>
  );
}

function TrashSkeletonRow() {
  return (
    <tr aria-hidden="true">
      <td className="px-4 py-1.5">
        <Skeleton className="h-[10.5px] w-[10.5px] rounded-[3px]" />
      </td>
      <td className="px-4 py-1.5">
        <div className="flex items-center gap-2">
          <Skeleton className="h-4 w-4 flex-shrink-0 rounded" />
          <Skeleton className="h-3 w-32" />
        </div>
      </td>
      <td className="hidden px-4 py-1.5 md:table-cell">
        <Skeleton className="h-3 w-16" />
      </td>
      <td className="hidden px-4 py-1.5 lg:table-cell">
        <Skeleton className="h-3 w-28" />
      </td>
      <td className="hidden px-4 py-1.5 sm:table-cell">
        <Skeleton className="h-3 w-12" />
      </td>
      <td className="hidden px-4 py-1.5 md:table-cell">
        <Skeleton className="h-3 w-24" />
      </td>
      <td className="px-4 py-1.5">
        <Skeleton className="h-5 w-16 rounded-full" />
      </td>
      <td className="px-4 py-1.5">
        <Skeleton className="h-7 w-20 rounded-md" />
      </td>
    </tr>
  );
}

function PurgeChip({ days }: { days: number }) {
  const tone =
    days <= 3
      ? "border-destructive/30 bg-destructive/10 text-destructive"
      : days <= 7
        ? "border-amber-500/30 bg-amber-500/10 text-amber-600"
        : "border-border bg-secondary text-muted-foreground";
  const label = days <= 0 ? "Today" : days === 1 ? "1 day" : `${days} days`;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10.5px] font-medium tabular-nums",
        tone,
      )}
      title={`Permanently deleted in ${label}`}
    >
      <Clock className="h-3 w-3" />
      {label}
    </span>
  );
}

function prettyPath(path: string): string {
  const p = (path ?? "").trim();
  if (p === "" || p === "/") return "Workspace root";
  return p.replace(/\/+$/, "");
}

function SortHeader({
  label,
  col,
  sortBy,
  sortDir,
  onSort,
  className,
}: {
  label: string;
  col: SortKey;
  sortBy: SortKey;
  sortDir: SortDir;
  onSort: (k: SortKey) => void;
  className?: string;
}) {
  const active = sortBy === col;
  return (
    <th className={cn("px-4 py-2.5 text-left", className)}>
      <button
        type="button"
        onClick={() => onSort(col)}
        className="inline-flex items-center gap-1 transition-colors hover:text-foreground"
        aria-sort={active ? (sortDir === "asc" ? "ascending" : "descending") : "none"}
      >
        {label}
        {active ? (
          sortDir === "asc" ? (
            <ChevronUp className="h-3 w-3" />
          ) : (
            <ChevronDown className="h-3 w-3" />
          )
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-40" />
        )}
      </button>
    </th>
  );
}

function Paginator({
  currentPage,
  totalPages,
  onJump,
}: {
  currentPage: number;
  totalPages: number;
  onJump: (p: number) => void;
}) {
  const pages = useMemo<(number | "…")[]>(() => {
    if (totalPages <= 7) {
      return Array.from({ length: totalPages }, (_, i) => i + 1);
    }
    const wanted = new Set<number>();
    wanted.add(1);
    wanted.add(totalPages);
    for (let p = currentPage - 1; p <= currentPage + 1; p++) {
      if (p >= 1 && p <= totalPages) wanted.add(p);
    }
    const sorted = [...wanted].sort((a, b) => a - b);
    const out: (number | "…")[] = [];
    sorted.forEach((p, i) => {
      if (i > 0 && p - sorted[i - 1] > 1) out.push("…");
      out.push(p);
    });
    return out;
  }, [currentPage, totalPages]);

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
      <span className="tabular-nums">
        Page <span className="font-medium text-foreground">{currentPage}</span> of {totalPages}
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
            <span key={`gap-${i}`} className="select-none px-1 text-muted-foreground/70" aria-hidden="true">
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
