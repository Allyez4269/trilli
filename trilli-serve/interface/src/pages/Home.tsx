import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ArrowDown,
  ArrowDownUp,
  ArrowRight,
  ArrowUp,
  Clock,
  Download,
  Eye,
  FilePlus2,
  FileText,
  Folder,
  HardDrive,
  FolderPlus,
  Inbox,
  Megaphone,
  Pencil,
  RotateCcw,
  Search,
  Share2,
  Sparkles,
  Star,
  Trash2,
  UserPlus,
  X,
  type LucideIcon,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { api, downloadUrl, type Quota, type StatsSummary } from "@/lib/api";
import { cn } from "@/lib/utils";
import { filesPath } from "@/lib/ids";
import { onStorageChanged } from "@/lib/events";
import { UserAvatar } from "@/components/UserAvatar";
import { Sparkline } from "@/components/Sparkline";
import { Skeleton } from "@/components/ui/skeleton";
import { displayName } from "@/lib/user-name";
import FilePreview, { canPreview } from "@/components/FilePreview";
import ShareModal, { type ShareTarget } from "@/components/ShareModal";
import { ConfirmDialog } from "@/components/Dialogs";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";

const UNLIMITED_BYTES = 1e18;

function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  if (n < 1024 ** 4) return `${(n / 1024 ** 3).toFixed(2)} GB`;
  return `${(n / 1024 ** 4).toFixed(2)} TB`;
}

function isAdminOrOwner(role: string | undefined | null): boolean {
  const r = (role ?? "").toLowerCase().trim();
  return r === "owner" || r === "admin";
}

// ----- Span model ----------------------------------------------------------
type SpanKey = "today" | "yesterday" | "this_month" | "prev_month" | "90d" | "custom";

const SPANS: { key: SpanKey; label: string }[] = [
  { key: "today", label: "Today" },
  { key: "yesterday", label: "Yesterday" },
  { key: "this_month", label: "This month" },
  { key: "prev_month", label: "Previous month" },
  { key: "90d", label: "90 days" },
];

const ymd = (d: Date) => d.toISOString().slice(0, 10);

// Short, friendly date for the custom-range label (e.g. "Jun 5").
const fmtDay = (s: string) => {
  const d = new Date(`${s}T00:00:00Z`);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
};
const customLabel = (from: string, to: string) =>
  from === to ? fmtDay(from) : `${fmtDay(from)} – ${fmtDay(to)}`;

// Resolve a span to an inclusive {from,to} (YYYY-MM-DD) plus a human label that
// the tiles reuse so every metric is "appropriately labeled for the span".
function resolveSpan(span: SpanKey, customFrom: string, customTo: string): { from: string; to: string; label: string } {
  const now = new Date();
  const today = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  switch (span) {
    case "today":
      return { from: ymd(today), to: ymd(today), label: "today" };
    case "yesterday": {
      const y = new Date(today);
      y.setUTCDate(y.getUTCDate() - 1);
      return { from: ymd(y), to: ymd(y), label: "yesterday" };
    }
    case "prev_month": {
      const f = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth() - 1, 1));
      const t = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 0));
      return { from: ymd(f), to: ymd(t), label: "last month" };
    }
    case "90d": {
      const f = new Date(today);
      f.setUTCDate(f.getUTCDate() - 89);
      return { from: ymd(f), to: ymd(today), label: "last 90 days" };
    }
    case "custom":
      return { from: customFrom, to: customTo, label: customLabel(customFrom, customTo) };
    case "this_month":
    default: {
      const f = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1));
      return { from: ymd(f), to: ymd(today), label: "this month" };
    }
  }
}

