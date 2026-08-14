import { useCallback, useEffect, useState } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  ChevronLeft,
  ChevronRight,
  BarChart3,
  Clock,
  Code2,
  FolderOpen,
  HardDrive,
  Home,
  LifeBuoy,
  LogOut,
  PenTool,
  Settings,
  Share2,
  Sparkles,
  Star,
  Trash2,
  Users,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { api, type Quota } from "@/lib/api";
import {
  TRILLI_DRAG_MIME,
  emitItemsTrashed,
  emitTrashChanged,
  onStorageChanged,
  onTrashChanged,
} from "@/lib/events";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { ProductivityNav } from "@/components/productivity/ProductivityNav";
import { isProductivityAllowed } from "@/lib/productivity/access";
import { cn } from "@/lib/utils";

type LucideIcon = typeof FolderOpen;

// PdfIcon — an original flat PDF glyph in the common "white page + red PDF
// banner" style (folded corner, gray outline, red ribbon labeled PDF). lucide
// has no PDF icon and we avoid third-party/trademarked PDF assets, so this is
// drawn in-house with fixed colors (independent of the nav's currentColor).
const PdfIcon = (props: React.SVGProps<SVGSVGElement>) => (
  <svg
    viewBox="0 0 24 24"
    fill="none"
    aria-hidden="true"
    {...props}
    style={{ transform: "scale(1.1)", ...props.style }}
  >
    {/* page */}
    <path
      d="M6 2h8l4 4v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2z"
      fill="#ffffff"
      stroke="#9ca3af"
      strokeWidth={1.4}
    />
    {/* folded corner */}
    <path d="M14 2l4 4h-4z" fill="#d1d5db" />
    {/* red PDF banner */}
    <rect x="2.5" y="12" width="17" height="6" rx="1.2" fill="#E5252A" />
    <text
      x="11"
      y="16.6"
      textAnchor="middle"
      fontSize="4.6"
      fontWeight="800"
      fontFamily="Arial, sans-serif"
      fill="#ffffff"
      stroke="none"
    >
      PDF
    </text>
  </svg>
);

type NavItem = {
  to: string;
  label: string;
  icon: LucideIcon;
  comingSoon?: boolean;
  beta?: boolean;
};

// MAIN — a Workspace's contents + the dashboard. ("Workspace" is a system
// object/partition, so it's deliberately not used as a nav label.)
const mainItems: NavItem[] = [
  { to: "/home", label: "Home", icon: Home },
  { to: "/files", label: "Files", icon: FolderOpen },
  { to: "/recent", label: "Recent", icon: Clock },
  { to: "/starred", label: "Starred", icon: Star },
  { to: "/shared", label: "Shared", icon: Share2 },
  { to: "/trash", label: "Trash", icon: Trash2 },
];

// APPS — the Trilli app ecosystem layered on top of storage. Staged as
// coming-soon for now (see /apps/* routes).
const appsItems: NavItem[] = [
  { to: "/apps/pdf", label: "Trilli PDF", icon: PdfIcon as unknown as LucideIcon },
  { to: "/apps/sign", label: "Trilli Sign", icon: PenTool, comingSoon: true },
];

// MANAGE — account/team administration (admin-only). Renamed from the old
// "Workspace" group, which clashed with the Workspace object. Items flagged
// adminOnly are hidden below admin; the server mirrors this on the APIs.
type ManageItem = NavItem & { adminOnly?: boolean };
const manageItems: ManageItem[] = [
  { to: "/members", label: "Users", icon: Users, adminOnly: true },
  { to: "/activity", label: "Activity", icon: Activity, adminOnly: true },
];

function isAdminOrOwner(role: string | undefined | null): boolean {
  const r = (role ?? "").toLowerCase().trim();
  return r === "owner" || r === "admin";
}

const STORAGE_KEY = "trilli.sidebar.collapsed";

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  if (n < 1024 ** 4) return `${(n / 1024 ** 3).toFixed(2)} GB`;
  return `${(n / 1024 ** 4).toFixed(2)} TB`;
}

