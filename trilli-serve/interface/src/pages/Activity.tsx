import { useEffect, useMemo, useState } from "react";
import {
  Activity as ActivityIcon,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Download,
  Copy,
  Eye,
  FileText,
  Folder,
  FolderPlus,
  Move,
  Pencil,
  RotateCcw,
  Search,
  Share2,
  Star,
  Trash2,
  Upload,
  X,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { api, ApiError } from "@/lib/api";
import { PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

// ===========================================================================
// Activity (design concept — no functionality, no API calls yet)
//
// Visual mock for the audit log surface. The data below is static; the real
// page will read from a future GET /api/activity endpoint backed by an
// append-only audit_events table (immutable: INSERT-only, no UPDATE/DELETE
// permitted from app code; events are filtered down by tenant/user but
// every actor's event is recorded for the workspace owner to audit).
// ===========================================================================

type ActionKey =
  | "upload"
  | "download"
  | "move"
  | "copy"
  | "rename"
  | "delete"
  | "restore"
  | "create_folder"
  | "share"
  | "star"
  | "view"
  | "account_delete"
  | "account_reactivate";

interface ActivityEvent {
  id: number;
  at: string; // pre-formatted display string for the mock
  actorName: string;
  actorEmail: string;
  action: ActionKey;
  // What kind of thing the subject is — drives the file/folder glyph.
  targetType: string; // "file" | "folder"
  // Primary subject — file or folder name.
  itemName: string;
  itemPath: string; // breadcrumb-style path
  // For moves: source and destination paths.
  fromPath?: string;
  toPath?: string;
  // For renames: old/new names.
  oldName?: string;
  // Device + IP — surfaced subtly on the right.
  device: string;
  ip: string;
}

// Raw row shape from GET /api/activity. Mapped to ActivityEvent (with a
// formatted timestamp) before rendering.
interface ServerActivityEvent {
  id: number;
  actor_name: string;
  actor_email: string;
  action: ActionKey;
  target_type: string;
  item_name: string;
  item_path: string;
  from_path?: string | null;
  to_path?: string | null;
  old_name?: string | null;
  device: string;
  ip: string;
  created_at: string; // RFC3339
}

interface ActivityResponse {
  events: ServerActivityEvent[];
  total: number;
}

const PAGE_SIZE = 10;

// formatAt renders an ISO timestamp into the "Today · 2:34 PM" /
// "Yesterday · …" / "May 26 · …" style used in the design.
function formatAt(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const time = d.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });
  const now = new Date();
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const eventDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const dayDiff = Math.round(
    (startOfToday.getTime() - eventDay.getTime()) / 86_400_000,
  );
  if (dayDiff === 0) return `Today · ${time}`;
  if (dayDiff === 1) return `Yesterday · ${time}`;
  const datePart = d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    ...(eventDay.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}),
  });
  return `${datePart} · ${time}`;
}

function toEvent(e: ServerActivityEvent): ActivityEvent {
  return {
    id: e.id,
    at: formatAt(e.created_at),
    actorName: e.actor_name,
    actorEmail: e.actor_email,
    action: e.action,
    targetType: e.target_type,
    itemName: e.item_name,
    itemPath: e.item_path,
    fromPath: e.from_path ?? undefined,
    toPath: e.to_path ?? undefined,
    oldName: e.old_name ?? undefined,
    device: e.device,
    ip: e.ip,
  };
}

// Action metadata: icon + verb phrase + soft accent color. Each row's
// icon chip uses the accent at /15 opacity for the bg and full for the
// stroke — consistent with the small "chip + glyph" pattern used in the
// rest of the app.
const ACTION_META: Record<
  ActionKey,
  { label: string; icon: typeof Upload; chipClass: string; iconClass: string }
