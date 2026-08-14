import { useState } from "react";
import { ShieldAlert, X } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import OtpInput from "@/components/OtpInput";

// StepUpModal gates a consequential action behind fresh 2FA (SPEC §6.9). The
// 6-digit code is entered as individual boxes and submits INSTANTLY once all
// digits are filled (no button), mirroring app.trilli.com. A recovery-code
// fallback is available for operators without their authenticator.
export default function StepUpModal({
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
  onClose,
}: {
  title: string;
  description: string;
  confirmLabel: string;
  destructive?: boolean;
  onConfirm: () => Promise<void>;
  onClose: () => void;
}) {
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(value: string) {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/cmx/step-up", { code: value.replace(/\s/g, "") });
      await onConfirm();
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
      setCode("");
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
            <ShieldAlert className="h-5 w-5" />
          </span>
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
        </div>

        {recovery ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submit(code);
            }}
            className="space-y-3"
          >
            <Label htmlFor="recovery-code" className="block text-center">
              Recovery code
            </Label>
            <Input
              id="recovery-code"
              autoFocus
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="xxxx-xxxx"
              className="h-8 border-foreground/25 text-center font-mono tracking-widest"
            />
            <Button
              type="submit"
              disabled={busy || code.trim().length < 4}
              variant={destructive ? "destructive" : "default"}
              className="h-8 w-full"
            >
              {busy ? "Confirming…" : confirmLabel}
            </Button>
          </form>
        ) : (
          <div className="space-y-3">
            <Label className="block text-center">Enter your 6-digit code</Label>
            <OtpInput value={code} onChange={setCode} autoFocus disabled={busy} onComplete={(v) => void submit(v)} />
          </div>
        )}

        {error && (
          <p className="mt-3 text-center text-sm text-destructive" role="alert">
            {error}
          </p>
        )}

        <button
          type="button"
          onClick={() => {
            setRecovery((v) => !v);
            setCode("");
            setError(null);
          }}
          className="mt-4 block w-full text-center text-[13px] text-muted-foreground transition-colors hover:text-foreground"
        >
          {recovery ? "Use your authenticator code" : "Use a recovery code instead"}
        </button>
      </div>
    </div>
  );
}
