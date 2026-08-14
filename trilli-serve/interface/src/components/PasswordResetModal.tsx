import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Fingerprint, KeyRound, Loader2, MailCheck, X } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import OtpInput from "@/components/OtpInput";
import { passkeyStepUp, passkeyErrorMessage } from "@/lib/webauthn";

interface PasswordResetModalProps {
  open: boolean;
  onClose: () => void;
  /** Whether the account has an authenticator app (2FA) — takes precedence. */
  totpEnabled: boolean;
  /** Whether the account has at least one passkey (the fallback factor). */
  hasPasskeys: boolean;
  accountEmail: string;
}

type Result = { ok: boolean; email: string };

/**
 * Step-up gate before emailing a password-reset link. Mirrors the server's
 * precedence: a 2FA code if the account has one, else a passkey, else nothing.
 * On success it shows a "check your email" confirmation.
 */
export default function PasswordResetModal({
  open,
  onClose,
  totpEnabled,
  hasPasskeys,
  accountEmail,
}: PasswordResetModalProps) {
  const method: "totp" | "passkey" | "none" = totpEnabled
    ? "totp"
    : hasPasskeys
      ? "passkey"
      : "none";

  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [sentTo, setSentTo] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setCode("");
      setBusy(false);
      setErr(null);
      setSentTo(null);
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

  async function submit() {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      let res: Result;
      if (method === "passkey") {
        res = await passkeyStepUp<Result>(
          "/api/me/password-reset/passkey/begin",
          "/api/me/password-reset/request",
        );
      } else {
        // totp sends the code; none sends an empty body.
        res = await api.post<Result>(
          "/api/me/password-reset/request",
          method === "totp" ? { code: code.replace(/\s/g, "") } : {},
        );
      }
      setSentTo(res.email || accountEmail);
    } catch (e) {
      setErr(
        method === "passkey"
          ? passkeyErrorMessage(e, "Couldn't verify your passkey. Try again.")
          : e instanceof ApiError
            ? e.message
            : "Something went wrong. Try again.",
      );
    } finally {
      // Always clear busy — otherwise the header X (gated on !busy) stays inert
      // after a successful send.
      setBusy(false);
    }
  }

  const canSubmit = method === "totp" ? code.length >= 6 : true;

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
            <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10">
              <KeyRound className="h-[18px] w-[18px] text-primary" />
            </span>
            <div className="leading-tight">
              <h2 className="text-sm font-semibold text-foreground">Reset your password</h2>
              <p className="text-[11px] text-muted-foreground">
                {sentTo ? "Check your inbox" : "Confirm it's you"}
              </p>
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
          {sentTo ? (
            <div className="space-y-4">
              <div className="flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
                <MailCheck className="mt-0.5 h-5 w-5 flex-shrink-0 text-emerald-600" />
                <p className="text-xs text-muted-foreground">
                  We sent a reset link to{" "}
                  <span className="font-medium text-foreground">{sentTo}</span>. Open it to set a new
                  password — the link expires in 30 minutes.
                </p>
              </div>
              <button
                type="button"
                onClick={onClose}
                className="inline-flex h-7 w-full items-center justify-center rounded-md bg-primary px-4 text-[12.5px] font-medium text-primary-foreground transition-colors hover:bg-primary/90"
              >
                Ok
              </button>
            </div>
          ) : (
            <>
              {method === "totp" && (
                <>
                  <p className="text-center text-[12px] text-muted-foreground">
                    Enter the 6-digit code from your authenticator app to confirm.
                  </p>
                  <OtpInput value={code} onChange={setCode} autoFocus disabled={busy} />
                </>
              )}
              {method === "passkey" && (
                <p className="text-center text-[12px] text-muted-foreground">
                  Verify with your passkey to confirm it's you, then we'll email a reset link.
                </p>
              )}
              {method === "none" && (
                <p className="text-center text-[12px] text-muted-foreground">
                  We'll email a reset link to{" "}
                  <span className="font-medium text-foreground">{accountEmail}</span>. Open it to set
                  a new password.
                </p>
              )}

              {err && (
                <p className="text-center text-[11px] text-destructive" role="alert">
                  {err}
                </p>
              )}

              <button
                type="button"
                onClick={() => void submit()}
                disabled={busy || !canSubmit}
                className="inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-primary px-4 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
              >
                {busy && <Loader2 className="h-4 w-4 animate-spin" />}
                {method === "passkey" && !busy && <Fingerprint className="h-4 w-4" />}
                {busy
                  ? method === "passkey"
                    ? "Waiting for passkey…"
                    : "Sending…"
                  : method === "passkey"
                    ? "Verify & send reset link"
                    : "Send reset link"}
              </button>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
