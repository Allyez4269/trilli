import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { FileWarning } from "lucide-react";

// MoveConflictModal is the friendly, Windows-style name-collision prompt shown
// when a moved file would clash with one that already exists at the
// destination. It explains the clash and offers a pre-filled, editable rename
// ("keep both") so nothing is silently overwritten or auto-renamed behind the
// user's back. When several files clash, it shows one at a time (position/total).
export default function MoveConflictModal({
  fileName,
  suggested,
  destLabel,
  position,
  total,
  actionLabel = "moving",
  onKeepBoth,
  onSkip,
}: {
  fileName: string;
  suggested: string;
  destLabel?: string;
  position: number; // 1-based index within the conflict queue
  total: number;
  actionLabel?: string; // "moving" | "uploading" — what the user is doing
  onKeepBoth: (name: string) => void;
  onSkip: () => void;
}) {
  const [name, setName] = useState(suggested);
  useEffect(() => setName(suggested), [suggested]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onSkip();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onSkip]);

  const trimmed = name.trim();
  const where = destLabel ? <>in <span className="font-medium text-foreground">{destLabel}</span></> : "here";

  return createPortal(
    <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150">
      <div className="w-full max-w-md overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200">
        {/* Header */}
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
          <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-amber-500/10 text-amber-600">
            <FileWarning className="h-[18px] w-[18px]" />
          </span>
          <div className="min-w-0 flex-1 leading-tight">
            <h2 className="text-sm font-semibold text-foreground">This file already exists</h2>
            <p className="text-[11px] text-muted-foreground">
              {total > 1 ? `Name conflict ${position} of ${total}` : "Name conflict"}
            </p>
          </div>
        </div>

        {/* Body */}
        <div className="space-y-4 px-5 py-4">
          <p className="text-[13px] leading-relaxed text-foreground/80">
            A file named <span className="font-medium text-foreground">“{fileName}”</span> already
            exists {where}. Keep both — the file you're {actionLabel} will be renamed:
          </p>
          <div>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && trimmed) onKeepBoth(trimmed);
              }}
              autoFocus
              aria-label="New name"
              className="h-10 w-full rounded-lg border border-border bg-background px-3 text-[14px] outline-none focus:border-primary/50"
            />
            <p className="mt-1.5 text-[11.5px] text-muted-foreground">
              Edit it to anything you like, or keep the suggested name.
            </p>
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
          <button
            type="button"
            onClick={onSkip}
            className="inline-flex h-9 items-center justify-center rounded-lg border border-border bg-card px-4 text-[13px] font-medium text-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => trimmed && onKeepBoth(trimmed)}
            disabled={!trimmed}
            className="inline-flex h-9 items-center justify-center rounded-lg bg-primary px-4 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            OK
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