export default function Home() {
  const { identity } = useAuth();
  const navigate = useNavigate();
  const [q, setQ] = useState("");

  const [span, setSpan] = useState<SpanKey>("this_month");
  // Remember the span we were on before opening Custom, so collapsing the
  // custom control "undoes" back to it.
  const [prevSpan, setPrevSpan] = useState<SpanKey>("this_month");
  const today = ymd(new Date());
  const [customFrom, setCustomFrom] = useState(today);
  const [customTo, setCustomTo] = useState(today);
  const [summary, setSummary] = useState<StatsSummary | null>(null);
  const [loading, setLoading] = useState(true);
  // True until the very first stats response lands. Drives the full-page
  // skeleton; later span changes only repaint the tile values (firstLoad
  // stays false), so the page never re-skeletons out from under the user.
  const [firstLoad, setFirstLoad] = useState(true);

  const enterCustom = () => {
    if (span !== "custom") setPrevSpan(span);
    setSpan("custom");
  };
  const exitCustom = () => setSpan(prevSpan === "custom" ? "this_month" : prevSpan);

  const range = useMemo(() => resolveSpan(span, customFrom, customTo), [span, customFrom, customTo]);

  useEffect(() => {
    if (!range.from || !range.to) return;
    setLoading(true);
    void api
      .get<StatsSummary>(`/api/stats?from=${range.from}&to=${range.to}`)
      .then((s) => setSummary(s))
      .catch(() => setSummary(null))
      .finally(() => {
        setLoading(false);
        setFirstLoad(false);
      });
  }, [range.from, range.to]);

  // Live storage figures: fetch the plan-backed quota and keep it fresh —
  // uploads/deletes emit storage-changed, so the "Storage used" tile updates
  // in near real time instead of showing the stale identity tenant snapshot.
  const [quota, setQuota] = useState<Quota | null>(null);
  useEffect(() => {
    const load = () => api.get<Quota>("/api/files/quota").then(setQuota).catch(() => {});
    load();
    return onStorageChanged(load);
  }, []);

  const tenant = identity?.tenant;
  const isAdmin = isAdminOrOwner(identity?.membership?.role);
  // Greet by first name when we have one; a bare "Welcome back" beats
  // "Welcome back, there" when the profile has no name yet.
  const firstName = identity?.user?.first_name?.trim();
  const greeting = firstName ? `Welcome back, ${firstName}` : "Welcome back";
  // Prefer the live quota (refetched on storage-changed) over the identity
  // tenant snapshot, which is only loaded at auth time and goes stale.
  const storageUsed = quota?.storage_bytes_used ?? tenant?.storage_bytes_used ?? 0;
  const storageMax = quota?.max_storage_bytes ?? tenant?.max_storage_bytes ?? 0;
  const planUnlimited = storageMax >= UNLIMITED_BYTES;

  const transferTotal = summary ? summary.transfer_in + summary.transfer_out : 0;
  const series = summary?.series ?? [];
  const transferSeries = series.map((p) => p.transfer);
  const filesSeries = series.map((p) => p.files);

  // Initial load: lay the whole dashboard out as a skeleton so every widget
  // settles into place at once, the same way the other sections do.
  if (firstLoad) {
    return <HomeSkeleton isAdmin={isAdmin} showUpgrade={isAdmin && !planUnlimited} />;
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl px-6 py-7 lg:px-8">
        {/* Greeting / account line */}
        <div>
          <p className="text-[13px] text-muted-foreground">{tenant?.name ?? "Your account"}</p>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            {greeting}
          </h1>
        </div>

        {/* Search — jumps into the file browser's search. */}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const term = q.trim();
            if (term) navigate(filesPath(null, null, term));
          }}
          className="relative mt-5 w-1/2"
        >
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search files and folders…"
            className="h-10 w-full rounded-lg border border-border bg-card pl-9 pr-3 text-[13.5px] text-foreground shadow-sm outline-none transition-colors placeholder:text-muted-foreground focus:border-primary/40 focus:ring-2 focus:ring-primary/15"
          />
        </form>

        {/* Quick stats header + span control */}
        <div className="mt-8 flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
          <h2 className="text-[15px] font-semibold text-foreground">Quick stats</h2>
          <SpanControl
            span={span}
            onSpan={setSpan}
            onEnterCustom={enterCustom}
            onExitCustom={exitCustom}
            customFrom={customFrom}
            customTo={customTo}
            onCustomFrom={setCustomFrom}
            onCustomTo={setCustomTo}
          />
        </div>

        <div className="mt-10 grid gap-5 lg:grid-cols-3">
          <div className="space-y-5 lg:col-span-2">
            <div className={cn("grid grid-cols-1 gap-4", isAdmin ? "sm:grid-cols-3" : "sm:grid-cols-2")}>
            <StatTile
              icon={ArrowDownUp}
              label="Data transfer"
              span={range.label}
              value={formatBytes(transferTotal)}
              loading={loading}
              series={transferSeries}
              accent="#635BFF"
              detail={
                summary && (
                  <span className="flex items-center gap-3 text-[11px] text-muted-foreground">
                    <span className="inline-flex items-center gap-0.5">
                      <ArrowUp className="h-3 w-3" /> {formatBytes(summary.transfer_in)}
                    </span>
                    <span className="inline-flex items-center gap-0.5">
                      <ArrowDown className="h-3 w-3" /> {formatBytes(summary.transfer_out)}
                    </span>
                  </span>
                )
              }
            />
            <StatTile
              icon={FilePlus2}
              label="Files added"
              span={range.label}
              value={summary ? summary.files_added.toLocaleString() : "—"}
              loading={loading}
              series={filesSeries}
              accent="#10B981"
            />
            {isAdmin && (
              // Storage used — a live, always-meaningful number (a 2-seat
              // workspace staring at "New users: 0" every month reads as
              // failure). Range-independent, so the span shows the plan
              // ceiling and the detail is a thin fill meter.
              <StatTile
                icon={HardDrive}
                label="Storage used"
                span={
                  planUnlimited
                    ? "of unlimited storage"
                    : `of ${formatBytes(storageMax)} total`
                }
                value={formatBytes(storageUsed)}
                loading={loading}
                accent="#8B5CF6"
                detail={
                  !planUnlimited && storageMax > 0 ? (
                    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-[#8B5CF6]"
                        style={{
                          width: `${Math.min(100, (storageUsed / (storageMax || 1)) * 100).toFixed(2)}%`,
                        }}
                      />
                    </div>
                  ) : undefined
                }
              />
            )}
            </div>
            <RecentActivity />
          </div>

          {/* Right rail: What's new, quick actions, upgrade */}
          <div className="space-y-4">
            <WhatsNew />
            <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
              <h3 className="text-[13px] font-semibold text-foreground">Quick actions</h3>
              <div className="mt-3 space-y-1.5">
                <QuickAction to="/files" icon={Share2} label="Browse & share files" />
                <QuickAction to="/files" icon={Inbox} label="Request files (drop portal)" />
                {isAdmin && <QuickAction to="/members" icon={UserPlus} label="Invite members" />}
                <QuickAction to="/activity" icon={Clock} label="View activity log" />
              </div>
            </div>

            {isAdmin && !planUnlimited && (
              <Link
                to="/settings/plan"
                className="flex items-center gap-2.5 rounded-xl border border-primary/30 bg-primary/5 p-4 shadow-sm transition-colors hover:bg-primary/10"
              >
                <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
                  <Sparkles className="h-[18px] w-[18px]" />
                </span>
                <span className="min-w-0">
                  <span className="block text-[13px] font-semibold text-foreground">Upgrade your plan</span>
                  <span className="block text-[11.5px] text-muted-foreground">More storage, transfer & workspaces.</span>
                </span>
                <ArrowRight className="ml-auto h-4 w-4 flex-shrink-0 text-primary" />
              </Link>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// ----- Skeleton ------------------------------------------------------------
// Full-page placeholder mirroring the dashboard layout — greeting, search,
// quick-stats header + span control, the stat tiles (with sparkline area),
// recent-activity feed, and the right rail (what's new, quick actions, and the
// upgrade card). Shown until the first stats response lands.
function HomeSkeleton({ isAdmin, showUpgrade }: { isAdmin: boolean; showUpgrade: boolean }) {
  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-6xl px-6 py-7 lg:px-8">
        {/* Greeting / account line */}
        <div className="space-y-2.5">
          <Skeleton className="h-3.5 w-32" />
          <Skeleton className="h-7 w-64" />
        </div>

        {/* Search */}
        <Skeleton className="mt-5 h-10 w-1/2 rounded-lg" />

        {/* Quick stats header + span control */}
        <div className="mt-8 flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-8 w-72 rounded-lg" />
        </div>

        <div className="mt-10 grid gap-5 lg:grid-cols-3">
          <div className="space-y-5 lg:col-span-2">
            <div className={cn("grid grid-cols-1 gap-4", isAdmin ? "sm:grid-cols-3" : "sm:grid-cols-2")}>
              {Array.from({ length: isAdmin ? 3 : 2 }).map((_, i) => (
                <StatTileSkeleton key={i} />
              ))}
            </div>
            <ActivitySkeleton />
          </div>

          {/* Right rail */}
          <div className="space-y-4">
            <WhatsNewSkeleton />
            <QuickActionsSkeleton count={isAdmin ? 4 : 3} />
            {showUpgrade && <Skeleton className="h-[68px] w-full rounded-xl" />}
          </div>
        </div>
      </div>
    </div>
  );
}

function StatTileSkeleton() {
  return (
    <div className="flex flex-col rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-4 w-4 rounded" />
      </div>
      <Skeleton className="mt-3 h-6 w-24" />
      <Skeleton className="mt-2 h-2.5 w-16" />
      <Skeleton className="mt-3 h-10 w-full rounded" />
    </div>
  );
}

function ActivitySkeleton() {
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <Skeleton className="h-3.5 w-28" />
        <Skeleton className="h-3 w-12" />
      </div>
      <div className="mt-4 space-y-3.5">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="h-[37px] w-[37px] flex-shrink-0 rounded-full" />
            <Skeleton className="h-3 w-1/2" />
            <Skeleton className="ml-auto h-3 w-10" />
          </div>
        ))}
      </div>
    </section>
  );
}

