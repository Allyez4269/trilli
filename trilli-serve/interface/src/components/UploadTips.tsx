import { useEffect, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Copy,
  Download,
  FolderInput,
  Lightbulb,
  Sparkles,
  Star,
  Upload,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

// UploadTips is a lively-but-calm carousel of teaching tips for the Files page.
// It auto-rotates (pausing on hover), can be paged manually, and is dismissible
// — but unlike a one-shot banner, dismissing collapses it to a small "Show
// tips" pill in the same spot so the tips are always one click away again.
const STORAGE_KEY = "trilli_tips_files_v1";

const readDismissed = () => {
  try {
    return localStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    return false;
  }
};

interface Tip {
  icon: LucideIcon;
  title: string;
  body: string;
  chip: string; // tint classes for the icon chip — a little color variety per tip
}

const TIPS: Tip[] = [
  {
    icon: Upload,
    title: "Drop to upload",
    body: "Drop files anywhere on this page — no need to aim for the box.",
    chip: "bg-indigo-500/10 text-indigo-600",
  },
  {
    icon: FolderInput,
    title: "Drag to move",
    body: "Drag a file or folder onto another folder — or a breadcrumb — to move it.",
    chip: "bg-amber-500/10 text-amber-600",
  },
  {
    icon: Copy,
    title: "Copy across accounts",
    body: "Drag a selection onto the account switcher up top to copy it into another account.",
    chip: "bg-violet-500/10 text-violet-600",
  },
  {
    icon: Star,
    title: "Star to pin",
    body: "Star important files and folders to keep them handy in your Starred view.",
    chip: "bg-yellow-500/10 text-yellow-600",
  },
  {
    icon: Download,
    title: "Drag out to download",
    body: "Drag a file straight out to your desktop to save it (Chromium browsers).",
    chip: "bg-emerald-500/10 text-emerald-600",
  },
];

export function UploadTips({ className }: { className?: string }) {
  const [dismissed, setDismissed] = useState(readDismissed);
  const [index, setIndex] = useState(0);
  const [paused, setPaused] = useState(false);

  // Auto-advance every 6s unless dismissed or the pointer is hovering (so a
  // reader isn't yanked to the next tip mid-sentence).
  useEffect(() => {
    if (dismissed || paused) return;
    const t = setInterval(() => setIndex((p) => (p + 1) % TIPS.length), 6000);
    return () => clearInterval(t);
  }, [dismissed, paused]);

  const dismiss = () => {
    try {
      localStorage.setItem(STORAGE_KEY, "1");
    } catch {
      /* private browsing — collapse for this view only */
    }
    setDismissed(true);
  };

  const restore = () => {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      /* ignore */
    }
    setDismissed(false);
  };

  const go = (next: number) => setIndex((next + TIPS.length) % TIPS.length);

  if (dismissed) {
    // Minimized: a small pill in the same spot — discoverable, one click back.
    return (
      <button
        type="button"
        onClick={restore}
        className={cn(
          "group inline-flex items-center gap-1.5 rounded-full border border-primary/20 bg-primary/[0.04] px-3 py-1 text-[12px] font-medium text-primary/80 transition-colors hover:bg-primary/10 hover:text-primary",
          className,
        )}
      >
        <Lightbulb className="h-3.5 w-3.5" />
        Show tips
      </button>
    );
  }

  const tip = TIPS[index];
  const Icon = tip.icon;

  return (
    <div
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      className={cn(
        "relative flex items-center gap-3 overflow-hidden rounded-xl border border-primary/15 bg-gradient-to-r from-primary/[0.07] via-primary/[0.04] to-transparent px-3.5 py-3 sm:px-4",
        className,
      )}
    >
      {/* Icon chip — re-keyed per tip so it gets a soft fade as tips rotate. */}
      <span
        key={`chip-${index}`}
        className={cn(
          "flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg animate-in fade-in zoom-in-95 duration-300",
          tip.chip,
        )}
      >
        <Icon className="h-[18px] w-[18px]" />
      </span>

      {/* Tip text — bigger + bolder than the old banner, fades on change. */}
      <div key={`txt-${index}`} className="min-w-0 flex-1 animate-in fade-in slide-in-from-right-2 duration-300">
        <p className="flex items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-wide text-primary/70">
          <Sparkles className="h-3 w-3" /> Tip
        </p>
        <p className="text-[14px] leading-snug text-foreground/90">
          <span className="font-semibold text-foreground">{tip.title}.</span> {tip.body}
        </p>
      </div>

      {/* Pager: prev · dots · next */}
      <div className="hidden flex-shrink-0 items-center gap-1.5 sm:flex">
        <button
          type="button"
          onClick={() => go(index - 1)}
          aria-label="Previous tip"
          className="rounded-md p-1 text-muted-foreground/70 transition-colors hover:bg-primary/10 hover:text-primary"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
        <div className="flex items-center gap-1">
          {TIPS.map((_, i) => (
            <button
              key={i}
              type="button"
              onClick={() => setIndex(i)}
              aria-label={`Go to tip ${i + 1}`}
              className={cn(
                "h-1.5 rounded-full transition-all",
                i === index ? "w-4 bg-primary" : "w-1.5 bg-primary/25 hover:bg-primary/50",
              )}
            />
          ))}
        </div>
        <button
          type="button"
          onClick={() => go(index + 1)}
          aria-label="Next tip"
          className="rounded-md p-1 text-muted-foreground/70 transition-colors hover:bg-primary/10 hover:text-primary"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>

      {/* Dismiss → collapses to the "Show tips" pill. */}
      <button
        type="button"
        onClick={dismiss}
        aria-label="Hide tips"
        title="Hide tips (you can bring them back)"
        className="flex-shrink-0 rounded-md p-1 text-muted-foreground/60 transition-colors hover:bg-primary/10 hover:text-foreground"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}
