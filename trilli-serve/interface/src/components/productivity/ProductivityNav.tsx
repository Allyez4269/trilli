// The Apps → Productivity entry for the sidebar. Expanded sidebar: an inline
// accordion — clicking "Productivity" expands the three apps as a tree BELOW it;
// navigating to a non-Productivity item collapses it again. Collapsed rail: a
// single icon with a hover flyout (a tree doesn't fit a narrow rail).
import { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { ChevronRight, Rocket } from "lucide-react";

import { PRODUCTIVITY_APPS } from "@/lib/productivity/apps";
import { isAppEnabled } from "@/lib/productivity/access";
import { useAuth } from "@/contexts/AuthContext";
import { cn } from "@/lib/utils";

const PRODUCTIVITY_PREFIX = "/apps/productivity";

// Collapsed-rail flyout (hover) — keeps quick access to the three apps when the
// sidebar is a narrow icon rail.
function Flyout({ email }: { email: string | null | undefined }) {
  return (
    <div className="invisible absolute left-full top-0 z-50 -ml-1 pl-2 opacity-0 transition-opacity duration-150 group-hover/prod:visible group-hover/prod:opacity-100">
      <div className="w-60 rounded-xl border border-border bg-card p-1.5 shadow-xl ring-1 ring-black/[0.03]">
        <div className="px-2 py-1 text-[10.5px] font-semibold uppercase tracking-wide text-muted-foreground">
          Productivity
        </div>
        {PRODUCTIVITY_APPS.map((a) => {
          const enabled = isAppEnabled(a.key, email);
          if (!enabled) {
            return (
              <div
                key={a.key}
                className="flex items-center gap-2.5 rounded-lg px-2 py-2 opacity-50"
              >
                <a.Icon className="h-7 w-7 flex-shrink-0 grayscale" />
                <span className="min-w-0 flex-1">
                  <span className="block text-[13px] font-medium text-muted-foreground">{a.name}</span>
                  <span className="block text-[11px] text-muted-foreground">{a.blurb}</span>
                </span>
                <span className="rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
                  Soon
                </span>
              </div>
            );
          }
          return (
            <NavLink
              key={a.key}
              to={`${PRODUCTIVITY_PREFIX}/${a.key}`}
              className="flex items-center gap-2.5 rounded-lg px-2 py-2 transition-colors hover:bg-muted"
            >
              <a.Icon className="h-7 w-7 flex-shrink-0" />
              <span className="min-w-0">
                <span className="block text-[13px] font-medium text-foreground">{a.name}</span>
                <span className="block text-[11px] text-muted-foreground">{a.blurb}</span>
              </span>
            </NavLink>
          );
        })}
      </div>
    </div>
  );
}

export function ProductivityNav({ collapsed }: { collapsed: boolean }) {
  const location = useLocation();
  const { identity } = useAuth();
  const email = identity?.user?.email;
  const onRoute = location.pathname.startsWith(PRODUCTIVITY_PREFIX);
  // Open while on a Productivity route; otherwise togglable by clicking the row.
  // Navigating to ANY non-Productivity route (Files, Home, etc.) flips onRoute
  // false → auto-collapse. The effect re-runs on every location change so the
  // submenu always reflects the current route, never lingering expanded after
  // the user leaves Productivity.
  const [open, setOpen] = useState(onRoute);
  useEffect(() => setOpen(onRoute), [onRoute]);

  if (collapsed) {
    return (
      <div className="group/prod relative">
        <NavLink
          to={PRODUCTIVITY_PREFIX}
          title="Productivity"
          className={({ isActive }) =>
            cn(
              "group relative mx-auto flex h-9 w-9 items-center justify-center rounded-lg transition-colors",
              isActive ? "text-primary" : "text-muted-foreground hover:text-foreground",
            )
          }
        >
          <Rocket className="h-[19px] w-[19px]" />
        </NavLink>
        <Flyout email={email} />
      </div>
    );
  }

  return (
    <div>
      {/* Parent row — toggles the tree open/closed. */}
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "group relative flex w-full items-center gap-[11px] rounded-lg px-[11px] py-[9px] transition-all duration-200 [@media(max-height:1100px)]:py-[5px]",
          onRoute ? "text-sidebar-accent-foreground" : "text-sidebar-foreground",
        )}
      >
        {/* Selected-state treatment is identical to the other nav items:
            inset muted pill + amber/indigo left accent bar. */}
        {!onRoute && (
          <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted opacity-0 transition-opacity duration-200 group-hover:opacity-100" />
        )}
        {onRoute && (
          <>
            <span className="absolute inset-x-0 inset-y-[4.5px] rounded-lg bg-muted" />
            <span className="absolute inset-y-[4.5px] left-0 w-[3px] rounded-full bg-primary" />
          </>
        )}
        <Rocket
          className={cn("relative h-[18px] w-[18px] flex-shrink-0", onRoute ? "text-primary" : "text-black")}
        />
        <span className="relative flex-1 text-left text-[13px] font-medium">Productivity</span>
        <ChevronRight
          className={cn("relative h-3.5 w-3.5 text-muted-foreground transition-transform", open && "rotate-90")}
        />
      </button>

      {/* Tree of apps below the parent — slides open/closed via a grid-rows
          transition (0fr → 1fr) for a smooth, high-frame-rate accordion. */}
      <div
        className={cn(
          "grid transition-[grid-template-rows] duration-300 ease-out",
          open ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
        )}
      >
        <div className="overflow-hidden">
          <div className="mt-0.5 ml-[19px] space-y-0.5 border-l border-sidebar-border pl-2">
            {PRODUCTIVITY_APPS.map((a) => {
              const available = isAppEnabled(a.key, email);
              return available ? (
                <NavLink
                  key={a.key}
                  to={`${PRODUCTIVITY_PREFIX}/${a.key}`}
                  className={({ isActive }) =>
                    cn(
                      "group relative flex items-center gap-2 rounded-md px-2 py-1.5 transition-colors [@media(max-height:1100px)]:py-1",
                      isActive ? "bg-muted" : "hover:bg-muted",
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      <a.Icon className="h-[18px] w-[18px] flex-shrink-0" />
                      <span
                        className={cn(
                          "flex-1 truncate text-[12.5px] font-medium",
                          isActive ? "text-primary" : "text-sidebar-foreground",
                        )}
                      >
                        {a.name}
                      </span>
                    </>
                  )}
                </NavLink>
              ) : (
                <div
                  key={a.key}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 opacity-50 [@media(max-height:1100px)]:py-1"
                >
                  <a.Icon className="h-[18px] w-[18px] flex-shrink-0 grayscale" />
                  <span className="flex-1 truncate text-[12.5px] font-medium text-muted-foreground">
                    {a.name}
                  </span>
                  <span className="rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
                    Soon
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
