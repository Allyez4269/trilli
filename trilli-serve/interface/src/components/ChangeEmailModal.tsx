import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Loader2, Lock, Mail, MailCheck, X } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

// ChangeEmailModal — the verified "change your email" flow: enter the new
// address, confirm with your password, and we email a confirmation link to the
// new address. The change only takes effect once that link is confirmed, so the
// current email stays active until then.

const inputCls = "h-7 border-foreground/25 bg-background text-sm";
const emailRE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

export function ChangeEmailModal({
  open,
  currentEmail,
  onClose,
}: {
  open: boolean;
  currentEmail: string;
  onClose: () => void;
}) {
  const [step, setStep] = useState<"form" | "sent">("form");
  const [newEmail, setNewEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Reset to a clean form whenever the modal is reopened.
  useEffect(() => {
    if (open) {
      setStep("form");
      setNewEmail("");
      setPassword("");
      setBusy(false);
      setError(null);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const emailValid = emailRE.test(newEmail.trim());
  const sameAsCurrent =
    newEmail.trim().toLowerCase() === currentEmail.trim().toLowerCase();
  const canSubmit = emailValid && !sameAsCurrent && password.length > 0 && !busy;

  async function submit() {
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/me/email-change", {
        new_email: newEmail.trim(),
        password,
      });
      setStep("sent");
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Something went wrong. Please try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-foreground">
            {step === "form" ? "Change email" : "Verify your new email"}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {step === "form" ? (
          <>
            <div className="space-y-4 p-4">
              <p className="text-xs text-muted-foreground">
                Enter the new email address for your account. We'll send a verification link
                there to confirm it's yours — your current email stays active until you confirm.
              </p>

              {/* Current email, read-only context */}
              <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2">
                <Mail className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  <p className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    Current email
                  </p>
                  <p className="truncate text-xs font-medium text-foreground">{currentEmail}</p>
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="new-email" className="text-xs font-medium text-foreground">
                  New email address
                </Label>
                <Input
                  id="new-email"
                  type="email"
                  autoComplete="email"
                  placeholder="Enter new email address"
                  value={newEmail}
                  onChange={(e) => setNewEmail(e.target.value)}
                  className={inputCls}
                />
                {newEmail.length > 0 && !emailValid && (
                  <p className="text-[11px] text-destructive">Enter a valid email address.</p>
                )}
                {emailValid && sameAsCurrent && (
                  <p className="text-[11px] text-destructive">
                    That's already your current email.
                  </p>
                )}
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="confirm-pw" className="text-xs font-medium text-foreground">
                  Confirm with your password
                </Label>
                <div className="relative">
                  <Lock className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    id="confirm-pw"
                    type="password"
                    autoComplete="current-password"
                    placeholder="Your current password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    className={cn(inputCls, "pl-9")}
                  />
                </div>
                <p className="text-[11px] text-muted-foreground">
                  For your security, confirm it's really you making this change.
                </p>
              </div>

              {error && (
                <p className="text-[11px] text-destructive" role="alert">
                  {error}
                </p>
              )}
            </div>

            {/* Footer */}
            <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
              <button
                type="button"
                onClick={onClose}
                disabled={busy}
                className="inline-flex h-7 min-w-[5.5rem] items-center justify-center rounded-md border border-foreground/30 bg-background px-3 text-[11px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={submit}
                disabled={!canSubmit}
                className="inline-flex h-7 items-center justify-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
              >
                {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {busy ? "Sending…" : "Send verification link"}
              </button>
            </div>
          </>
        ) : (
          <>
            {/* Sent confirmation */}
            <div className="space-y-4 p-5">
              <div className="flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
                <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-emerald-500/10">
                  <MailCheck className="h-5 w-5 text-emerald-600" />
                </div>
                <div className="min-w-0">
                  <p className="text-sm font-medium text-foreground">Verification link sent</p>
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    We've sent a verification link to{" "}
                    <span className="font-medium text-foreground">{newEmail.trim()}</span>. Open it
                    to confirm your new email address.
                  </p>
                </div>
              </div>
              <p className="text-[11px] text-muted-foreground">
                Until you confirm, you'll keep signing in with{" "}
                <span className="font-medium text-foreground">{currentEmail}</span>. The link
                expires in 30 minutes — didn't get it? Check spam or request a new one.
              </p>
            </div>

            <div className="flex items-center justify-end border-t border-border px-4 py-3">
              <button
                type="button"
                onClick={onClose}
                className="inline-flex h-7 min-w-[5.5rem] items-center justify-center rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90"
              >
                Done
              </button>
            </div>
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}
