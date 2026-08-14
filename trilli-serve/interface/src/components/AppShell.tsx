import { useState } from "react";
import { Bell, AlertTriangle, Building2, Check, ChevronDown, Feather, Infinity as InfinityIcon, Loader2, Plus, Sparkles, Users } from "lucide-react";
import { Link, useLocation, useNavigate } from "react-router-dom";

import { useAuth } from "@/contexts/AuthContext";
import { useInactivityLogout } from "@/hooks/useInactivityLogout";
import { firstNameOf } from "@/lib/user-name";
import { TRILLI_DRAG_MIME } from "@/lib/events";
import { api, ApiError } from "@/lib/api";
import CopyToAccountModal, { type CopyAccount } from "@/components/CopyToAccountModal";
import { Sidebar } from "@/components/Sidebar";
import { TrilliLogo } from "@/components/Logo";
import { UserAvatar } from "@/components/UserAvatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

// AppShell wraps every authenticated page with the left sidebar.
// Pages render inside <main>; they own their own header/breadcrumbs.
//
// Layout:
//   +-----------+--------------------------------------+
//   |           |   Hi, Mike  [Admin]  [bell]  (avatar)|  <- top bar
//   |  sidebar  +--------------------------------------+
//   |           |  main card                           |
//   |           |                                      |
//   +-----------+--------------------------------------+
//
// Everything shares bg-sidebar (warm cream) as the canvas, with the main
// card floating inside it.
export function AppShell({ children }: { children: React.ReactNode }) {
  const { identity, switchTenant } = useAuth();
  const navigate = useNavigate();
  // The Productivity editor is a full-height surface — the marketing footer
  // under it is awkward and steals canvas space, so hide it there.
  const { pathname } = useLocation();
  const isEditorRoute = /^\/apps\/productivity\/[^/]+/.test(pathname);
  // Auto-log-out after the server-defined inactivity window (resets on any
  // interaction). Mounted here so it runs once for the whole authenticated app.
  useInactivityLogout();
  const greeting = firstNameOf(identity?.user) || "there";

  // In-app account switching — only when the user can access >1 account.
  const tenants = identity?.tenants ?? [];
  const activeTenantId = identity?.tenant?.id;
  const ownsAccount = tenants.some((t) => t.is_owner);
  const [switching, setSwitching] = useState<number | null>(null);
  // After a switch, keep the user's navigational CONTEXT but not their depth:
  // deep locations (folders, envelopes, tickets) belong to the old account, so
  // land at the top of the same section in the new one — /files stays /files
  // (at the new account's root workspace), /apps/sign stays /apps/sign, etc.
  const sectionRoot = (p: string): string => {
    if (p.startsWith("/files")) return "/files";
    const app = p.match(/^\/apps\/([^/]+)/);
    if (app) return `/apps/${app[1]}`;
    const seg = `/${p.split("/")[1] ?? ""}`;
    const roots = new Set([
      "/home", "/recent", "/starred", "/shared", "/trash",
      "/activity", "/members", "/usage", "/api-access", "/support", "/status",
    ]);
    return roots.has(seg) ? seg : "/home";
  };
  const onSwitchAccount = async (tenantId: number) => {
    if (switching || tenantId === activeTenantId) return;
    setSwitching(tenantId);
    try {
      await switchTenant(tenantId);
      navigate(sectionRoot(pathname), { replace: true });
    } catch {
      /* stay on the current account */
    } finally {
      setSwitching(null);
    }
  };
  const role = identity?.membership?.role ?? "member";

  // Drag-onto-switcher: dropping a file/folder selection (dragged from Files)
  // onto the account switcher opens the cross-account copy modal with that
  // selection. Cross-account transfers are always copies, never moves — the
  // modal discloses this and lets the user pick which account to copy into.
  const otherAccounts: CopyAccount[] = tenants
    .filter((t) => t.id !== activeTenantId)
    .map((t) => ({ id: t.id, name: t.name, is_owner: t.is_owner }));
  const currentAccountName = identity?.tenant?.name ?? "this account";
  const [dragOverSwitcher, setDragOverSwitcher] = useState(false);
  const [copyDrop, setCopyDrop] = useState<{ fileIds: number[]; folderIds: number[] } | null>(null);

  const isItemDrag = (e: React.DragEvent) => e.dataTransfer.types.includes(TRILLI_DRAG_MIME);
  const onSwitcherDragOver = (e: React.DragEvent) => {
    if (!isItemDrag(e) || otherAccounts.length === 0) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    setDragOverSwitcher(true);
  };
  const onSwitcherDrop = (e: React.DragEvent) => {
    if (!isItemDrag(e)) return;
    e.preventDefault();
    setDragOverSwitcher(false);
    try {
      const raw = e.dataTransfer.getData(TRILLI_DRAG_MIME);
      const p = raw ? JSON.parse(raw) : null;
      const fileIds: number[] = Array.isArray(p?.fileIds) ? p.fileIds : [];
      const folderIds: number[] = Array.isArray(p?.folderIds) ? p.folderIds : [];
      if (fileIds.length + folderIds.length > 0) setCopyDrop({ fileIds, folderIds });
    } catch {
      /* ignore a malformed drag payload */
    }
  };

  // Plan tier comes from the identity payload (loaded before this renders), so
  // the badge appears together with the avatar/role — no late-arriving fetch.
  const planName = identity?.tenant?.plan_name;
  const planUnlimited = (identity?.tenant?.max_storage_bytes ?? 0) >= 1e18;

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background">
      {/* Global navy chrome bar — brand left, user cluster right. */}
      <header className="flex h-14 shrink-0 items-center justify-between bg-chrome pl-4 pr-5 text-chrome-foreground">
        <Link to="/home" className="flex flex-shrink-0 items-center">
          <TrilliLogo className="h-[28px] text-white" />
        </Link>
        <div className="flex items-center gap-3">
          {tenants.length > 1 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  title={dragOverSwitcher ? "Drop to copy into another account" : "Switch account"}
                  onDragOver={onSwitcherDragOver}
                  onDragLeave={() => setDragOverSwitcher(false)}
                  onDrop={onSwitcherDrop}
                  className={cn(
                    "hidden items-center gap-1.5 rounded-md bg-white/10 px-2.5 py-1.5 text-[13px] font-medium text-white/90 outline-none transition-colors hover:bg-white/15 focus:outline-none sm:inline-flex",
                    dragOverSwitcher && "bg-white/25 ring-2 ring-white/70",
                  )}
                >
                  <Building2 className="h-3.5 w-3.5 opacity-80" />
                  <span className="w-[150px] truncate text-left">{identity?.tenant?.name ?? "Account"}</span>
                  <ChevronDown className="h-3.5 w-3.5 opacity-70" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64 p-1">
                <div className="px-2 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                  Switch account
                </div>
                {tenants.map((t) => (
                  <DropdownMenuItem
                    key={t.id}
                    onSelect={(e) => {
                      e.preventDefault();
                      void onSwitchAccount(t.id);
                    }}
                    className="group flex items-center justify-between gap-2 text-sm"
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      {/* No explicit text color: the menu-item rules paint these
                          muted at rest and accent-foreground when highlighted,
                          so they always match the row's text. */}
                      {t.is_owner ? (
                        <Building2 className="h-3.5 w-3.5 flex-shrink-0" />
                      ) : (
                        <Users className="h-3.5 w-3.5 flex-shrink-0" />
                      )}
                      <span className="min-w-0">
                        <span className="block truncate">{t.name}</span>
                        <span className="block text-[11px] opacity-70">
                          {t.is_owner ? "Your account" : "Member"}
                        </span>
                      </span>
                    </span>
                    {switching === t.id ? (
                      <Loader2 className="h-3.5 w-3.5 flex-shrink-0 animate-spin text-primary group-data-[highlighted]:text-accent-foreground" />
                    ) : (
                      t.id === activeTenantId && (
                        <Check className="h-3.5 w-3.5 flex-shrink-0 text-primary group-data-[highlighted]:text-accent-foreground" />
                      )
                    )}
                  </DropdownMenuItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => navigate("/get-started")} className="text-sm">
                  <Plus className="mr-2 h-3.5 w-3.5" /> Create your own account
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          {/* Member-only users (no account of their own, so no switcher above)
              get a direct invitation to start one. */}
          {tenants.length <= 1 && !ownsAccount && (
            <Link
              to="/get-started"
              className="hidden items-center gap-1.5 rounded-md bg-white/10 px-2.5 py-1.5 text-[13px] font-medium text-white/90 transition-colors hover:bg-white/15 sm:inline-flex"
            >
              <Plus className="h-3.5 w-3.5 opacity-80" />
              Create your own account
            </Link>
          )}
          <span className="hidden text-sm text-white/70 sm:block">
            Hi, <span className="font-medium text-white">{greeting}</span>
          </span>
          {/* On the navy bar the default bg-primary/10 fallback reads as a dark
              blob, so give the initials a light lavender circle (#f1f2ff) with
              indigo type and a periwinkle ring (#a6b2d0). The ring is a REAL
              padded circle (not a ring/border utility): box-shadow and border
              strokes rasterize jagged against the navy under the desktop zoom,
              while a padded element gets true geometric anti-aliasing on both
              edges. */}
          <span className="flex flex-shrink-0 rounded-full bg-[#a6b2d0] p-[3.9px]">
            <UserAvatar
              user={identity?.user}
              className="size-10"
              fallbackClassName="bg-[#f1f2ff] text-[15px] font-semibold leading-none tracking-tight text-primary"
            />
          </span>
          <RoleBadge role={role} />
          <PlanBadge planName={planName} unlimited={planUnlimited} />
          <NotificationsBell />
        </div>
      </header>
      <div className="flex flex-1 overflow-hidden">
        <Sidebar />
        <div className="flex flex-1 flex-col overflow-hidden">
          <main className="relative mb-2 mr-2 mt-2 flex flex-1 flex-col overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <LapseBanner tenant={identity?.tenant} />
            {/* Children area always fills the remaining height so the footer is
                pinned to the bottom even when a page's content is short. Pages
                that manage their own scroll (flex-1 overflow-y-auto) still do so
                within this region. */}
            {/* The Productivity editor is a fixed full-height frame that owns
                its own (iframe-internal) scrolling, so the shell wrapper must
                NOT scroll there — otherwise overflow-y:auto implies overflow-x:
                auto and a stray horizontal scrollbar flickers in on vertical
                scroll. Other routes keep normal vertical scrolling. */}
            {/* key=tenant: switching accounts REMOUNTS the page content, so no
                tenant-scoped state (workspace lists, folder ids, caches) can
                leak from the previous account into the new one. */}
            <div
              key={activeTenantId ?? "none"}
              className={cn("flex min-h-0 flex-1 flex-col", isEditorRoute ? "overflow-hidden" : "overflow-y-auto")}
            >
              {children}
            </div>
            {!isEditorRoute && <AppFooter />}
          </main>
        </div>
      </div>

      {copyDrop && otherAccounts.length > 0 && (
        <CopyToAccountModal
          fileCount={copyDrop.fileIds.length}
          folderCount={copyDrop.folderIds.length}
          fileIds={copyDrop.fileIds}
          folderIds={copyDrop.folderIds}
          accounts={otherAccounts}
          currentAccountName={currentAccountName}
          onClose={() => setCopyDrop(null)}
        />
      )}
    </div>
  );
}

// AppFooter is the slim, app-wide footer pinned to the bottom of every
// content card: a copyright line + tagline on the left, a row of minimal
// links on the right. Support is a real route; the legal/status links are
// inert placeholders to wire to real pages once they exist.
function AppFooter() {
  const year = new Date().getFullYear();
  const links: { label: string; to: string }[] = [
    { label: "Privacy", to: "/privacy" },
    { label: "Terms", to: "/terms" },
    { label: "Status", to: "/status" },
    { label: "Support", to: "/support" },
  ];
  return (
    <footer className="flex shrink-0 flex-col items-center justify-center gap-1.5 bg-card/60 px-6 py-3 text-[11px] text-muted-foreground [@media(max-height:1100px)]:gap-0.5 [@media(max-height:1100px)]:py-1.5">
      <nav className="flex items-center gap-x-4">
        {links.map((l) => (
          <Link
            key={l.label}
            to={l.to}
            className="transition-colors hover:text-foreground"
          >
            {l.label}
          </Link>
        ))}
      </nav>
      <p className="flex flex-wrap items-center justify-center gap-x-1.5 text-center">
        <span>&copy; {year} Trilli Media LLC. All rights reserved.</span>
        <span aria-hidden="true" className="text-border">
          |
        </span>
        <span>File storage and collaboration for modern teams.</span>
      </p>
    </footer>
  );
}

// LapseBanner is the account-wide read-only notice for an account in its purge
// window — either 'lapsed' (subscription ended) or 'closing' (owner asked to
// delete it). The account stays usable for reading/downloading, but every
// mutation is refused server-side until reactivated, and the data is purged on
// purge_at. Tone escalates in the final week. Returns nothing for healthy
// accounts. For 'closing', Reactivate cancels the deletion inline (and may
// re-charge a refunded account); for 'lapsed' it routes to billing.
function LapseBanner({ tenant }: { tenant?: import("@/lib/api").Tenant }) {
  const { refresh } = useAuth();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const state = tenant?.lifecycle_state;
  const closing = state === "closing";
  const lapsed = state === "lapsed";

  const reactivate = async () => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.post("/api/account/reactivate", {});
      await refresh({ silent: true });
    } catch (e) {
      if (e instanceof ApiError && e.status === 402) {
        setErr("Add a payment method to reactivate.");
        navigate("/settings/plan");
      } else {
        setErr(e instanceof ApiError ? e.message : "Couldn't reactivate. Try again.");
        setBusy(false);
      }
    }
  };

  if (!tenant || (!lapsed && !closing)) return null;

  let dateLabel = "";
  let daysLeft: number | null = null;
  if (tenant.purge_at) {
    const purge = new Date(tenant.purge_at);
    dateLabel = purge.toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" });
    daysLeft = Math.max(0, Math.ceil((purge.getTime() - Date.now()) / 86_400_000));
  }
  const urgent = daysLeft !== null && daysLeft <= 7;

  return (
    <div
      className={cn(
        "flex flex-shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-6 py-2.5 text-sm",
        urgent
          ? "border-destructive/30 bg-destructive/10 text-destructive"
          : "border-amber-500/30 bg-amber-500/10 text-amber-700",
      )}
    >
      <AlertTriangle className="h-4 w-4 flex-shrink-0" />
      <span className="font-medium">
        {closing
          ? "This account is scheduled for deletion — it's read-only."
          : "Your subscription has ended — this account is read-only."}
      </span>
      <span className="text-foreground/70">
        You can still view and download everything.
        {dateLabel && (
          <>
            {" "}Your data will be permanently deleted on{" "}
            <span className="font-semibold">{dateLabel}</span>
            {daysLeft !== null && <> (in {daysLeft} {daysLeft === 1 ? "day" : "days"})</>}.
          </>
        )}
        {err && <span className="ml-1 font-medium text-destructive">{err}</span>}
      </span>
      {closing ? (
        <button
          type="button"
          onClick={() => void reactivate()}
          disabled={busy}
          className="ml-auto inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1 text-xs font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
        >
          {busy && <Loader2 className="h-3 w-3 animate-spin" />}
          {busy ? "Reactivating…" : "Reactivate"}
        </button>
      ) : (
        <Link
          to="/settings/plan"
          className="ml-auto rounded-md bg-primary px-3 py-1 text-xs font-semibold text-primary-foreground hover:bg-primary/90"
        >
          Reactivate
        </Link>
      )}
    </div>
  );
}