function WhatsNewSkeleton() {
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-start gap-3">
        <Skeleton className="h-9 w-9 flex-shrink-0 rounded-lg" />
        <div className="flex-1 space-y-2">
          <Skeleton className="h-3.5 w-28" />
          <Skeleton className="h-3 w-full" />
          <Skeleton className="h-3 w-5/6" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      </div>
    </section>
  );
}

function QuickActionsSkeleton({ count }: { count: number }) {
  return (
    <div className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <Skeleton className="h-3.5 w-24" />
      <div className="mt-3 space-y-1.5">
        {Array.from({ length: count }).map((_, i) => (
          <div key={i} className="flex items-center gap-2.5 px-2 py-2">
            <Skeleton className="h-4 w-4 flex-shrink-0 rounded" />
            <Skeleton className="h-3.5 w-40" />
          </div>
        ))}
      </div>
    </div>
  );
}

// ----- Span control --------------------------------------------------------
// Presets + a Custom control that fluidly morphs: clicking "Custom" shrinks the
// label away and slides the from/to date pickers open (one cohesive control);
// the ✕ collapses it back, undoing to the previously selected span.
function SpanControl({
  span,
  onSpan,
  onEnterCustom,
  onExitCustom,
  customFrom,
  customTo,
  onCustomFrom,
  onCustomTo,
}: {
  span: SpanKey;
  onSpan: (s: SpanKey) => void;
  onEnterCustom: () => void;
  onExitCustom: () => void;
  customFrom: string;
  customTo: string;
  onCustomFrom: (v: string) => void;
  onCustomTo: (v: string) => void;
}) {
  const isCustom = span === "custom";
  return (
    <div className="inline-flex items-center gap-0.5 rounded-lg border border-border bg-muted/40 p-0.5">
      {SPANS.map((s) => (
        <button
          key={s.key}
          type="button"
          onClick={() => onSpan(s.key)}
          className={cn(
            "rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors",
            span === s.key
              ? "bg-secondary text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {s.label}
        </button>
      ))}

      {/* Custom label — collapses (fades + shrinks) when its range is open. */}
      <button
        type="button"
        onClick={onEnterCustom}
        tabIndex={isCustom ? -1 : 0}
        className={cn(
          "overflow-hidden whitespace-nowrap rounded-md text-[12px] font-medium text-muted-foreground transition-all duration-300 ease-in-out hover:text-foreground",
          isCustom ? "max-w-0 px-0 py-1 opacity-0" : "max-w-[72px] px-2.5 py-1 opacity-100",
        )}
      >
        Custom
      </button>

      {/* The range pickers slide open in the same control. */}
      <div
        className={cn(
          "flex items-center gap-1.5 overflow-hidden rounded-md transition-all duration-300 ease-in-out",
          isCustom ? "max-w-[340px] bg-card px-2 py-1 opacity-100 shadow-sm" : "max-w-0 px-0 py-1 opacity-0",
        )}
      >
        <input
          type="date"
          value={customFrom}
          max={customTo}
          onChange={(e) => onCustomFrom(e.target.value)}
          tabIndex={isCustom ? 0 : -1}
          className="bg-transparent text-[12px] text-foreground outline-none [color-scheme:light]"
        />
        <span className="text-muted-foreground">→</span>
        <input
          type="date"
          value={customTo}
          min={customFrom}
          onChange={(e) => onCustomTo(e.target.value)}
          tabIndex={isCustom ? 0 : -1}
          className="bg-transparent text-[12px] text-foreground outline-none [color-scheme:light]"
        />
        <button
          type="button"
          onClick={onExitCustom}
          aria-label="Close custom range"
          tabIndex={isCustom ? 0 : -1}
          className="ml-0.5 flex h-5 w-5 flex-shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

// ----- What's new bulletin -------------------------------------------------
// A product-announcement area (not span stats). Edit the copy as releases ship.
function WhatsNew() {
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-start gap-3">
        <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Megaphone className="h-[18px] w-[18px]" />
        </span>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="text-[13px] font-semibold text-foreground">What's new</h3>
            <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary">
              Coming soon
            </span>
          </div>
          <p className="mt-1.5 max-w-2xl text-[12.5px] leading-relaxed text-muted-foreground">
            We're hard at work on <span className="font-medium text-foreground">Trilli Desktop Sync</span> — keep
            your files automatically mirrored across all your devices, with offline access and background uploads.
            It's on the way, and we'll post right here the moment it ships. Thanks for being an early Trilli user. 🚀
          </p>
        </div>
      </div>
    </section>
  );
}

// ----- Tiles ---------------------------------------------------------------
function StatTile({
  icon: Icon,
  label,
  span,
  value,
  detail,
  loading,
  series,
  accent = "#635BFF",
}: {
  icon: typeof ArrowDownUp;
  label: string;
  span: string;
  value: string;
  detail?: React.ReactNode;
  loading?: boolean;
  series?: number[];
  accent?: string;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <span className="text-[12px] font-medium text-muted-foreground">{label}</span>
        <Icon className="h-4 w-4 text-muted-foreground" />
      </div>
      <div
        className={cn(
          "mt-2 text-[22px] font-semibold leading-none tabular-nums text-foreground",
          loading && "animate-pulse text-muted-foreground/40",
        )}
      >
        {value}
      </div>
      <p className="mt-1.5 text-[11px] capitalize text-muted-foreground">{span}</p>
      {detail && <div className="mt-2">{detail}</div>}
      {!loading && series && series.length >= 2 && (
        <div className="mt-3">
          <Sparkline data={series} stroke={accent} />
        </div>
      )}
    </div>
  );
}

function QuickAction({ to, icon: Icon, label }: { to: string; icon: typeof ArrowDownUp; label: string }) {
  return (
    <Link
      to={to}
      className="group flex items-center gap-2.5 rounded-lg px-2 py-2 text-[13px] text-foreground transition-colors hover:bg-muted"
    >
      <Icon className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
      <span className="flex-1">{label}</span>
      <ArrowRight className="h-3.5 w-3.5 text-muted-foreground/0 transition-colors group-hover:text-muted-foreground" />
    </Link>
  );
}

// ----- Recent activity -----------------------------------------------------
// Compact feed of the latest workspace activity (audit log) with member
// avatars. Admin/owner only — mirrors the admin-gated /api/activity endpoint.
type RecentEvent = {
  id: number;
  actor_name: string;
  actor_email: string;
  actor_avatar_url?: string;
  action: string;
  target_type?: string; // "file" | "folder"
  target_id?: number | null;
  item_name: string;
  item_path?: string;
  // For file events: false once the referenced file has been trashed/purged, so
  // the UI can drop the link, the right-click menu, and the preview action.
  // Non-file events are always true. (Optional for backwards-compat.)
  still_exists?: boolean;
  content_type?: string; // referenced file's MIME, when it still exists
  created_at: string;
};

// Deep-link an activity item back to its location in the file browser.
// Folders open directly (by opaque token); files are located by name search.
// Deleted items live in Trash, so they aren't linked.
function itemHref(ev: RecentEvent): string | null {
  if (!ev.item_name || ev.action === "delete") return null;
  // A file that has since been trashed/purged has no working destination —
  // don't link it (the name search would just turn up nothing).
  if (ev.target_type === "file" && ev.still_exists === false) return null;
  if (ev.target_type === "folder" && ev.target_id) {
    return filesPath(null, ev.target_id);
  }
  return filesPath(null, null, ev.item_name);
}

const VERB: Record<string, string> = {
  upload: "uploaded",
  download: "downloaded",
  move: "moved",
  copy: "copied to another account",
  account_delete: "scheduled the account for deletion",
  account_reactivate: "reactivated the account",
  rename: "renamed",
  delete: "deleted",
  restore: "restored",
  create_folder: "created folder",
  share: "shared",
  star: "starred",
  view: "viewed",
};

// Small colored status badge overlaid on the actor avatar (bottom-right),
// indicating the kind of action — mirrors the marketing-site activity feed.
const ACTION_BADGE: Record<string, { Icon: LucideIcon; className: string }> = {
  upload: { Icon: ArrowUp, className: "bg-emerald-500" },
  download: { Icon: ArrowDown, className: "bg-sky-500" },
  move: { Icon: ArrowDownUp, className: "bg-amber-500" },
  copy: { Icon: ArrowDownUp, className: "bg-indigo-500" },
  account_delete: { Icon: Trash2, className: "bg-rose-500" },
  account_reactivate: { Icon: RotateCcw, className: "bg-emerald-500" },
  rename: { Icon: Pencil, className: "bg-amber-500" },
  delete: { Icon: Trash2, className: "bg-rose-500" },
  restore: { Icon: RotateCcw, className: "bg-emerald-500" },
  create_folder: { Icon: FolderPlus, className: "bg-indigo-500" },
  share: { Icon: Share2, className: "bg-violet-500" },
  star: { Icon: Star, className: "bg-amber-400" },
  view: { Icon: Eye, className: "bg-slate-400" },
};

function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const secs = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (secs < 60) return "just now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function RecentActivity() {
  const [events, setEvents] = useState<RecentEvent[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [tick, setTick] = useState(0);
  // Right-click action state — preview overlay, share modal, trash confirm,
  // and a transient error line for actions on items that no longer exist.
  const [previewEv, setPreviewEv] = useState<RecentEvent | null>(null);
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [trashEv, setTrashEv] = useState<RecentEvent | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    void api
      .get<{ events: RecentEvent[] }>("/api/activity?range=30d&limit=6")
      .then((r) => setEvents((r.events ?? []).slice(0, 6)))
      .catch(() => setFailed(true));
  }, [tick]);

  const reload = () => setTick((t) => t + 1);

  const flashError = (msg: string) => {
    setActionError(msg);
    window.setTimeout(() => setActionError(null), 4000);
  };

  const starFile = async (ev: RecentEvent) => {
    if (!ev.target_id) return;
    try {
      await api.patch(`/api/files/${ev.target_id}`, { starred: true });
    } catch {
      flashError(`Couldn't star "${ev.item_name}" — it may have been moved or deleted.`);
    }
  };

  if (failed) return null;

  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <h3 className="text-[13px] font-semibold text-foreground">Recent activity</h3>
        <Link to="/activity" className="text-[12px] font-medium text-primary hover:underline">
          View all
        </Link>
      </div>

      {actionError && (
        <p className="mt-2 text-[12px] text-destructive">{actionError}</p>
      )}

      {events === null ? (
        <div className="mt-3 space-y-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3">
              <div className="h-8 w-8 flex-shrink-0 animate-pulse rounded-full bg-muted" />
              <div className="h-3 flex-1 animate-pulse rounded bg-muted" />
            </div>
          ))}
        </div>
      ) : events.length === 0 ? (
        <p className="mt-4 text-[12.5px] text-muted-foreground">
          No recent activity yet. Uploads, shares, and member changes will show up here.
        </p>
      ) : (
        // On 13-15" laptop panels (viewport <=1100px tall) the nth-child(6)
        // rule below drops the 6th row so Home fits without scrolling; larger
        // desktops keep all 6.
        <ul className="mt-2 divide-y divide-border/70 [@media(max-height:1100px)]:[&>li:nth-child(6)]:hidden">
          {events.map((ev) => (
            <ActivityRow
              key={ev.id}
              ev={ev}
              onPreview={() => setPreviewEv(ev)}
              onShare={() =>
                setShareTarget({
                  kind: ev.target_type === "folder" ? "folder" : "file",
                  id: ev.target_id!,
                  name: ev.item_name,
                })
              }
              onStar={() => void starFile(ev)}
              onTrash={() => setTrashEv(ev)}
            />
          ))}
        </ul>
      )}

      {previewEv?.target_id && (
        <FilePreview
          files={[
            {
              id: previewEv.target_id,
              name: previewEv.item_name,
              content_type: previewEv.content_type ?? "", // else kindOf uses the extension
              size_bytes: 0,
            },
          ]}
          index={0}
          onIndexChange={() => {}}
          onClose={() => setPreviewEv(null)}
        />
      )}
      {shareTarget && (
        <ShareModal target={shareTarget} onClose={() => setShareTarget(null)} />
      )}
      {trashEv?.target_id && (
        <ConfirmDialog
          title="Move to trash"
          message={
            <>
              Move <span className="font-medium">{trashEv.item_name}</span> to trash? You can
              restore it from Trash for 7 days.
            </>
          }
          confirmLabel="Move to trash"
          danger
          onConfirm={async () => {
            await api.delete(`/api/files/${trashEv.target_id}`);
            reload();
          }}
          onClose={() => setTrashEv(null)}
        />
      )}
    </section>
  );
}

