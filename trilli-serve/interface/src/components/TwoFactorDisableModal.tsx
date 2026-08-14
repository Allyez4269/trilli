import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Loader2, ShieldOff, X } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import OtpInput from "@/components/OtpInput";

interface TwoFactorDisableModalProps {
  open: boolean;
  onClose: () => void;
  /** Fired after 2FA is successfully turned off. */
  onDisabled: () => void;
}

/**
 * Step-up confirmation before turning 2FA off. The user must prove they still
 * control the second factor by entering a current authenticator code (or a
 * recovery code) — so an unlocked-but-hijacked session can't silently disable
 * it. DELETE /api/me/2fa/totp now verifies this code server-side.
 */
export default function TwoFactorDisableModal({ open, onClose, onDisabled }: TwoFactorDisableModalProps) {
  const [code, setCode] = useState("");
  const [useRecovery, setUseRecovery] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setCode("");
      setUseRecovery(false);
      setBusy(false);
      setErr(null);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  async function disable(submitCode: string) {
    const clean = submitCode.replace(/\s/g, "");
    if (!clean || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.delete("/api/me/2fa/totp", { code: clean });
      onDisabled();
      onClose();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Couldn't turn off two-factor. Try again.");
      setBusy(false);
    }
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
          <div className="flex items-center gap-2.5">
            <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-destructive/10">
              <ShieldOff className="h-[18px] w-[18px] text-destructive" />
            </span>
            <div className="leading-tight">
              <h2 className="text-sm font-semibold text-foreground">Turn off two-factor authentication</h2>
              <p className="text-[11px] text-muted-foreground">Confirm it's you</p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => !busy && onClose()}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div className="space-y-4 px-6 py-5">
          <p className="text-center text-[12px] text-muted-foreground">
            This removes the extra sign-in step and your recovery codes. Enter a code from your
            authenticator app to confirm.
          </p>

          {useRecovery ? (
            <input
              autoFocus
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="abcd-efgh"
              className="h-11 w-full rounded-md border border-foreground/25 bg-background text-center font-mono tracking-widest outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
            />
          ) : (
            <OtpInput value={code} onChange={setCode} autoFocus disabled={busy} onComplete={disable} />
          )}

          {err && (
            <p className="text-center text-[11px] text-destructive" role="alert">
              {err}
            </p>
          )}

          <button
            type="button"
            onClick={() => {
              setUseRecovery((v) => !v);
              setCode("");
              setErr(null);
            }}
            className="mx-auto block text-[11px] text-brand-purple hover:underline"
          >
            {useRecovery ? "Use authenticator code" : "Use a recovery code instead"}
          </button>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-2.5">
          <button
            type="button"
            onClick={() => !busy && onClose()}
            disabled={busy}
            className="inline-flex h-7 items-center rounded-md border border-input bg-background px-3 text-[12.5px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void disable(code)}
            disabled={busy || (useRecovery ? code.trim().length < 4 : code.length < 6)}
            className="inline-flex h-7 items-center gap-1.5 rounded-md bg-destructive px-3 text-[12.5px] font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 disabled:opacity-50"
          >
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {busy ? "Turning off…" : "Turn off"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