> = {
  upload:         { label: "uploaded",      icon: Upload,    chipClass: "bg-emerald-500/10",  iconClass: "text-emerald-600" },
  download:       { label: "downloaded",    icon: Download,  chipClass: "bg-sky-500/10",      iconClass: "text-sky-600" },
  move:           { label: "moved",         icon: Move,      chipClass: "bg-indigo-500/10",   iconClass: "text-indigo-600" },
  copy:           { label: "copied to another account", icon: Copy, chipClass: "bg-indigo-500/10", iconClass: "text-indigo-600" },
  rename:         { label: "renamed",       icon: Pencil,    chipClass: "bg-amber-500/10",    iconClass: "text-amber-600" },
  delete:         { label: "deleted",       icon: Trash2,    chipClass: "bg-destructive/10",  iconClass: "text-destructive" },
  restore:        { label: "restored",      icon: RotateCcw, chipClass: "bg-teal-500/10",     iconClass: "text-teal-600" },
  create_folder:  { label: "created folder", icon: FolderPlus,chipClass: "bg-amber-500/10",   iconClass: "text-amber-700" },
  share:          { label: "shared",        icon: Share2,    chipClass: "bg-violet-500/10",   iconClass: "text-violet-600" },
  star:           { label: "starred",       icon: Star,      chipClass: "bg-amber-500/10",    iconClass: "text-amber-500" },
  view:           { label: "previewed",     icon: Eye,       chipClass: "bg-secondary",       iconClass: "text-muted-foreground" },
  account_delete:     { label: "scheduled the account for deletion", icon: Trash2,    chipClass: "bg-destructive/10", iconClass: "text-destructive" },
  account_reactivate: { label: "reactivated the account",           icon: RotateCcw, chipClass: "bg-emerald-500/10", iconClass: "text-emerald-600" },
};

const RANGE_PRESETS = [
  { value: "today",  label: "Today" },
  { value: "7d",     label: "Last 7 days" },
  { value: "30d",    label: "Last 30 days" },
  { value: "all",    label: "All time" },
] as const;
type RangeKey = (typeof RANGE_PRESETS)[number]["value"];

const ACTION_FILTERS: { value: ActionKey | "all"; label: string }[] = [
  { value: "all",           label: "All actions" },
  { value: "upload",        label: "Uploads" },
  { value: "download",      label: "Downloads" },
  { value: "move",          label: "Moves" },
  { value: "copy",          label: "Copies" },
  { value: "rename",        label: "Renames" },
  { value: "delete",        label: "Deletes" },
  { value: "restore",       label: "Restores" },
  { value: "create_folder", label: "New folders" },
  { value: "share",         label: "Shares" },
];

