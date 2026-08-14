import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Download, KeyRound, Loader2, MonitorSmartphone, ShieldCheck, X } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import googleAuthIcon from "@/assets/google-authenticator.svg";
import { checkboxCls, cn } from "@/lib/utils";
import OtpInput from "@/components/OtpInput";

// Authy brand mark (Simple Icons) for the "works with apps you trust" tiles.
function AuthyLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" role="img" aria-label="Authy" fill="#EC1C24" className={className}>
      <path d="M12 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0zm3.42 5.338c.274 0 .551.105.769.315l2.862 2.862c2.054 2.039 2.084 5.35.105 7.449a.21.21 0 0 1-.045.06l-.03.03-.03.03c-.015.015-.045.03-.06.045-2.098 1.978-5.41 1.948-7.463-.105l-2.863-2.863a1.05 1.05 0 0 1 0-1.499 1.05 1.05 0 0 1 1.5 0l2.861 2.863a3.23 3.23 0 0 0 4.542.03 3.244 3.244 0 0 0-.03-4.541l-2.863-2.862a1.05 1.05 0 0 1 0-1.5c.203-.209.472-.314.746-.314zM8.758 6.397a5.33 5.33 0 0 1 3.715 1.564l2.863 2.862c.42.42.42 1.08 0 1.5-.42.419-1.08.419-1.5 0L10.975 9.46a3.249 3.249 0 0 0-4.558-.015 3.243 3.243 0 0 0 .03 4.54l2.863 2.863c.42.42.42 1.08 0 1.499a1.05 1.05 0 0 1-1.499 0L4.95 15.484c-2.054-2.053-2.084-5.365-.105-7.463.015-.03.03-.045.045-.06l.03-.03.03-.03c.015-.015.045-.03.06-.045a5.355 5.355 0 0 1 3.748-1.46z" />
    </svg>
  );
}

// TwoFactorSetupModal: the authenticator-app enrollment wizard.
//   1. Install  — point the user at an app
//   2. Scan     — QR (server-rendered) + manual secret
//   3. Verify   — confirm a 6-digit code (nothing is enabled until this passes)
//   4. Recovery — show one-time backup codes once, gated by a "saved" confirm
// onEnabled fires after a successful verify so the page can refresh status.

type Step = "scan" | "verify" | "recovery";
const STEPS: Step[] = ["scan", "verify", "recovery"];
const STEP_LABELS: Record<Step, string> = {
  scan: "Scan",
  verify: "Verify",
  recovery: "Backup",
};

// Group a base32 secret into 4-char chunks for legible manual entry.
const groupKey = (s: string) => s.replace(/\s/g, "").match(/.{1,4}/g)?.join(" ") ?? s;

interface SetupResp {
  secret: string;
  otpauth_url: string;
  qr_data_url: string;
}

const btnBase =
  "inline-flex h-7 min-w-[5rem] items-center justify-center gap-1.5 rounded-md px-3 text-[11px] font-medium shadow-sm transition-colors disabled:cursor-not-allowed disabled:opacity-50 outline-none focus-visible:ring-1 focus-visible:ring-primary/40";
const btnOutline = cn(btnBase, "border border-foreground/30 bg-background text-foreground hover:bg-muted");
const btnPrimary = cn(btnBase, "bg-primary text-primary-foreground hover:bg-primary/90");

