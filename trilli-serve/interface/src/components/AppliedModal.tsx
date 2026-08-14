import { useEffect } from "react";
import { createPortal } from "react-dom";
import { CheckCircle2 } from "lucide-react";

interface AppliedModalProps {
  open: boolean;
  title: string;
  message: string;
  onClose: () => void;
}

/** A small, pretty confirmation modal: green check, title, message, OK button. */
export default function AppliedModal({ open, title, message, onClose }: AppliedModalProps) {
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
    >
      <div className="w-full max-w-sm overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex flex-col items-center px-6 py-7 text-center">
          <span className="flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500/10">
            <CheckCircle2 className="h-7 w-7 text-emerald-600" />
          </span>
          <h2 className="mt-4 text-base font-semibold text-foreground">{title}</h2>
          <p className="mt-1 text-[13px] leading-relaxed text-muted-foreground">{message}</p>
          <button
            type="button"
            autoFocus
            onClick={onClose}
            className="mt-5 inline-flex h-9 w-full items-center justify-center rounded-md bg-primary px-4 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary/90"
          >
            OK
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
