import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { AlertTriangle, Loader2 } from "lucide-react";

import { cn } from "@/lib/utils";

// Shared overlay chrome for the app's modal dialogs. Click-outside and Escape
// both close (unless busy). Matches the move-progress / share modal styling.
function Overlay({ onClose, children }: { onClose: () => void; children: React.ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-2xl animate-in zoom-in-95 fade-in duration-200">
        {children}
      </div>
    </div>,
    document.body,
  );
}

// NameDialog collects a single name (new folder, rename). onSubmit may throw —
// its message is shown inline and the dialog stays open so the user can fix it.
export function NameDialog({
  title,
  label,
  initialValue = "",
  confirmLabel = "Save",
  placeholder,
  allowUnchanged = false,
  onSubmit,
  onClose,
}: {
  title: string;
  label: string;
  initialValue?: string;
  confirmLabel?: string;
  placeholder?: string;
  // When true, submitting the unchanged initial value still calls onSubmit
  // (rename treats no-change as a no-op; duplicate must proceed regardless).
  allowUnchanged?: boolean;
  onSubmit: (name: string) => Promise<void>;
  onClose: () => void;
}) {
  const [value, setValue] = useState(initialValue);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    // Focus + select the name so a rename is immediately editable.
    const t = setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 30);
    return () => clearTimeout(t);
  }, []);

  const submit = async () => {
    const name = value.trim();
    if (!name) {
      setError("Please enter a name.");
      return;
    }
    if (!allowUnchanged && name === initialValue.trim()) {
      onClose();
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await onSubmit(name);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
      setBusy(false);
    }
  };

  return (
    <Overlay onClose={() => !busy && onClose()}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <div className="px-5 pb-1 pt-4">
          <h2 className="text-sm font-semibold text-foreground">{title}</h2>
        </div>
        <div className="space-y-1.5 px-5 py-3">
          <label className="block text-[12px] font-medium text-foreground">{label}</label>
          <input
            ref={inputRef}
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              if (error) setError(null);
            }}
            placeholder={placeholder}
            className={cn(
              "h-9 w-full rounded-md border bg-background px-2.5 text-[13px] outline-none focus-visible:ring-1",
              error
                ? "border-destructive focus-visible:ring-destructive/40"
                : "border-border focus-visible:ring-primary/40",
            )}
          />
          {error && (
            <p className="flex items-center gap-1.5 pt-0.5 text-[12px] text-destructive">
              <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
              {error}
            </p>
          )}
        </div>
        <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground hover:bg-muted disabled:opacity-60"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy}
            className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
          >
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {confirmLabel}
          </button>
        </div>
      </form>
    </Overlay>
  );
}

// ConfirmDialog is a styled yes/no confirmation (e.g. move to trash). danger
// renders the confirm button in the destructive color.
export function ConfirmDialog({
  title,
  message,
  confirmLabel = "Confirm",
  danger = false,
  onConfirm,
  onClose,
}: {
  title: string;
  message: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  onConfirm: () => Promise<void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const confirm = async () => {
    setBusy(true);
    setError(null);
    try {
      await onConfirm();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
      setBusy(false);
    }
  };

  return (
    <Overlay onClose={() => !busy && onClose()}>
      <div className="px-5 pt-4">
        <div className="flex items-start gap-3">
          {danger && (
            <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-destructive/10 text-destructive">
              <AlertTriangle className="h-5 w-5" />
            </span>
          )}
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-foreground">{title}</h2>
            <div className="mt-1 text-[13px] text-muted-foreground">{message}</div>
          </div>
        </div>
        {error && (
          <p className="mt-2 text-[12px] text-destructive">{error}</p>
        )}
      </div>
      <div className="mt-4 flex justify-end gap-2 border-t border-border px-5 py-3">
        <button
          type="button"
          onClick={onClose}
          disabled={busy}
          className="h-8 rounded-md border border-border bg-background px-2.5 text-[12.5px] font-medium text-foreground hover:bg-muted disabled:opacity-60"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={() => void confirm()}
          disabled={busy}
          className={cn(
            "inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-[12.5px] font-semibold text-white disabled:opacity-60",
            danger ? "bg-destructive hover:bg-destructive/90" : "bg-primary hover:bg-primary/90",
          )}
        >
          {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {confirmLabel}
        </button>
      </div>
    </Overlay>
  );
}
