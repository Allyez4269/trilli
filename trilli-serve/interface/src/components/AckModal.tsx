import { useEffect } from "react";
import { createPortal } from "react-dom";
import { CheckCircle2 } from "lucide-react";

// AckModal is a small, reusable acknowledgement dialog: a success check, a
// message, and a single OK button to dismiss. Used after a successful save
// across Settings (Workspace, Account, …) so every save lands the same way.
export function AckModal({
  open,
  title = "Changes saved",
  message,
  onClose,
}: {
  open: boolean;
  title?: string;
  message: string;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" || e.key === "Enter") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      role="alertdialog"
      aria-modal="true"
    >
      <div className="w-full max-w-sm overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex flex-col items-center gap-3 px-6 pb-5 pt-7 text-center">
          <div className="flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500/10">
            <CheckCircle2 className="h-6 w-6 text-emerald-600" />
          </div>
          <h2 className="text-sm font-semibold text-foreground">{title}</h2>
          <p className="text-xs text-muted-foreground">{message}</p>
        </div>
        <div className="flex justify-center border-t border-border px-4 py-3">
          <button
            type="button"
            autoFocus
            onClick={onClose}
            className="inline-flex h-7 min-w-[6rem] items-center justify-center rounded-md bg-primary px-4 text-[11px] font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90"
          >
            OK
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
