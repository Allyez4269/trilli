import { type ComponentType, useState } from "react";
import { AlertTriangle, X } from "lucide-react";

import { ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";

// ConfirmModal is a lightweight yes/no confirmation for low-risk actions that do
// NOT require step-up 2FA (e.g. revoking or deleting a comp invite). It mirrors
// StepUpModal's chrome but collects no code — just a Cancel / Confirm choice.
export default function ConfirmModal({
  title,
  description,
  confirmLabel,
  destructive,
  icon: Icon = AlertTriangle,
  onConfirm,
  onClose,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  destructive?: boolean;
  icon?: ComponentType<{ className?: string }>;
  onConfirm: () => Promise<void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await onConfirm();
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/35 p-4 animate-fade-in">
      <div className="relative w-full max-w-sm rounded-2xl border border-border bg-card p-6 shadow-lg animate-scale-in">
        <button
          type="button"
          onClick={onClose}
          className="absolute right-3 top-3 inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          aria-label="Close"
        >
          <X className="h-4 w-4" />
        </button>
        <div className="mb-5 flex flex-col items-center text-center">
          <span
            className={`mb-3 flex h-11 w-11 items-center justify-center rounded-2xl ${
              destructive ? "bg-destructive/10 text-destructive" : "bg-primary/10 text-primary"
            }`}
          >
            <Icon className="h-5 w-5" />
          </span>
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
        </div>
        {error && (
          <p className="mb-3 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="button" variant="outline" onClick={onClose} disabled={busy} className="h-8 flex-1">
            Cancel
          </Button>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            onClick={run}
            disabled={busy}
            className="h-8 flex-1"
          >
            {busy ? "Working…" : confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