// Uncapped plans come back as max int64 from the API; treat anything absurdly
// large as "Unlimited" rather than rendering a nonsense byte count.
const UNLIMITED_BYTES = 1e18;

export function Sidebar() {
  const { identity, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState<boolean>(
    () => localStorage.getItem(STORAGE_KEY) === "1",
  );
  const [trashCount, setTrashCount] = useState(0);
  // Trilli Sign is GA — a first-class app for everyone, no beta badge.
  const appsNav = appsItems.map((it) =>
    it.to === "/apps/sign" ? { ...it, comingSoon: false } : it,
  );
  const [quota, setQuota] = useState<Quota | null>(null);

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, collapsed ? "1" : "0");
  }, [collapsed]);

  // Trash badge count — its own fetch so it can be refreshed both on
  // navigation and live whenever the trash changes (drag-to-trash, delete,
  // restore, purge) via the trash-changed event.
  const refetchTrashCount = useCallback(() => {
    api
      .get<{ count: number }>("/api/trash/count")
      .then((r) => setTrashCount(r.count ?? 0))
      .catch(() => {
        /* non-fatal — just hide the badge */
      });
  }, []);

  // Trash badge + storage quota — refetched on each navigation so they stay
  // roughly in sync (after deletes/restores/uploads) without a global store.
  const refetchQuota = useCallback(() => {
    api
      .get<Quota>("/api/files/quota")
      .then(setQuota)
      .catch(() => {
        /* non-fatal — fall back to identity used + no cap */
      });
  }, []);

  useEffect(() => {
    refetchTrashCount();
    refetchQuota();
  }, [location.pathname, refetchTrashCount, refetchQuota]);

  // Account switch: the pathname often DOESN'T change (the switcher lands on
  // /home), so key on the tenant itself. Drop the previous account's cached
  // figures immediately — the identity fallback already carries the new
  // account's numbers — then refetch the live quota + trash badge.
  const tenantId = identity?.tenant?.id;
  useEffect(() => {
    setQuota(null);
    setTrashCount(0);
    refetchTrashCount();
    refetchQuota();
  }, [tenantId, refetchTrashCount, refetchQuota]);

  // Keep the Trash count live: any trash mutation anywhere in the app emits
  // trash-changed; we refetch the badge immediately rather than waiting for
  // the next navigation.
  useEffect(() => onTrashChanged(refetchTrashCount), [refetchTrashCount]);

  // Keep the storage meter live: uploads/deletes/purges emit storage-changed;
  // refetch the quota immediately rather than waiting for the next navigation.
  useEffect(() => onStorageChanged(refetchQuota), [refetchQuota]);

  // Drag-and-drop a file/folder selection onto the Trash bin to soft-delete
  // it. Reads the same payload the file browser writes on dragstart, trashes
  // each item, then signals the browser to reload and the badge to refresh.
  const [trashDragOver, setTrashDragOver] = useState(false);
  const handleDropToTrash = useCallback(async (e: React.DragEvent) => {
    e.preventDefault();
    setTrashDragOver(false);
    let payload: { fileIds?: number[]; folderIds?: number[] } | null = null;
    try {
      const raw = e.dataTransfer.getData(TRILLI_DRAG_MIME);
      if (raw) payload = JSON.parse(raw);
    } catch {
      /* not an internal drag */
    }
    const fileIds = payload?.fileIds ?? [];
    const folderIds = payload?.folderIds ?? [];
    if (fileIds.length + folderIds.length === 0) return;
    try {
      await Promise.all([
        ...fileIds.map((id) => api.delete(`/api/files/${id}`)),
        ...folderIds.map((id) => api.delete(`/api/folders/${id}`)),
      ]);
    } catch {
      /* a failed item just stays put; the reload below reflects reality */
    }
    emitItemsTrashed(); // open Files view reloads, dropping the trashed rows
    emitTrashChanged(); // badge count refreshes
  }, []);

  // Storage figures: prefer the live quota endpoint, but fall back to the
  // identity payload (available synchronously at mount) for max/plan so the card
  // renders at its FINAL height immediately — the quota fetch then only refreshes
  // the numbers, never the layout (no load-time height jank).
  const used = quota?.storage_bytes_used ?? identity?.tenant?.storage_bytes_used ?? 0;
  const max = quota?.max_storage_bytes ?? identity?.tenant?.max_storage_bytes ?? 0;
  const planName = quota?.plan_name ?? identity?.tenant?.plan_name ?? "";
  const unlimited = max >= UNLIMITED_BYTES;
  const pct = unlimited || max <= 0 ? 0 : Math.min(100, (used / max) * 100);
  // Show "<0.1%" for tiny-but-nonzero usage so e.g. a 498 MB file on a 3 TB
  // plan reads as "<0.1%" rather than a confusing "0.0%".
  const pctLabel = pct > 0 && pct < 0.1 ? "<0.1" : pct.toFixed(1);
  const isAdmin = isAdminOrOwner(identity?.membership?.role);
  const visibleManage = manageItems.filter((it) => !it.adminOnly || isAdmin);

  const handleSignOut = async () => {
    await logout();
    navigate("/login", { replace: true });
  };

  return (
    <TooltipProvider delayDuration={0}>
      <aside
        className={cn(
          "relative z-30 flex h-full shrink-0 flex-col bg-sidebar text-sidebar-foreground transition-[width] duration-300 ease-in-out",
          collapsed ? "w-[60px]" : "w-[228px]",
        )}
      >
        {/* Brand now lives in the global navy top bar (AppShell); the sidebar
            starts directly with the nav. A little top padding keeps the first
            section header clear of the collapse toggle. */}
        {/* Nav */}
        {/* On short laptop panels the nav can't always fit every row in the
            leftover height; the account-hub card below is pinned outside this
            scroll region so it stays visible. Hide the scrollbar chrome there
            (wheel/trackpad still scrolls) so no gutter bar appears. */}
        <nav className="flex-1 space-y-1 overflow-y-auto p-3 pt-4 [@media(max-height:1100px)]:pt-2 [@media(max-height:1100px)]:[scrollbar-width:none] [@media(max-height:1100px)]:[&::-webkit-scrollbar]:hidden">
          <NavSection
            title="Main"
            items={mainItems}
            collapsed={collapsed}
            trashCount={trashCount}
            onDropToTrash={handleDropToTrash}
            trashDragOver={trashDragOver}
            setTrashDragOver={setTrashDragOver}
          />
          <div className={cn("mt-6 pt-4 border-t border-sidebar-border [@media(max-height:1100px)]:mt-2 [@media(max-height:1100px)]:pt-2", collapsed && "mx-1")}>
            {/* Productivity section is GA (Trilli Docs is open to all). Which
                apps appear live inside it is the per-app rollout gate's call
                (isAppEnabled) — e.g. Sheets is allowlisted while we build it. */}
            <NavSection
              title="Apps"
              items={appsNav}
              collapsed={collapsed}
              prelude={
                isProductivityAllowed(identity?.user?.email) ? (
                  <ProductivityNav collapsed={collapsed} />
                ) : null
              }
            />
          </div>
          {visibleManage.length > 0 && (
            <div className={cn("mt-6 pt-4 border-t border-sidebar-border [@media(max-height:1100px)]:mt-2 [@media(max-height:1100px)]:pt-2", collapsed && "mx-1")}>
              <NavSection title="Manage" items={visibleManage} collapsed={collapsed} />
            </div>
          )}
        </nav>

        {/* Account hub — one card holding storage/usage, the upgrade
            up-sell, quick links (Plan & usage, Settings), and the user's
            identity + sign-out. Replaces the old separate Storage + User
            blocks. Collapses to a compact icon stack. */}
        {!collapsed ? (
          // On short laptop panels (~13-15", viewport height <= 850px) the
          // account-hub card crowds the nav, so the height-keyed variants below
          // tighten its padding, spacing and type. Taller displays are
          // untouched.
          <div className="p-3 [@media(max-height:1100px)]:p-2">
            <div className="overflow-hidden rounded-xl border border-border bg-card shadow-md ring-1 ring-black/[0.02]">
              {/* Storage + plan + upgrade */}
              <div className="space-y-2.5 p-3 [@media(max-height:1100px)]:space-y-1.5 [@media(max-height:1100px)]:p-2.5">
                {/* Section label. Plan tier now lives only in the navy header. */}
                <div className="flex min-w-0 items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-wide text-muted-foreground">
                  <HardDrive className="h-3.5 w-3.5 flex-shrink-0" />
                  <span>Storage</span>
                </div>

                {/* Usage figure. Unlimited reads as one phrase — "66.5 MB / of
                    unlimited" — with a slash divider; capped plans show the
                    percentage centered in the space to the right. */}
                <div className="flex items-center gap-1.5">
                  <span className="text-[15px] font-semibold tabular-nums leading-none text-foreground [@media(max-height:1100px)]:text-[13.5px]">
                    {formatBytes(used)}
                  </span>
                  {unlimited && (
                    <span className="text-[15px] leading-none text-muted-foreground/60">/</span>
                  )}
                  <span
                    className={cn(
                      "tabular-nums text-muted-foreground",
                      unlimited
                        ? "text-[16px] font-semibold leading-none"
                        : "flex-1 text-center text-[10.5px]",
                    )}
                  >
                    {unlimited ? "of unlimited" : `${pctLabel}% of ${formatBytes(max)}`}
                  </span>
                </div>

                {/* Progress bar — full animated gradient on unlimited plans */}
                <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                  {unlimited ? (
                    <div className="h-full w-full bg-gradient-to-r from-primary/30 via-primary to-primary/30" />
                  ) : (
                    <div
                      className={cn(
                        "h-full transition-all duration-300",
                        pct >= 100 ? "bg-destructive" : pct >= 80 ? "bg-destructive/70" : "bg-primary",
                      )}
                      style={{ width: `${pct}%` }}
                    />
                  )}
                </div>
                {/* Upgrade up-sell is owner/admin only, and hidden once the
                    account is on an unlimited (top-tier) plan. Its presence is
                    decided from identity's plan (known at mount) — not the async
                    quota fetch — so the card never grows/jerks after load. */}
                {isAdmin && !unlimited && max > 0 && (
                  <button
                    type="button"
                    onClick={() => navigate("/settings/plan")}
                    title="Compare plans and upgrade"
                    className="flex w-full items-center justify-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground shadow-sm transition-opacity hover:opacity-90 [@media(max-height:1100px)]:py-1"
                  >
                    <Sparkles className="h-3.5 w-3.5" />
                    Upgrade plan
                    <ChevronRight className="h-3 w-3 opacity-80" />
                  </button>
                )}
              </div>

              {/* Quick links + logout — one divider line above this block. */}
              <div className="space-y-1 border-t border-border p-2 [@media(max-height:1100px)]:space-y-0 [@media(max-height:1100px)]:p-1.5">
                <HubLink to="/settings" icon={Settings} label="Settings" />
                {isAdmin && <HubLink to="/usage" icon={BarChart3} label="Usage" />}
                {isAdmin && <HubLink to="/api-access" icon={Code2} label="API" />}
                <HubLink to="/support" icon={LifeBuoy} label="Support" />
                <button
                  type="button"
                  onClick={handleSignOut}
                  className="group relative flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] font-medium text-foreground transition-colors [@media(max-height:1100px)]:py-1"
                >
                  <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted opacity-0 transition-opacity group-hover:opacity-100" />
                  <LogOut className="relative h-[16px] w-[16px] flex-shrink-0 text-muted-foreground" />
                  <span className="relative flex-1 text-left">Logout</span>
                </button>
              </div>
            </div>
          </div>
        ) : (
          /* Collapsed: compact icon stack — usage, settings, logout. */
          <div className="flex flex-col items-center gap-3 border-t border-sidebar-border p-3">
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex justify-center">
                  <HardDrive className="h-5 w-5 text-muted-foreground" />
                </div>
              </TooltipTrigger>
              <TooltipContent side="right">
                {planName ? `${planName} · ` : ""}
                {unlimited
                  ? `${formatBytes(used)} used · Unlimited`
                  : `${formatBytes(used)} of ${formatBytes(max)} · ${pct.toFixed(1)}%`}
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <NavLink
                  to="/settings"
                  title="Settings"
                  className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"
                >
                  <Settings className="h-[18px] w-[18px]" />
                </NavLink>
              </TooltipTrigger>
              <TooltipContent side="right">Settings</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  onClick={handleSignOut}
                  title="Logout"
                  className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"
                >
                  <LogOut className="h-4 w-4" />
                </button>
              </TooltipTrigger>
              <TooltipContent side="right">Logout</TooltipContent>
            </Tooltip>
          </div>
        )}

        {/* Sidebar collapse / expand toggle. Straddles the sidebar's
            right edge and sits roughly on the FILES section header
            line — single button that swaps icon based on state. */}
        <button
          onClick={() => setCollapsed((v) => !v)}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          className="absolute -right-3 top-5 z-50 hidden h-6 w-6 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-sm hover:bg-muted hover:text-foreground md:flex"
        >
          {collapsed ? (
            <ChevronRight className="h-3.5 w-3.5" />
          ) : (
            <ChevronLeft className="h-3.5 w-3.5" />
          )}
        </button>
      </aside>
    </TooltipProvider>
  );
}