interface Alert {
  id: number;
  title: string;
  body?: string;
  at: string;
}

// NotificationsBell: dimmed with no badge when there are no alerts; when
// alerts exist the bell undims and shows an unread count, and clicking opens
// a dropdown of them. No notifications backend yet, so the list is empty —
// swap `alerts` for a real feed to light it up.
function NotificationsBell() {
  const alerts: Alert[] = [];
  const count = alerts.length;
  const hasAlerts = count > 0;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          title="Notifications"
          className={cn(
            "relative h-9 w-9 transition-colors hover:bg-white/10",
            hasAlerts
              ? "text-white/80 hover:text-white"
              : "text-white/50 hover:text-white/80",
          )}
        >
          <Bell className="h-5 w-5" />
          {hasAlerts && (
            <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-[16px] items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold leading-none text-destructive-foreground">
              {count > 9 ? "9+" : count}
            </span>
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <div className="px-2 py-1.5 text-xs font-semibold text-foreground">Notifications</div>
        <DropdownMenuSeparator />
        {hasAlerts ? (
          alerts.map((a) => (
            <DropdownMenuItem key={a.id} className="flex flex-col items-start gap-0.5">
              <span className="text-xs font-medium text-foreground">{a.title}</span>
              {a.body && <span className="text-[11px] text-muted-foreground">{a.body}</span>}
              <span className="text-[10px] text-muted-foreground/70">{a.at}</span>
            </DropdownMenuItem>
          ))
        ) : (
          <div className="px-3 py-6 text-center text-xs text-muted-foreground">
            You're all caught up — no notifications.
          </div>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// PlanBadge — the single place the plan tier is surfaced. A dressed-up pill
// (indigo-tinted, tier icon) sitting beside the role rank in the navy bar.
function PlanBadge({ planName, unlimited }: { planName?: string; unlimited?: boolean }) {
  if (!planName) return null;
  // Match the icon to the tier: Lite → feather, Plus → plus, Infinity → ∞.
  const n = planName.toLowerCase();
  let Icon = Sparkles;
  if (unlimited || n.includes("infinity")) Icon = InfinityIcon;
  else if (n.includes("plus")) Icon = Plus;
  else if (n.includes("lite") || n.includes("feather")) Icon = Feather;
  return (
    <span className="hidden min-w-[86px] items-center justify-center gap-1 rounded-full border border-primary/40 bg-primary/25 px-2.5 py-0.5 text-[11px] font-semibold text-white shadow-sm sm:inline-flex">
      <Icon className="h-3.5 w-3.5" />
      {planName}
    </span>
  );
}

// RoleBadge shows the user's workspace rank with a pill. Styled for the navy
// chrome bar — translucent white so it reads on the dark background.
function RoleBadge({ role }: { role: string }) {
  return (
    <span className="inline-flex min-w-[64px] justify-center rounded-full bg-white/15 px-2 py-0.5 text-[11px] font-medium capitalize text-white">
      {role}
    </span>
  );
}