function ActivityRow({
  ev,
  onPreview,
  onShare,
  onStar,
  onTrash,
}: {
  ev: RecentEvent;
  onPreview: () => void;
  onShare: () => void;
  onStar: () => void;
  onTrash: () => void;
}) {
  const navigate = useNavigate();
  const [first, ...rest] = (ev.actor_name ?? "").trim().split(/\s+/);
  const user = {
    first_name: first,
    last_name: rest.join(" "),
    email: ev.actor_email,
    avatar_url: ev.actor_avatar_url ?? null,
  };
  const verb = VERB[ev.action] ?? ev.action;
  const href = itemHref(ev);
  const ItemIcon = ev.target_type === "folder" ? Folder : FileText;
  const badge = ACTION_BADGE[ev.action];

  const item = ev.item_name ? (
    href ? (
      <Link
        to={href}
        title={ev.item_path ? `${ev.item_path}` : ev.item_name}
        className="inline-flex min-w-0 items-center gap-1 font-medium text-foreground transition-colors hover:text-primary"
      >
        <ItemIcon className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
        <span className="truncate hover:underline">{ev.item_name}</span>
      </Link>
    ) : (
      <span className="inline-flex min-w-0 items-center gap-1 font-medium text-foreground">
        <ItemIcon className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
        <span className="truncate">{ev.item_name}</span>
      </span>
    )
  ) : null;

  const row = (
    <li className="flex items-center gap-3 py-2.5">
      <div className="relative flex-shrink-0">
        <UserAvatar
          user={user}
          className="h-[37px] w-[37px] ring-[4.14px] ring-[#a3b8d8] shadow-sm"
        />
        {badge && (
          <span
            className={cn(
              "absolute -bottom-0.5 -right-0.5 flex h-[14px] w-[14px] items-center justify-center rounded-full text-white ring-2 ring-card",
              badge.className,
            )}
          >
            <badge.Icon className="h-[9px] w-[9px]" strokeWidth={2.5} />
          </span>
        )}
      </div>
      <div className="flex min-w-0 flex-1 items-center gap-1.5 text-[12px]">
        <span className="flex-shrink-0 font-medium text-foreground">{displayName(user)}</span>
        <span className="flex-shrink-0 text-muted-foreground">{verb}</span>
        {item}
      </div>
      <span className="flex-shrink-0 text-[11px] text-muted-foreground">
        {relativeTime(ev.created_at)}
      </span>
    </li>
  );

  // Right-click menu — only the actions that actually apply to this event.
  // Deleted items offer a jump to Trash; live files get the full file menu;
  // live folders open/share. Events with no surviving target get no menu.
  const isFolder = ev.target_type === "folder";
  // A file whose row has since been trashed/purged is no longer "live": no
  // link, no right-click menu (the file actions would all fail).
  const fileGone = ev.target_type === "file" && ev.still_exists === false;
  const live = !!ev.target_id && ev.action !== "delete" && !fileGone;

  if (ev.action === "delete") {
    return (
      <ContextMenu>
        <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
        <ContextMenuContent className="w-48">
          <ContextMenuItem onSelect={() => navigate("/trash")}>
            <Trash2 className="mr-2 h-4 w-4" />
            Open Trash
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    );
  }
  if (!live) return row;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
      <ContextMenuContent className="w-52">
        {isFolder ? (
          <>
            <ContextMenuItem onSelect={() => href && navigate(href)}>
              <Folder className="mr-2 h-4 w-4" />
              Open folder
            </ContextMenuItem>
            <ContextMenuItem onSelect={onShare}>
              <Share2 className="mr-2 h-4 w-4" />
              Share
            </ContextMenuItem>
          </>
        ) : (
          <>
            {/* Preview only for file types we can render or convert to PDF;
                non-previewable files (archives, binaries, …) get no Preview. */}
            {canPreview(ev.item_name, ev.content_type) && (
              <ContextMenuItem onSelect={onPreview}>
                <Eye className="mr-2 h-4 w-4" />
                Preview
              </ContextMenuItem>
            )}
            <ContextMenuItem
              onSelect={() => window.location.assign(downloadUrl(ev.target_id!))}
            >
              <Download className="mr-2 h-4 w-4" />
              Download
            </ContextMenuItem>
            <ContextMenuItem onSelect={onShare}>
              <Share2 className="mr-2 h-4 w-4" />
              Share
            </ContextMenuItem>
            <ContextMenuItem onSelect={onStar}>
              <Star className="mr-2 h-4 w-4" />
              Star
            </ContextMenuItem>
            {href && (
              <ContextMenuItem onSelect={() => navigate(href)}>
                <Search className="mr-2 h-4 w-4" />
                Show in Files
              </ContextMenuItem>
            )}
            <ContextMenuSeparator />
            <ContextMenuItem
              onSelect={onTrash}
              variant="destructive"
            >
              <Trash2 className="mr-2 h-4 w-4" />
              Move to trash
            </ContextMenuItem>
          </>
        )}
      </ContextMenuContent>
    </ContextMenu>
  );
}