// HubLink is a full-width quick-link inside the account hub card (Plan &
// usage, Settings). Transparent — it inherits the card's white background;
// a subtle muted hover is the only fill.
function HubLink({
  to,
  icon: Icon,
  label,
  soon,
}: {
  to: string;
  icon: LucideIcon;
  label: string;
  soon?: boolean;
}) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        cn(
          // Highlight rendered as an inset layer below (not bg here) so the
          // selected pill matches the main nav: 10px shorter + amber bar.
          // py tightens on short laptop panels to keep the hub card compact.
          "group relative flex items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] font-medium text-foreground transition-colors [@media(max-height:1100px)]:py-1",
        )
      }
    >
      {({ isActive }) => (
        <>
          {!isActive && (
            <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted opacity-0 transition-opacity group-hover:opacity-100" />
          )}
          {isActive && (
            <>
              <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted" />
              <span className="absolute inset-y-[2.5px] left-0 w-[3px] rounded-full bg-primary" />
            </>
          )}
          <Icon className="relative h-[16px] w-[16px] flex-shrink-0 text-muted-foreground" />
          <span className="relative flex-1">{label}</span>
          {soon ? (
            <span className="relative rounded border border-border px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
              soon
            </span>
          ) : (
            <ChevronRight className="relative h-3.5 w-3.5 text-muted-foreground" />
          )}
        </>
      )}
    </NavLink>
  );
}

