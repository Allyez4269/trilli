import { FormEvent, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { AlertTriangle, CheckCircle2, Eye, EyeOff, Loader2, ShieldCheck } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import AuthLayout from "@/components/AuthLayout";
import PasswordStrength from "@/components/PasswordStrength";

// ResetPassword backs the /reset-password/:token link from the reset email. It
// first validates the token (a safe GET — never consumes it), then shows a
// set-new-password form that POSTs the new password. On success the password
// changes and all sessions are revoked, so the user signs back in fresh.
type Phase = "loading" | "ready" | "saving" | "done" | "expired" | "invalid";

const MIN_LEN = 8;
const fieldCls = "h-9 border-foreground/25";

export default function ResetPassword() {
  const { token } = useParams<{ token: string }>();
  const navigate = useNavigate();
  const [phase, setPhase] = useState<Phase>("loading");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [show, setShow] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get<{ email: string }>(`/api/password-reset/${token}`);
        if (cancelled) return;
        setEmail(res.email);
        setPhase("ready");
      } catch (err) {
        if (cancelled) return;
        setPhase(err instanceof ApiError && err.status === 410 ? "expired" : "invalid");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);

  const mismatch = confirm.length > 0 && password !== confirm;
  const canSubmit = password.length >= MIN_LEN && password === confirm;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setPhase("saving");
    setErrorMsg(null);
    try {
      await api.post(`/api/password-reset/${token}`, { password });
      setPhase("done");
    } catch (err) {
      if (err instanceof ApiError && err.status === 410) {
        setPhase("expired");
      } else if (err instanceof ApiError && err.status === 400) {
        setErrorMsg(err.message);
        setPhase("ready");
      } else if (err instanceof ApiError && err.status === 404) {
        setPhase("invalid");
      } else {
        setErrorMsg("Something went wrong. Try again.");
        setPhase("ready");
      }
    }
  }

  const heading =
    phase === "done"
      ? "Password updated"
      : phase === "expired"
        ? "Link expired"
        : phase === "invalid"
          ? "Link unavailable"
          : "Set a new password";

  const subtitle =
    phase === "done"
      ? "Your password has been changed."
      : phase === "expired"
        ? "This reset link is no longer valid."
        : phase === "invalid"
          ? "We couldn't use this reset link."
          : email
            ? `Choose a new password for ${email}.`
            : "Choose a new password.";

  return (
    <AuthLayout>
      <div className="space-y-5">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold text-foreground">
            {phase === "done" ? (
              <CheckCircle2 className="h-6 w-6 text-emerald-600" />
            ) : phase === "expired" || phase === "invalid" ? (
              <AlertTriangle className="h-6 w-6 text-amber-600" />
            ) : (
              <ShieldCheck className="h-6 w-6 text-primary" />
            )}
            {heading}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>
        </div>

        {phase === "loading" && (
          <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            Checking your link…
          </div>
        )}

        {(phase === "ready" || phase === "saving") && (
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="password">New password</Label>
              <div className="relative">
                <Input
                  id="password"
                  type={show ? "text" : "password"}
                  autoFocus
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className={`${fieldCls} pr-9`}
                />
                <button
                  type="button"
                  onClick={() => setShow((v) => !v)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  aria-label={show ? "Hide password" : "Show password"}
                >
                  {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
              <PasswordStrength password={password} />
            </div>

            <div className="space-y-2">
              <Label htmlFor="confirm">Confirm password</Label>
              <Input
                id="confirm"
                type={show ? "text" : "password"}
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                className={fieldCls}
              />
              {mismatch && <p className="text-[11px] text-destructive">Passwords don't match.</p>}
            </div>

            {errorMsg && (
              <p className="text-sm text-destructive" role="alert">
                {errorMsg}
              </p>
            )}

            <Button type="submit" disabled={!canSubmit || phase === "saving"} className="h-9 w-full">
              {phase === "saving" ? "Saving…" : "Set new password"}
            </Button>
          </form>
        )}

        {phase === "done" && (
          <>
            <div className="flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
              <CheckCircle2 className="mt-0.5 h-5 w-5 flex-shrink-0 text-emerald-600" />
              <p className="text-xs text-muted-foreground">
                For your security we signed out all sessions. Sign in with your new password.
              </p>
            </div>
            <Button onClick={() => navigate("/login", { replace: true })} className="h-9 w-full">
              Go to sign in
            </Button>
          </>
        )}

        {(phase === "expired" || phase === "invalid") && (
          <>
            <p className="text-sm text-muted-foreground">
              {phase === "expired"
                ? "Request a fresh link from the sign-in page or Settings → Security."
                : "This link may have already been used. Request a new one from the sign-in page or Settings → Security."}
            </p>
            <Link
              to="/login"
              className="inline-flex h-9 w-full items-center justify-center rounded-md border border-foreground/25 bg-background px-4 text-sm font-medium text-foreground transition-colors hover:bg-muted"
            >
              Go to sign in
            </Link>
          </>
        )}
      </div>
    </AuthLayout>
  );
}
