import { type ReactNode, useState } from "react";
import { Link, NavLink, useNavigate } from "react-router-dom";
import { ChevronLeft, ChevronRight, LogOut, Settings } from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { TrilliLogo } from "@/components/Logo";
import { NAV, NAV_SECTIONS, canSee, type NavItem } from "@/lib/nav";
import { cn } from "@/lib/utils";

const COLLAPSE_KEY = "cmx.sidebar.collapsed";

// The persistent operator console shell, built on the Trilli design system
// (DESIGN_SYSTEM.md §7–8): a navy `chrome` top bar, a collapsible left sidebar
// whose active state is a layered inset pill + 3px primary bar (never a full-row
// fill), and a floating white content card that punches out of the cool canvas.
// Recolored to the CMX brand (slate primary / navy chrome).
export default function AppShell({ children }: { children: ReactNode }) {
  const { operator, logout } = useAuth();
  const navigate = useNavigate();
  const role = operator?.role ?? "cmx";
  const year = new Date().getFullYear();

  const [collapsed, setCollapsed] = useState<boolean>(
    () => (typeof localStorage !== "undefined" && localStorage.getItem(COLLAPSE_KEY)) === "1",
  );

  function toggleCollapsed() {
    setCollapsed((c) => {
      const next = !c;
      try {
        localStorage.setItem(COLLAPSE_KEY, next ? "1" : "0");
      } catch {
        /* ignore */
      }
      return next;
    });
  }

  async function onLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  // Group role-visible nav items by section, preserving NAV_SECTIONS order and
  // dropping empty groups (a "cmx" operator sees no Platform items).
  const groups = NAV_SECTIONS.map((section) => ({
    section,
    items: NAV.filter((item) => item.section === section && canSee(item, role)),
  })).filter((g) => g.items.length > 0);

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background">
      {/* Navy chrome top bar (§7.2) */}
      <header className="flex h-14 shrink-0 items-center justify-between bg-chrome pl-4 pr-5 text-chrome-foreground">
        <Link to="/" className="flex flex-shrink-0 items-center">
          <TrilliLogo className="h-[28px] text-white" />
        </Link>
        <div className="flex items-center gap-3 text-sm">
          <span className="rounded-full bg-white/15 px-2 py-0.5 text-[11px] font-medium capitalize text-white">
            {role === "global" ? "Global admin" : "CMX admin"}
          </span>
          <span className="hidden text-white/70 sm:inline">{operator?.email}</span>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* Left sidebar (§8) */}
        <aside
          className={cn(
            "relative z-30 flex h-full shrink-0 flex-col bg-sidebar text-sidebar-foreground transition-[width] duration-300 ease-in-out",
            collapsed ? "w-[60px]" : "w-[228px]",
          )}
        >
          {/* Edge collapse toggle */}
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="absolute -right-3 top-5 z-50 hidden h-6 w-6 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-sm transition-colors hover:bg-muted hover:text-foreground md:flex"
          >
            {collapsed ? <ChevronRight className="h-3.5 w-3.5" /> : <ChevronLeft className="h-3.5 w-3.5" />}
          </button>

          <nav className="flex-1 space-y-1 overflow-y-auto p-3 pt-4">
            {groups.map((group, gi) => (
              <div key={group.section} className={cn("space-y-1", gi > 0 && "mt-6 border-t border-sidebar-border pt-4", gi > 0 && collapsed && "mx-1")}>
                {!collapsed && (
                  <span className="px-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                    {group.section}
                  </span>
                )}
                {group.items.map((item) => (
                  <NavRow key={item.to} item={item} collapsed={collapsed} />
                ))}
              </div>
            ))}
          </nav>

          {/* Account hub — Settings + Sign out in an elevated card (app §8.4).
              Collapsed: centered icons with a top divider. */}
          {collapsed ? (
            <div className="flex flex-col items-center gap-3 border-t border-sidebar-border p-3">
              <NavLink
                to="/settings"
                title="Settings"
                aria-label="Settings"
                className={({ isActive }) =>
                  cn(
                    "flex h-8 w-8 items-center justify-center rounded-lg transition-colors",
                    isActive
                      ? "text-primary"
                      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
                  )
                }
              >
                <Settings className="h-[18px] w-[18px]" />
              </NavLink>
              <button
                type="button"
                onClick={onLogout}
                title="Sign out"
                aria-label="Sign out"
                className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-sidebar-accent/60 hover:text-foreground"
              >
                <LogOut className="h-4 w-4" />
              </button>
            </div>
          ) : (
            <div className="p-3">
              <div className="overflow-hidden rounded-xl border border-border bg-card shadow-md ring-1 ring-black/[0.02]">
                <div className="space-y-1 p-2">
                  <NavLink
                    to="/settings"
                    className={({ isActive }) =>
                      cn(
                        "group relative flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] font-medium transition-colors",
                        isActive ? "text-sidebar-accent-foreground" : "text-foreground",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        {isActive ? (
                          <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted" />
                        ) : (
                          <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted opacity-0 transition-opacity group-hover:opacity-100" />
                        )}
                        <Settings className="relative h-[16px] w-[16px] flex-shrink-0 text-muted-foreground" />
                        <span className="relative flex-1 text-left">Settings</span>
                      </>
                    )}
                  </NavLink>
                  <button
                    type="button"
                    onClick={onLogout}
                    className="group relative flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] font-medium text-foreground transition-colors"
                  >
                    <span className="absolute inset-x-0 inset-y-[2.5px] rounded-lg bg-muted opacity-0 transition-opacity group-hover:opacity-100" />
                    <LogOut className="relative h-[16px] w-[16px] flex-shrink-0 text-muted-foreground" />
                    <span className="relative flex-1 text-left">Sign out</span>
                  </button>
                </div>
              </div>
            </div>
          )}
        </aside>

        {/* Floating content card (§7.1) */}
        <div className="flex flex-1 flex-col overflow-hidden">
          <main className="relative mb-2 mr-2 mt-2 flex flex-1 flex-col overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <div className="min-h-0 flex-1 overflow-y-auto p-6 lg:p-8">{children}</div>
            <footer className="flex shrink-0 items-center justify-center gap-1.5 bg-card/60 px-6 py-3 text-[11px] text-muted-foreground">
              © {year} Trilli Media LLC
              <span className="text-border">|</span>
              Operator console
            </footer>
          </main>
        </div>
      </div>
    </div>
  );
}

