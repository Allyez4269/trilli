import { useState } from "react";
import { Lightbulb, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

// InfoBanner is a subtle, brand-tinted instruction strip. Used to teach
// users about non-obvious interactions (drag to move, star to pin, etc.)
// without shouting. Visual recipe:
//   - bg-primary/[0.04]   — barely-there primary wash, sits softly above
//                           the bg-background but doesn't compete with
//                           card surfaces (bg-card).
//   - border-primary/15   — just enough edge to define the box.
//   - Icon at primary/70  — small accent, not loud.
//   - text-foreground/80  — readable but quieter than headlines.
//
// Pass `storageKey` to make the tip dismissible: an X appears and the
// dismissal is remembered per device (localStorage), so training-wheel
// copy doesn't cost a row on every visit forever.
const isDismissed = (key: string) => {
  try {
    return localStorage.getItem(`trilli_tip_${key}`) === "1";
  } catch {
    return false;
  }
};

export function InfoBanner({
  icon: Icon = Lightbulb,
  children,
  className,
  storageKey,
}: {
  icon?: LucideIcon;
  children: React.ReactNode;
  className?: string;
  storageKey?: string;
}) {
  const [hidden, setHidden] = useState(() => (storageKey ? isDismissed(storageKey) : false));
  if (hidden) return null;

  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-lg border border-primary/15 bg-primary/[0.04] px-4 py-2.5 text-xs leading-relaxed text-foreground/80",
        className,
      )}
    >
      <Icon className="mt-0.5 h-4 w-4 flex-shrink-0 text-primary/70" />
      <div className="min-w-0 flex-1">{children}</div>
      {storageKey && (
        <button
          type="button"
          aria-label="Dismiss tip"
          title="Dismiss"
          onClick={() => {
            try {
              localStorage.setItem(`trilli_tip_${storageKey}`, "1");
            } catch {
              /* private browsing — dismiss for this view only */
            }
            setHidden(true);
          }}
          className="-mr-1 -mt-0.5 rounded p-1 text-muted-foreground/60 transition-colors hover:bg-primary/10 hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}
