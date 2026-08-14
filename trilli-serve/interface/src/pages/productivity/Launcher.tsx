// Trilli Productivity launcher — the landing page when a user opens
// Apps → Productivity without a specific document. Which tiles are live is
// decided by the single per-app rollout gate (isAppEnabled); everything else is
// greyed out with a "Soon" tag.
import { useNavigate } from "react-router-dom";

import { PRODUCTIVITY_APPS } from "@/lib/productivity/apps";
import { isAppEnabled } from "@/lib/productivity/access";
import { useAuth } from "@/contexts/AuthContext";

export default function ProductivityLauncher() {
  const navigate = useNavigate();
  const { identity } = useAuth();
  const email = identity?.user?.email;
  return (
    <div className="mx-auto max-w-5xl px-6 py-8 lg:px-8 [@media(max-height:1100px)]:py-4">
      <header className="mb-6 [@media(max-height:1100px)]:mb-4">
        <h1 className="text-xl font-semibold text-foreground">Trilli Productivity</h1>
        <p className="mt-1 text-[13px] text-muted-foreground">
          Create and edit documents right inside Trilli. More apps coming soon.
        </p>
      </header>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        {PRODUCTIVITY_APPS.map((a) => {
          const available = isAppEnabled(a.key, email);
          return (
            <button
              key={a.key}
              type="button"
              disabled={!available}
              onClick={() => available && navigate(`/apps/productivity/${a.key}`)}
              className={
                available
                  ? "group flex flex-col items-start gap-3 rounded-xl border border-border bg-card p-5 text-left shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-md"
                  : "flex flex-col items-start gap-3 rounded-xl border border-border bg-muted/30 p-5 text-left shadow-sm"
              }
            >
              <span
                className={
                  available
                    ? "flex h-12 w-12 items-center justify-center rounded-xl ring-1 ring-inset ring-black/[0.04]"
                    : "flex h-12 w-12 items-center justify-center rounded-xl ring-1 ring-inset ring-black/[0.04] opacity-40"
                }
                style={{ backgroundColor: `${a.color}14` }}
              >
                <a.Icon className={available ? "h-8 w-8" : "h-8 w-8 grayscale"} />
              </span>
              <span>
                <span
                  className={
                    available
                      ? "block text-[15px] font-semibold text-foreground"
                      : "block text-[15px] font-semibold text-muted-foreground"
                  }
                >
                  {a.name}
                </span>
                <span className="block text-[12.5px] text-muted-foreground">{a.blurb}</span>
              </span>
              {available ? (
                <span className="mt-1 text-[12px] font-medium" style={{ color: a.color }}>
                  Open →
                </span>
              ) : (
                <span className="mt-1 rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
                  Soon
                </span>
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}