export function TwoFactorSetupModal({
  open,
  accountEmail,
  onClose,
  onEnabled,
}: {
  open: boolean;
  accountEmail: string;
  onClose: () => void;
  onEnabled: () => void;
}) {
  const [step, setStep] = useState<Step>("install");
  const [setup, setSetup] = useState<SetupResp | null>(null);
  const [setupErr, setSetupErr] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [verifying, setVerifying] = useState(false);
  const [verifyErr, setVerifyErr] = useState<string | null>(null);
  const [recovery, setRecovery] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const [copied, setCopied] = useState(false);

  // Reset + kick off setup whenever the modal opens.
  useEffect(() => {
    if (!open) return;
    setStep("scan");
    setSetup(null);
    setSetupErr(null);
    setCode("");
    setVerifyErr(null);
    setRecovery([]);
    setSaved(false);
    setCopied(false);
    let cancelled = false;
    void (async () => {
      try {
        const res = await api.post<SetupResp>("/api/me/2fa/totp/setup");
        if (!cancelled) setSetup(res);
      } catch (err) {
        if (!cancelled) setSetupErr(err instanceof ApiError ? err.message : "Couldn't start setup.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !verifying) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, verifying, onClose]);

  if (!open) return null;

  const stepIdx = STEPS.indexOf(step);

  async function verify() {
    const clean = code.replace(/\s/g, "");
    if (clean.length !== 6 || verifying) return;
    setVerifying(true);
    setVerifyErr(null);
    try {
      const res = await api.post<{ recovery_codes: string[] }>("/api/me/2fa/totp/verify", { code: clean });
      setRecovery(res.recovery_codes ?? []);
      setStep("recovery");
      onEnabled(); // 2FA is on now — let the page refresh its status
    } catch (err) {
      setVerifyErr(err instanceof ApiError ? err.message : "Couldn't verify — try again.");
    } finally {
      setVerifying(false);
    }
  }

  function copyCodes() {
    void navigator.clipboard.writeText(recovery.join("\n")).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  function downloadCodes() {
    const blob = new Blob([`Trilli recovery codes\n\n${recovery.join("\n")}\n`], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "trilli-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(url);
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !verifying && step !== "recovery") onClose();
      }}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
          <div className="flex items-center gap-2.5">
            <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10">
              <MonitorSmartphone className="h-[18px] w-[18px] text-primary" />
            </span>
            <div className="leading-tight">
              <h2 className="text-sm font-semibold text-foreground">Set up authenticator app</h2>
              <p className="text-[11px] text-muted-foreground">Two-factor authentication</p>
            </div>
          </div>
          {step !== "recovery" && (
            <button
              type="button"
              onClick={() => !verifying && onClose()}
              className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        {/* Step indicator — centered, connected numbered steps. */}
        <div className="flex items-center justify-center gap-2 border-b border-border px-6 py-3">
          {STEPS.map((s, i) => (
            <div key={s} className="flex items-center gap-2">
              {/* Connector from the previous step. */}
              {i > 0 && (
                <span className={cn("h-px w-7 sm:w-9", i <= stepIdx ? "bg-primary/50" : "bg-border")} />
              )}
              <span
                className={cn(
                  "flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-full text-[10px] font-semibold",
                  i < stepIdx
                    ? "bg-primary text-primary-foreground"
                    : i === stepIdx
                      ? "bg-primary/15 text-primary ring-1 ring-primary/40"
                      : "bg-muted text-muted-foreground",
                )}
              >
                {i < stepIdx ? <Check className="h-3 w-3" /> : i + 1}
              </span>
              <span className="flex flex-col leading-none">
                <span className="text-[9px] uppercase tracking-wide text-muted-foreground/70">Step {i + 1}</span>
                <span
                  className={cn(
                    "mt-0.5 text-[11px]",
                    i === stepIdx ? "font-semibold text-foreground" : "text-muted-foreground",
                  )}
                >
                  {STEP_LABELS[s]}
                </span>
              </span>
            </div>
          ))}
        </div>

        <div className="p-4">
          {step === "scan" && (
            <div className="space-y-4">
              {/* Centered, single clear instruction. */}
              <div className="text-center">
                <h3 className="text-base font-semibold text-foreground">Scan with your authenticator</h3>
                <p className="mx-auto mt-1 max-w-[18rem] text-[12px] leading-relaxed text-muted-foreground">
                  Open your app, tap add, then scan this code to link your Trilli account.
                </p>
              </div>

              {/* Compatible-app chips — quiet brand validation. */}
              <div className="flex flex-wrap items-center justify-center gap-1.5">
                <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card py-1 pl-1 pr-2.5">
                  <img src={googleAuthIcon} alt="" className="h-4 w-4" />
                  <span className="text-[11px] font-medium text-foreground">Google</span>
                </span>
                <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card py-1 pl-1 pr-2.5">
                  <AuthyLogo className="h-4 w-4" />
                  <span className="text-[11px] font-medium text-foreground">Authy</span>
                </span>
                <span className="text-[11px] text-muted-foreground">+ any TOTP app</span>
              </div>

              {/* QR with the account label tucked at its base. */}
              <div className="flex justify-center">
                {setupErr ? (
                  <p className="py-10 text-center text-[12px] text-destructive">{setupErr}</p>
                ) : setup ? (
                  <div className="relative rounded-2xl border border-border bg-white p-3 shadow-sm">
                    <img src={setup.qr_data_url} alt="Authenticator QR code" className="h-40 w-40" />
                    <span className="absolute -bottom-2 left-1/2 -translate-x-1/2 whitespace-nowrap rounded-full border border-border bg-card px-2 py-0.5 text-[10.5px] text-muted-foreground shadow-sm">
                      {accountEmail}
                    </span>
                  </div>
                ) : (
                  <div className="flex h-[184px] w-[184px] items-center justify-center rounded-2xl border border-border bg-muted/40">
                    <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
                  </div>
                )}
              </div>

              {/* Manual entry key — one tidy bar. */}
              {setup && (
                <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2">
                  <KeyRound className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                  <code className="flex-1 truncate font-mono text-[12.5px] tracking-wide text-foreground">
                    {groupKey(setup.secret)}
                  </code>
                  <button
                    type="button"
                    onClick={() => {
                      void navigator.clipboard.writeText(setup.secret);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 1500);
                    }}
                    className="flex flex-shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  >
                    {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                    {copied ? "Copied" : "Copy key"}
                  </button>
                </div>
              )}
            </div>
          )}

          {step === "verify" && (
            <div className="space-y-3">
              <p className="text-center text-[12px] text-muted-foreground">
                Enter the 6-digit code shown in your authenticator app.
              </p>
              <OtpInput
                value={code}
                onChange={setCode}
                autoFocus
                disabled={verifying}
                onComplete={() => void verify()}
              />
              {verifyErr && (
                <p className="text-center text-[11px] text-destructive" role="alert">
                  {verifyErr}
                </p>
              )}
            </div>
          )}

          {step === "recovery" && (
            <div className="space-y-3">
              <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
                <ShieldCheck className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-600" />
                <p className="text-[12px] leading-relaxed text-foreground">
                  Two-factor authentication is on. Save these one-time recovery codes somewhere safe — each works
                  once if you lose access to your authenticator. <span className="font-medium">They won't be shown
                  again.</span>
                </p>
              </div>
              <div className="grid grid-cols-2 gap-1.5 rounded-md border border-border bg-muted/30 p-2.5">
                {recovery.map((c) => (
                  <code key={c} className="text-center font-mono text-[12.5px] text-foreground">
                    {c}
                  </code>
                ))}
              </div>
              <div className="flex items-center gap-2">
                <button type="button" onClick={copyCodes} className={cn(btnOutline, "flex-1")}>
                  {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                  {copied ? "Copied" : "Copy"}
                </button>
                <button type="button" onClick={downloadCodes} className={cn(btnOutline, "flex-1")}>
                  <Download className="h-3.5 w-3.5" />
                  Download
                </button>
              </div>
              <label className="flex items-start gap-2 text-[12px] text-foreground">
                <input
                  type="checkbox"
                  checked={saved}
                  onChange={(e) => setSaved(e.target.checked)}
                  className={cn(checkboxCls, "mt-0.5")}
                />
                I've saved my recovery codes somewhere safe.
              </label>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between gap-2 border-t border-border px-4 py-2.5">
          {step !== "recovery" ? (
            <>
              <button
                type="button"
                onClick={() => (stepIdx === 0 ? onClose() : setStep(STEPS[stepIdx - 1]))}
                disabled={verifying}
                className={btnOutline}
              >
                {stepIdx === 0 ? "Cancel" : "Back"}
              </button>
              <span className="text-[11px] text-muted-foreground">
                Step {stepIdx + 1} of {STEPS.length}
              </span>
              {step === "verify" ? (
                <button
                  type="button"
                  onClick={() => void verify()}
                  disabled={code.length !== 6 || verifying}
                  className={btnPrimary}
                >
                  {verifying && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  {verifying ? "Verifying…" : "Verify & enable"}
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => setStep(STEPS[stepIdx + 1])}
                  disabled={step === "scan" && !setup}
                  className={btnPrimary}
                >
                  Next
                </button>
              )}
            </>
          ) : (
            <button
              type="button"
              onClick={onClose}
              disabled={!saved}
              className={cn(btnPrimary, "ml-auto")}
            >
              Done
            </button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