function NavSection({
  title,
  items,
  collapsed,
  trashCount = 0,
  onDropToTrash,
  trashDragOver = false,
  setTrashDragOver,
  prelude,
}: {
  title: string;
  items: NavItem[];
  collapsed: boolean;
  trashCount?: number;
  onDropToTrash?: (e: React.DragEvent) => void;
  trashDragOver?: boolean;
  setTrashDragOver?: (v: boolean) => void;
  // Optional node rendered between the section title and its items (e.g. the
  // Productivity flyout entry in the Apps section).
  prelude?: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      {!collapsed && (
        <span className="px-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          {title}
        </span>
      )}
      {prelude}
      {items.map((it) => {
        const Icon = it.icon;
        // Only the Trash item carries a live count badge for now.
        const isTrash = it.to === "/trash";
        const count = isTrash ? trashCount : 0;
        // The Trash row doubles as a drop target — drag files/folders onto it
        // to soft-delete them.
        const trashDnD =
          isTrash && onDropToTrash
            ? {
                onDragOver: (e: React.DragEvent) => {
                  if (!e.dataTransfer.types.includes(TRILLI_DRAG_MIME)) return;
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                  setTrashDragOver?.(true);
                },
                onDragLeave: () => setTrashDragOver?.(false),
                onDrop: onDropToTrash,
              }
            : {};
        const button = (
          <NavLink
            key={it.to}
            to={it.to}
            {...trashDnD}
            className={({ isActive }) =>
              cn(
                // +7% from the prior tightened spacing.
                "group relative flex w-full items-center gap-[11px] rounded-lg px-[11px] py-[9px] transition-all duration-200 [@media(max-height:1100px)]:py-[5px]",
                // Drag-over-trash affordance: a clear ring + tint so it reads
                // as a live drop zone while dragging items onto it.
                isTrash &&
                  trashDragOver &&
                  "bg-destructive/10 ring-2 ring-inset ring-destructive/60",
                // Non-selected text now sits at the same darkness as the
                // selected state — hover only adds the bg container, it
                // doesn't darken the text further (since text is already
                // fully dark). The selected highlight is rendered as an
                // inset layer below (not bg on the row itself) so it can be
                // 10px shorter while the row keeps its full click height.
                isActive
                  ? "text-sidebar-accent-foreground"
                  : "text-sidebar-foreground",
              )
            }
          >
            {({ isActive }) => (
              <>
                {/* Hover highlight — same inset height as the selected pill,
                    keeping the old hover color. */}
                {!isActive && (
                  <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted opacity-0 transition-opacity duration-200 group-hover:opacity-100" />
                )}
                {isActive && (
                  <>
                    {/* Selected highlight, inset top/bottom so the pill hugs
                        the icon/label and reads clearly shorter than the row. */}
                    <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted" />
                    {/* Left accent bar — amber/dark-orange — an extra cue that
                        this row is selected, beyond the fill. */}
                    <span className="absolute inset-y-[4.5px] left-0 w-[3px] rounded-full bg-primary" />
                  </>
                )}
                <Icon
                  className={cn(
                    // 17px -> 18px (+~7%)
                    "relative h-[18px] w-[18px] flex-shrink-0",
                    // Active item keeps the warm-brown primary accent; all
                    // other nav icons render true black. The Trash icon turns
                    // red while items are dragged over it.
                    isTrash && trashDragOver
                      ? "text-destructive"
                      : isActive
                        ? "text-primary"
                        : "text-black",
                  )}
                />
                {/* Collapsed: a small dot stands in for the count. */}
                {collapsed && count > 0 && (
                  <span className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-primary" />
                )}
                {!collapsed && (
                  <>
                    <span className="relative flex-1 text-left text-[13px] font-medium">{it.label}</span>
                    {count > 0 && (
                      <span className="relative rounded-full bg-sidebar-foreground/10 px-1.5 py-0.5 text-[10px] font-semibold tabular-nums text-sidebar-foreground/80">
                        {count > 99 ? "99+" : count}
                      </span>
                    )}
                    {it.comingSoon && (
                      <span className="rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
                        soon
                      </span>
                    )}
                    {it.beta && (
                      <span className="rounded bg-primary px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-white">
                        beta
                      </span>
                    )}
                  </>
                )}
              </>
            )}
          </NavLink>
        );

        if (collapsed) {
          // Minimized rail: a clean centered icon — NO box/fill. Active is
          // conveyed by the indigo icon color alone; hover just darkens it.
          const collapsedIcon = (
            <NavLink
              key={it.to}
              to={it.to}
              {...trashDnD}
              className={({ isActive }) =>
                cn(
                  "group relative mx-auto flex h-9 w-9 items-center justify-center rounded-lg transition-colors",
                  isTrash && trashDragOver
                    ? "text-destructive"
                    : isActive
                      ? "text-primary"
                      : "text-muted-foreground hover:text-foreground",
                )
              }
            >
              {({ isActive }) => (
                <>
                  <Icon
                    className={cn(
                      "h-[19px] w-[19px]",
                      isActive && "stroke-[2.4]",
                    )}
                  />
                  {count > 0 && (
                    <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full bg-primary" />
                  )}
                </>
              )}
            </NavLink>
          );
          return (
            <Tooltip key={it.to}>
              <TooltipTrigger asChild>{collapsedIcon}</TooltipTrigger>
              <TooltipContent side="right" className="font-medium">
                {it.label}
                {count > 0 && <span className="ml-1 opacity-60">({count})</span>}
                {it.comingSoon && <span className="ml-1 opacity-60">(soon)</span>}
                {it.beta && <span className="ml-1 opacity-60">(beta)</span>}
              </TooltipContent>
            </Tooltip>
          );
        }
        return button;
      })}
    </div>
  );
}
