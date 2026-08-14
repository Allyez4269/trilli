import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { AlertTriangle, Loader2, MailCheck, ShieldCheck } from "lucide-react";

import { TrilliLogo } from "@/components/Logo";
import { api, ApiError } from "@/lib/api";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

// ConfirmEmail backs the /confirm-email/:token link from the verification
// email. It first looks up the pending change (a safe GET, so email-client
// link prefetchers don't consume it), shows which address is being confirmed,
// then applies it via a POST when the user clicks Confirm.
type Phase = "loading" | "ready" | "confirming" | "done" | "expired" | "invalid";

export default function ConfirmEmail() {
  const { token } = useParams<{ token: string }>();
  const [phase, setPhase] = useState<Phase>("loading");
  const [newEmail, setNewEmail] = useState("");
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get<{ new_email: string }>(`/api/email-change/${token}`);
        if (cancelled) return;
        setNewEmail(res.new_email);
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

  async function confirm() {
    setPhase("confirming");
    setErrorMsg(null);
    try {
      const res = await api.post<{ ok: boolean; email: string }>(
        `/api/email-change/${token}/confirm`,
      );
      setNewEmail(res.email);
      setPhase("done");
    } catch (err) {
      if (err instanceof ApiError && err.status === 410) {
        setPhase("expired");
      } else if (err instanceof ApiError && err.status === 409) {
        setErrorMsg(err.message);
        setPhase("invalid");
      } else {
        setPhase("invalid");
      }
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="items-center text-center">
          <TrilliLogo className="h-[34px] text-foreground" />
          <CardTitle className="mt-4 flex items-center justify-center gap-2 text-xl">
            {phase === "done" ? (
              <>
                <MailCheck className="h-5 w-5 text-emerald-600" />
                Email confirmed
              </>
            ) : phase === "expired" || phase === "invalid" ? (
              <>
                <AlertTriangle className="h-5 w-5 text-amber-600" />
                {phase === "expired" ? "Link expired" : "Link unavailable"}
              </>
            ) : (
              <>
                <ShieldCheck className="h-5 w-5 text-primary" />
                Confirm your new email
              </>
            )}
          </CardTitle>
          <CardDescription>
            {phase === "done"
              ? "Your account email has been updated."
              : phase === "expired"
                ? "This confirmation link is no longer valid."
                : phase === "invalid"
                  ? "We couldn't confirm this change."
                  : "Review and confirm the change to your account email."}
          </CardDescription>
        </CardHeader>

        <CardContent className="space-y-4">
          {(phase === "loading" || phase === "confirming") && (
            <div className="flex items-center justify-center gap-2 py-4 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              {phase === "loading" ? "Checking your link…" : "Confirming…"}
            </div>
          )}

          {phase === "ready" && (
            <>
              <div className="rounded-lg border border-border bg-muted/40 p-3 text-center">
                <p className="text-[10px] uppercase tracking-wide text-muted-foreground">
                  New email address
                </p>
                <p className="mt-0.5 break-all text-sm font-medium text-foreground">{newEmail}</p>
              </div>
              <button
                type="button"
                onClick={confirm}
                className="inline-flex h-7 w-full items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90"
              >
                Confirm email change
              </button>
              <p className="text-center text-[11px] text-muted-foreground">
                If you didn't request this, you can close this page — nothing will change.
              </p>
            </>
          )}

          {phase === "done" && (
            <>
              <div className="flex items-start gap-3 rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
                <MailCheck className="mt-0.5 h-5 w-5 flex-shrink-0 text-emerald-600" />
                <p className="text-xs text-muted-foreground">
                  You'll now sign in with{" "}
                  <span className="font-medium text-foreground">{newEmail}</span>. Use it the next
                  time you log in.
                </p>
              </div>
              <Link
                to="/login"
                className="inline-flex h-7 w-full items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90"
              >
                Go to sign in
              </Link>
            </>
          )}

          {(phase === "expired" || phase === "invalid") && (
            <>
              {errorMsg && (
                <p className="text-center text-xs text-destructive" role="alert">
                  {errorMsg}
                </p>
              )}
              <p className="text-center text-xs text-muted-foreground">
                {phase === "expired"
                  ? "Open Settings → Account and request a new confirmation link."
                  : "This link may have already been used. Request a new one from Settings → Account if you still want to change your email."}
              </p>
              <Link
                to="/settings/account"
                className="inline-flex h-7 w-full items-center justify-center rounded-md border border-foreground/30 bg-background px-4 text-sm font-medium text-foreground shadow-sm transition-colors hover:bg-muted"
              >
                Back to Settings
              </Link>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