// NavRow renders one sidebar entry in either the expanded (label) or collapsed
// (icon-only) form. Active state = a `bg-muted` inset pill + a 3px primary left
// bar; idle hover fades the same pill in. Coming-soon entries are inert.
function NavRow({ item, collapsed }: { item: NavItem; collapsed: boolean }) {
  const Icon = item.icon;

  if (item.comingSoon) {
    if (collapsed) {
      return (
        <div
          title={`${item.label} — coming soon`}
          className="mx-auto flex h-9 w-9 cursor-not-allowed items-center justify-center rounded-lg text-muted-foreground/40"
        >
          <Icon className="h-[19px] w-[19px]" />
        </div>
      );
    }
    return (
      <div
        title="Coming soon"
        className="flex cursor-not-allowed items-center gap-[11px] rounded-lg px-[11px] py-[9px] text-sidebar-foreground/40"
      >
        <Icon className="h-[18px] w-[18px] flex-shrink-0" />
        <span className="flex-1 text-left text-[13px] font-medium">{item.label}</span>
        <span className="rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
          Soon
        </span>
      </div>
    );
  }

  if (collapsed) {
    return (
      <NavLink
        to={item.to}
        end={item.to === "/"}
        title={item.label}
        className={({ isActive }) =>
          cn(
            "group relative mx-auto flex h-9 w-9 items-center justify-center rounded-lg transition-colors",
            isActive ? "text-primary" : "text-muted-foreground hover:text-foreground",
          )
        }
      >
        {({ isActive }) => (
          <>
            {!isActive && (
              <span className="absolute inset-0 rounded-lg bg-muted opacity-0 transition-opacity group-hover:opacity-100" />
            )}
            {isActive && <span className="absolute inset-0 rounded-lg bg-muted" />}
            <Icon className={cn("relative h-[19px] w-[19px]", isActive && "stroke-[2.4]")} />
          </>
        )}
      </NavLink>
    );
  }

  return (
    <NavLink
      to={item.to}
      end={item.to === "/"}
      className={({ isActive }) =>
        cn(
          "group relative flex w-full items-center gap-[11px] rounded-lg px-[11px] py-[9px] transition-all duration-200",
          isActive ? "text-sidebar-accent-foreground" : "text-sidebar-foreground",
        )
      }
    >
      {({ isActive }) => (
        <>
          {!isActive && (
            <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted opacity-0 transition-opacity duration-200 group-hover:opacity-100" />
          )}
          {isActive && (
            <>
              <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted" />
              <span className="absolute inset-y-[4.5px] left-0 w-[3px] rounded-full bg-primary" />
            </>
          )}
          <Icon
            className={cn(
              "relative h-[18px] w-[18px] flex-shrink-0",
              isActive ? "text-primary" : "text-sidebar-foreground",
            )}
          />
          <span className="relative flex-1 text-left text-[13px] font-medium">{item.label}</span>
        </>
      )}
    </NavLink>
  );
}