export default function Activity() {
  const { identity } = useAuth();

  // Filter state — pushed into the /api/activity query string. The server
  // does the filtering + pagination (the log can grow unbounded).
  const [range, setRange] = useState<RangeKey>("7d");
  const [actionFilter, setActionFilter] = useState<ActionKey | "all">("all");
  const [searchInput, setSearchInput] = useState("");
  const [activeQuery, setActiveQuery] = useState("");
  const [offset, setOffset] = useState(0);

  const [events, setEvents] = useState<ActivityEvent[] | null>(null);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Reset to page 1 whenever a filter (not the offset) changes.
  useEffect(() => {
    setOffset(0);
  }, [range, actionFilter, activeQuery]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const params = new URLSearchParams();
    params.set("range", range);
    if (actionFilter !== "all") params.set("action", actionFilter);
    if (activeQuery.trim()) params.set("q", activeQuery.trim());
    params.set("offset", String(offset));
    params.set("limit", String(PAGE_SIZE));
    api
      .get<ActivityResponse>(`/api/activity?${params.toString()}`)
      .then((res) => {
        if (cancelled) return;
        setEvents((res.events ?? []).map(toEvent));
        setTotal(res.total ?? 0);
        setError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err instanceof ApiError ? err.message : "Couldn't load activity");
        setEvents([]);
        setTotal(0);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [range, actionFilter, activeQuery, offset]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const currentPage = Math.min(totalPages, Math.floor(offset / PAGE_SIZE) + 1);
  const visible = events ?? [];

  const inSearch = activeQuery.trim() !== "";

  // Initial-load skeleton — mirrors the whole layout (header, badge,
  // filters, events/search, table) so nothing pops in piecemeal, matching
  // Recent / Users. Subsequent filter/page fetches keep the real header
  // and only skeleton the table rows (handled in the tbody below).
  if (events === null && !error) {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
          <div className="space-y-2">
            <Skeleton className="h-4 w-44" />
            <Skeleton className="h-8 w-36" />
          </div>
          <Skeleton className="h-6 w-64 rounded-full" />
          <div className="flex items-center gap-2">
            <Skeleton className="h-7 w-28 rounded" />
            <Skeleton className="h-7 w-28 rounded" />
          </div>
          <div className="flex items-center justify-between gap-3">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="h-7 w-full max-w-72 rounded-[11px]" />
          </div>
          <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
            <div className="px-4">
              <table className="w-full table-fixed text-sm">
                <colgroup>
                  <col style={{ width: "16%" }} />
                  <col style={{ width: "22%" }} />
                  <col style={{ width: "14%" }} />
                  <col style={{ width: "28%" }} />
                  <col style={{ width: "20%" }} />
                </colgroup>
                <thead className="bg-card">
                  <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                    <th className="px-4 py-2.5 text-left">When</th>
                    <th className="px-4 py-2.5 text-left">Who</th>
                    <th className="px-4 py-2.5 text-left">What</th>
                    <th className="px-4 py-2.5 text-left">Subject</th>
                    <th className="hidden px-4 py-2.5 text-left md:table-cell">Device</th>
                  </tr>
                </thead>
                <tbody>
                  <tr aria-hidden="true">
                    <td colSpan={5} className="h-3 p-0" />
                  </tr>
                  {Array.from({ length: 6 }).map((_, i) => (
                    <ActivitySkeletonRow key={`init-sk-${i}`} />
                  ))}
                  <tr aria-hidden="true">
                    <td colSpan={5} className="h-3 p-0" />
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
          title="Activity"
          icon={<ActivityIcon className="h-6 w-6 text-sidebar-foreground" />}
          subtitle={`${identity?.tenant?.name ?? "Workspace"} · ${total} event${total === 1 ? "" : "s"}`}
        />

        {/* Immutable-log badge — visual reassurance that this view is the
            tamper-evident audit. Subtle so it reads as info, not warning. */}
        <div className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-[11px] text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
          Append-only audit log · entries are immutable
        </div>

        {/* Filters row — date range + action type only. */}
        <div className="flex flex-wrap items-center gap-2">
          <RangeFilter value={range} onChange={setRange} />
          <ActionFilterDropdown value={actionFilter} onChange={setActionFilter} />
        </div>

        {/* Result count pill on the left, search on the right —
            matches the Members header pattern. */}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-medium text-foreground">Events</h2>
            <span className="inline-flex items-center rounded-full bg-secondary px-2 py-0.5 text-[10.5px] font-semibold tabular-nums text-foreground/80">
              {total}
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
                placeholder="Search by file, folder, or person…"
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

        {/* Events table — same chrome as Users / Pending Invites
            (rounded-lg border, sticky thead, py-1.5 rows, top/bottom
            spacer rows). Soft vertical scroll inside its own viewport
            so the surrounding chrome (filters + pagination) stays
            anchored on long lists. */}
        <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
          <div className="max-h-[640px] overflow-y-auto">
            {/* px-4 on this wrapper insets the entire table content
                toward the middle of the card while preserving the
                outer card width — outer columns (When + Device)
                no longer hug the card's left/right edges. */}
            <div className="px-4">
            <table className="w-full table-fixed text-sm">
              <colgroup>
                <col style={{ width: "16%" }} />
                <col style={{ width: "22%" }} />
                <col style={{ width: "14%" }} />
                <col style={{ width: "28%" }} />
                <col style={{ width: "20%" }} />
              </colgroup>
              <thead className="sticky top-0 z-10 bg-card">
                <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                  <th className="px-4 py-2.5 text-left">When</th>
                  <th className="px-4 py-2.5 text-left">Who</th>
                  <th className="px-4 py-2.5 text-left">What</th>
                  <th className="px-4 py-2.5 text-left">Subject</th>
                  <th className="hidden px-4 py-2.5 text-left md:table-cell">
                    Device
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr aria-hidden="true">
                  <td colSpan={5} className="h-3 p-0" />
                </tr>
                {loading ? (
                  Array.from({ length: 6 }).map((_, i) => (
                    <ActivitySkeletonRow key={`sk-${i}`} />
                  ))
                ) : error ? (
                  <tr>
                    <td
                      colSpan={5}
                      className="px-4 py-10 text-center text-xs text-destructive"
                    >
                      {error}
                    </td>
                  </tr>
                ) : visible.length === 0 ? (
                  <tr>
                    <td
                      colSpan={5}
                      className="px-4 py-10 text-center text-xs text-muted-foreground"
                    >
                      No matching events.
                    </td>
                  </tr>
                ) : (
                  visible.map((ev) => <ActivityRow key={ev.id} ev={ev} />)
                )}
                <tr aria-hidden="true">
                  <td colSpan={5} className="h-3 p-0" />
                </tr>
              </tbody>
            </table>
            </div>
          </div>
        </div>

        {total > PAGE_SIZE && (
          <Paginator
            currentPage={currentPage}
            totalPages={totalPages}
            onJump={(p) => setOffset((p - 1) * PAGE_SIZE)}
          />
        )}

      </div>
    </div>
  );
}

// ----- row + filter pieces ------------------------------------------------

// shortenIp keeps IPv4 untouched and middle-ellipsizes IPv6 so the
// device column stays one line. Full address is preserved in the
// title attribute for hover. For IPv6 we keep the first two groups
// and the last two groups — typically the routing prefix + the host
// identifier — which is the part operators actually scan for.
// prettyPath renders the tenant root ("/") as a friendly label instead of a
// bare slash, and trims a trailing slash off deeper paths.
function prettyPath(path: string): string {
  const p = (path ?? "").trim();
  if (p === "" || p === "/") return "Workspace root";
  return p.replace(/\/+$/, "");
}

function shortenIp(ip: string): string {
  // IPv4 is always short enough — under 16 chars — pass through.
  if (!ip.includes(":")) {
    return ip.length <= 16 ? ip : `${ip.slice(0, 8)}…${ip.slice(-4)}`;
  }
  // IPv6 — keep the first group + last group so the user can spot
  // both the prefix and the host identifier. Anything beyond 14 chars
  // gets the middle collapsed so the cell never pushes past its width.
  if (ip.length <= 14) return ip;
  const parts = ip.split(":");
  const head = parts[0] || "::";
  const tail = parts[parts.length - 1] || "::";
  return `${head}:…:${tail}`;
}

function ActivityRow({ ev }: { ev: ActivityEvent }) {
  const meta = ACTION_META[ev.action];
  const Icon = meta.icon;
  return (
    <tr className="group transition-colors hover:bg-muted/40">
      <td className="px-4 py-1.5 text-[12.5px] text-muted-foreground">
        <span className="block truncate" title={ev.at}>
          {ev.at}
        </span>
      </td>
      <td className="px-4 py-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full bg-secondary text-[10px] font-semibold uppercase text-foreground/70">
            {ev.actorName
              .split(" ")
              .filter(Boolean)
              .slice(0, 2)
              .map((s) => s[0])
              .join("")}
          </div>
          <div className="min-w-0">
            <div className="truncate text-xs font-medium text-foreground" title={ev.actorName}>
              {ev.actorName}
            </div>
            <div className="truncate text-[11px] text-muted-foreground" title={ev.actorEmail}>
              {ev.actorEmail}
            </div>
          </div>
        </div>
      </td>
      <td className="px-4 py-1.5">
        <div className="flex items-center gap-2">
          <span
            className={cn(
              "inline-flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full",
              meta.chipClass,
            )}
          >
            <Icon className={cn("h-3.5 w-3.5", meta.iconClass)} />
          </span>
          <span className="text-xs font-medium text-foreground">
            {meta.label}
          </span>
        </div>
      </td>
      <td className="px-4 py-1.5">
        <div className="min-w-0">
          <div className="flex items-center gap-1.5 truncate">
            {ev.targetType === "folder" ? (
              <Folder
                className="h-3.5 w-3.5 flex-shrink-0 fill-amber-200/70 text-amber-500"
                aria-label="Folder"
              />
            ) : (
              <FileText
                className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground"
                aria-label="File"
              />
            )}
            <span
              className="truncate text-xs font-medium text-foreground"
              title={`${ev.targetType === "folder" ? "Folder" : "File"}: ${ev.itemName}`}
            >
              {ev.itemName}
            </span>
          </div>
          <div className="truncate text-[11px] text-muted-foreground" title={prettyPath(ev.itemPath)}>
            {ev.action === "move" && ev.fromPath && ev.toPath ? (
              <>
                <span>{prettyPath(ev.fromPath)}</span>
                <span className="mx-1 text-muted-foreground/60">→</span>
                <span>{prettyPath(ev.toPath)}</span>
              </>
            ) : ev.action === "rename" && ev.oldName ? (
              <>
                <span>was “{ev.oldName}”</span>
              </>
            ) : (
              prettyPath(ev.itemPath)
            )}
          </div>
        </div>
      </td>
      <td className="hidden overflow-hidden px-4 py-1.5 text-left text-[11.5px] text-muted-foreground md:table-cell">
        <div
          className="block truncate whitespace-nowrap"
          title={`${ev.device} / ${ev.ip}`}
        >
          <span className="font-medium text-foreground/80">{ev.device}</span>
          <span className="mx-1 text-muted-foreground/60">/</span>
          <span className="tabular-nums">{shortenIp(ev.ip)}</span>
        </div>
      </td>
    </tr>
  );
}

// ActivitySkeletonRow mirrors ActivityRow's five columns with soft pulsing
// placeholders so the table shape holds steady while events load — same
// Skeleton primitive used across Recent / Users.
function ActivitySkeletonRow() {
  return (
    <tr aria-hidden="true">
      <td className="px-4 py-1.5">
        <Skeleton className="h-3 w-24" />
      </td>
      <td className="px-4 py-1.5">
        <div className="flex items-center gap-2">
          <Skeleton className="h-6 w-6 flex-shrink-0 rounded-full" />
          <div className="space-y-1">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-2.5 w-32" />
          </div>
        </div>
      </td>
      <td className="px-4 py-1.5">
        <div className="flex items-center gap-2">
          <Skeleton className="h-6 w-6 flex-shrink-0 rounded-full" />
          <Skeleton className="h-3 w-16" />
        </div>
      </td>
      <td className="px-4 py-1.5">
        <div className="flex items-center gap-1.5">
          <Skeleton className="h-3.5 w-3.5 flex-shrink-0 rounded" />
          <div className="space-y-1">
            <Skeleton className="h-3 w-32" />
            <Skeleton className="h-2.5 w-20" />
          </div>
        </div>
      </td>
      <td className="hidden px-4 py-1.5 md:table-cell">
        <Skeleton className="h-3 w-28" />
      </td>
    </tr>
  );
}

function RangeFilter({
  value,
  onChange,
}: {
  value: RangeKey;
  onChange: (v: RangeKey) => void;
}) {
  const label = RANGE_PRESETS.find((p) => p.value === value)?.label ?? "Range";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded border border-border bg-card px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
        >
          {label}
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-40">
        {RANGE_PRESETS.map((p) => (
          <DropdownMenuItem
            key={p.value}
            onSelect={() => onChange(p.value)}
            className="text-xs"
          >
            {p.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ActionFilterDropdown({
  value,
  onChange,
}: {
  value: ActionKey | "all";
  onChange: (v: ActionKey | "all") => void;
}) {
  const label = ACTION_FILTERS.find((p) => p.value === value)?.label ?? "All actions";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded border border-border bg-card px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
        >
          {label}
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-44">
        {ACTION_FILTERS.map((p) => (
          <DropdownMenuItem
            key={p.value}
            onSelect={() => onChange(p.value)}
            className="text-xs"
          >
            {p.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// Lifted from Members — same look, simpler signature (no jump-to-page).
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
        Page <span className="font-medium text-foreground">{currentPage}</span> of{" "}
        {totalPages}
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
              className="px-1 text-muted-foreground/70 select-none"
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
                  ? "bg-primary text-primary-foreground font-medium"
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
