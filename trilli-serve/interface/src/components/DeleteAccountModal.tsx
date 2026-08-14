import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  AlertTriangle,
  CheckCircle2,
  Fingerprint,
  Info,
  Loader2,
  Trash2,
  X,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import OtpInput from "@/components/OtpInput";
import { passkeyStepUp, passkeyErrorMessage } from "@/lib/webauthn";
import { continueWithProvider } from "@/lib/googlePopup";

interface Preview {
  account_name: string;
  storage_bytes: number;
  member_count: number;
  refund_eligible: boolean;
  refund_cents: number;
  has_active_billing: boolean;
  grace_days: number;
}

interface DeleteResult {
  ok: boolean;
  purge_at: string;
  refund_cents: number;
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
}
const fmtMoney = (cents: number) => `$${(cents / 100).toFixed(2)}`;
const fmtDate = (iso: string) =>
  new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" });

// DeleteAccountModal runs the owner-only account-deletion flow: it shows the
// consequences + the billing outcome (3 states), then a step-up re-auth
// (password [+ TOTP code when 2FA is on], or a passkey), and on success a
// confirmation with the purge date. Deletion is recoverable for the grace
// window — the modal says so.
export default function DeleteAccountModal({
  open,
  onClose,
  onDeleted,
}: {
  open: boolean;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [totpEnabled, setTotpEnabled] = useState(false);
  const [hasPasskeys, setHasPasskeys] = useState(false);
  const [usePasskey, setUsePasskey] = useState(false);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [done, setDone] = useState<DeleteResult | null>(null);
  const [reauthBusy, setReauthBusy] = useState<"google" | "microsoft" | null>(null);

  useEffect(() => {
    if (!open) return;
    setPreview(null);
    setUsePasskey(false);
    setPassword("");
    setCode("");
    setBusy(false);
    setErr(null);
    setDone(null);
    api
      .get<Preview>("/api/account/delete/preview")
      .then(setPreview)
      .catch(() => setErr("Couldn't load account details. Please try again."));
    // Determine the available step-up factors (the same flags the security tab uses).
    api.get<{ enabled?: boolean }>("/api/me/2fa").then((r) => setTotpEnabled(!!r?.enabled)).catch(() => {});
    api.get<{ passkeys?: unknown[] } | unknown[]>("/api/me/passkeys")
      .then((r) => {
        const list = Array.isArray(r) ? r : (r as { passkeys?: unknown[] })?.passkeys;
        setHasPasskeys(Array.isArray(list) && list.length > 0);
      })
      .catch(() => {});
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !busy && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      let res: DeleteResult;
      if (usePasskey) {
        res = await passkeyStepUp<DeleteResult>(
          "/api/account/delete/passkey/begin",
          "/api/account/delete",
        );
      } else {
        res = await api.post<DeleteResult>("/api/account/delete", {
          password,
          code: code.replace(/\s/g, ""),
        });
      }
      setDone(res);
    } catch (e) {
      setErr(
        usePasskey
          ? passkeyErrorMessage(e, "Couldn't verify your passkey. Try again.")
          : e instanceof ApiError
            ? e.message
            : "Something went wrong. Try again.",
      );
    } finally {
      setBusy(false);
    }
  };

  // OAuth re-auth: confirm via Google/Microsoft (for accounts with no usable
  // password — e.g. OAuth-only sign-ups — or as an alternative). The popup
  // verifies the provider email matches the account and returns a step-up token.
  const reauthVia = (provider: "google" | "microsoft") => {
    if (busy || reauthBusy) return;
    setErr(null);
    setReauthBusy(provider);
    continueWithProvider(provider, {
      mode: "reauth",
      onReauth: async (token) => {
        setReauthBusy(null);
        if (!token) {
          setErr("Re-authentication didn't complete. Try again.");
          return;
        }
        setBusy(true);
        try {
          const res = await api.post<DeleteResult>("/api/account/delete", { reauth_token: token });
          setDone(res);
        } catch (e) {
          setErr(e instanceof ApiError ? e.message : "Something went wrong. Try again.");
        } finally {
          setBusy(false);
        }
      },
      onError: (msg) => {
        setReauthBusy(null);
        setErr(msg);
      },
      onClosed: () => setReauthBusy(null),
    });
  };

  const canSubmit = usePasskey
    ? true
    : password.length > 0 && (!totpEnabled || code.replace(/\s/g, "").length >= 6);

  // Billing notice copy, from the preview.
  const billingNotice = (() => {
    if (!preview) return null;
    if (preview.refund_eligible && preview.refund_cents > 0) {
      return {
        tone: "good" as const,
        text: `Created less than 30 days ago — you'll get a full refund of ${fmtMoney(preview.refund_cents)} now.`,
      };
    }
    if (preview.has_active_billing) {
      return {
        tone: "muted" as const,
        text: "Your plan will be cancelled at the end of the current billing period. No refund for the remaining term.",
      };
    }
    return { tone: "muted" as const, text: "No active billing on this account." };
  })();

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}
    >
      <div className="flex max-h-[88vh] w-full max-w-md flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200">
        {/* Header */}
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
          <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-destructive/10 text-destructive">
            <Trash2 className="h-[18px] w-[18px]" />
          </span>
          <div className="min-w-0 flex-1 leading-tight">
            <h2 className="text-sm font-semibold text-foreground">Delete account</h2>
            <p className="truncate text-[11px] text-muted-foreground">
              {done ? "Scheduled for deletion" : preview?.account_name ?? "…"}
            </p>
          </div>
          <button
            type="button"
            onClick={() => !busy && onClose()}
            disabled={busy}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-40"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {done ? (
          <div className="flex flex-col gap-3 px-5 py-6">
            <div className="flex items-start gap-3">
              <CheckCircle2 className="mt-0.5 h-5 w-5 flex-shrink-0 text-emerald-600" />
              <div>
                <p className="text-[14px] font-semibold text-foreground">
                  Account scheduled for deletion
                </p>
                <p className="mt-0.5 text-[12.5px] text-muted-foreground">
                  It's now read-only and will be permanently deleted on{" "}
                  <span className="font-medium text-foreground">{fmtDate(done.purge_at)}</span>.
                  {done.refund_cents > 0 && (
                    <> A refund of {fmtMoney(done.refund_cents)} has been issued.</>
                  )}{" "}
                  You can reactivate any time before then.
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={() => {
                onDeleted();
              }}
              className="mt-1 h-10 w-full rounded-lg bg-primary text-[13.5px] font-semibold text-primary-foreground hover:bg-primary/90"
            >
              Done
            </button>
          </div>
        ) : (
          <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4">
            {/* Consequences */}
            <div className="flex items-start gap-2.5 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2.5">
              <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-destructive" />
              <p className="text-[12.5px] leading-relaxed text-foreground/80">
                This permanently deletes{" "}
                <span className="font-medium text-foreground">
                  {preview ? fmtBytes(preview.storage_bytes) : "…"}
                </span>{" "}
                of files, all folders and workspaces
                {preview && preview.member_count > 1 && (
                  <>
                    , and removes access for{" "}
                    <span className="font-medium text-foreground">
                      {preview.member_count} members
                    </span>
                  </>
                )}
                . Your account stays read-only and recoverable for{" "}
                <span className="font-medium text-foreground">{preview?.grace_days ?? 30} days</span>
                , then everything is erased for good.
              </p>
            </div>

            {/* Billing notice */}
            {billingNotice && (
              <div
                className={
                  billingNotice.tone === "good"
                    ? "flex items-start gap-2 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-3 py-2.5 text-[12px] text-emerald-700"
                    : "flex items-start gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2.5 text-[12px] text-muted-foreground"
                }
              >
                <Info className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
                <span>{billingNotice.text}</span>
              </div>
            )}

            {/* Step-up */}
            <div className="space-y-2.5 border-t border-border pt-3.5">
              <p className="text-[12px] font-medium text-foreground">Confirm it's you</p>
              {usePasskey ? (
                <p className="text-[12px] text-muted-foreground">
                  You'll verify with your passkey when you click delete.
                </p>
              ) : (
                <>
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="Your password"
                    autoComplete="current-password"
                    autoFocus
                    className="h-10 w-full rounded-lg border border-border bg-background px-3 text-[14px] outline-none focus:border-primary/50"
                  />
                  {totpEnabled && (
                    <div className="space-y-1.5">
                      <p className="text-[11.5px] text-muted-foreground">
                        Enter the 6-digit code from your authenticator app.
                      </p>
                      <OtpInput value={code} onChange={setCode} disabled={busy} />
                    </div>
                  )}
                </>
              )}
              {hasPasskeys && (
                <button
                  type="button"
                  onClick={() => {
                    setUsePasskey((v) => !v);
                    setErr(null);
                  }}
                  className="inline-flex items-center gap-1.5 text-[12px] font-medium text-primary hover:underline"
                >
                  <Fingerprint className="h-3.5 w-3.5" />
                  {usePasskey ? "Use password instead" : "Use a passkey instead"}
                </button>
              )}

              {/* OAuth re-auth — for accounts signed up with Google/Microsoft
                  (which have no password the user knows), or as an alternative. */}
              <div className="flex items-center gap-2 pt-1 text-[11px] text-muted-foreground">
                <span className="h-px flex-1 bg-border" />
                or confirm with
                <span className="h-px flex-1 bg-border" />
              </div>
              <div className="flex gap-2">
                {(["google", "microsoft"] as const).map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => reauthVia(p)}
                    disabled={busy || reauthBusy !== null}
                    className="inline-flex h-9 flex-1 items-center justify-center gap-1.5 rounded-lg border border-border bg-card text-[12.5px] font-medium text-foreground hover:bg-muted disabled:opacity-50"
                  >
                    {reauthBusy === p ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : null}
                    {p === "google" ? "Google" : "Microsoft"}
                  </button>
                ))}
              </div>
            </div>

            {err && (
              <p className="text-[12px] text-destructive" role="alert">
                {err}
              </p>
            )}

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={onClose}
                disabled={busy}
                className="h-9 rounded-lg border border-border bg-card px-3.5 text-[13px] font-medium text-foreground hover:bg-muted disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void submit()}
                disabled={busy || !canSubmit || !preview}
                className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-destructive px-3.5 text-[13px] font-semibold text-white hover:bg-destructive/90 disabled:opacity-50"
              >
                {busy ? (
                  <>
                    <Loader2 className="h-4 w-4 animate-spin" />
                    {usePasskey ? "Waiting for passkey…" : "Deleting…"}
                  </>
                ) : (
                  <>
                    <Trash2 className="h-4 w-4" /> Delete account
                  </>
                )}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
