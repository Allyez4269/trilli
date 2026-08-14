import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Eye, EyeOff, Loader2, UserPlus } from "lucide-react";

import {
  api,
  ApiError,
  type InviteAcceptResponse,
  type InviteLookup,
} from "@/lib/api";
import { checkboxCls, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { TrilliLogo } from "@/components/Logo";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import PasswordStrength from "@/components/PasswordStrength";

type LookupState =
  | { kind: "loading" }
  | { kind: "ready"; data: InviteLookup }
  | { kind: "error"; status: number; message: string };

export default function InviteAccept() {
  const { token = "" } = useParams<{ token: string }>();
  const navigate = useNavigate();

  const [state, setState] = useState<LookupState>({ kind: "loading" });
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [acceptTerms, setAcceptTerms] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    if (!token) {
      setState({ kind: "error", status: 404, message: "Missing token." });
      return;
    }
    let cancelled = false;
    api
      .get<InviteLookup>(`/api/invites/${encodeURIComponent(token)}`)
      .then((data) => {
        if (!cancelled) setState({ kind: "ready", data });
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError) {
          setState({ kind: "error", status: err.status, message: err.message });
        } else {
          setState({ kind: "error", status: 0, message: "Network error" });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setSubmitError(null);
    setSubmitting(true);
    try {
      const res = await api.post<InviteAcceptResponse>(
        `/api/invites/${encodeURIComponent(token)}/accept`,
        { first_name: firstName.trim(), last_name: lastName.trim(), password },
      );
      if (res?.twofa_required) {
        // Membership created, but the account has 2FA — finish sign-in through
        // the normal login flow so the second factor is enforced.
        navigate("/login", { replace: true, state: { notice: "invite-accepted" } });
        return;
      }
      // Session cookie is set by the server; drop into the workspace.
      navigate("/files", { replace: true });
    } catch (err) {
      setSubmitError(
        err instanceof ApiError ? err.message : "Something went wrong",
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <div className="w-full max-w-md">
        <div className="mb-6 flex items-center justify-center">
          <TrilliLogo className="h-[34px] text-foreground" />
        </div>

        {state.kind === "loading" && (
          <div className="rounded-xl border border-border bg-card p-8 text-center shadow-sm">
            <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" />
            <p className="mt-3 text-sm text-muted-foreground">Loading invite…</p>
          </div>
        )}

        {state.kind === "error" && (
          <InviteError status={state.status} message={state.message} />
        )}

        {state.kind === "ready" && (
          <div className="rounded-xl border border-border bg-card p-6 shadow-sm">
            {/* Invite summary banner */}
            <div className="mb-5 flex items-start gap-3">
              <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10">
                <UserPlus className="h-5 w-5 text-primary" />
              </div>
              <div className="min-w-0 flex-1">
                <h1 className="text-base font-semibold text-foreground">
                  You're invited to{" "}
                  <span className="text-primary">{state.data.tenant_name}</span>
                </h1>
                <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                  Joining as{" "}
                  <span className="font-medium text-foreground">
                    {capitalize(state.data.role)}
                  </span>
                  , invited by{" "}
                  <span className="font-medium text-foreground">
                    {state.data.inviter_email}
                  </span>
                  .
                </p>
              </div>
            </div>

            <form onSubmit={onSubmit} className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  type="email"
                  value={state.data.email}
                  readOnly
                  className="bg-muted text-muted-foreground"
                />
              </div>

              {!state.data.email_has_account && (
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label htmlFor="first_name">First name</Label>
                    <Input
                      id="first_name"
                      type="text"
                      placeholder="Mary"
                      value={firstName}
                      onChange={(e) => setFirstName(e.target.value)}
                      required
                      minLength={1}
                      maxLength={60}
                      autoComplete="given-name"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="last_name">Last name</Label>
                    <Input
                      id="last_name"
                      type="text"
                      placeholder="Anderson"
                      value={lastName}
                      onChange={(e) => setLastName(e.target.value)}
                      required
                      minLength={1}
                      maxLength={60}
                      autoComplete="family-name"
                    />
                  </div>
                </div>
              )}

              <div className="space-y-1.5">
                <Label htmlFor="password">
                  {state.data.email_has_account
                    ? "Your existing password"
                    : "Choose a password"}
                </Label>
                <div className="relative">
                  <Input
                    id="password"
                    type={showPassword ? "text" : "password"}
                    placeholder="At least 8 characters"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required
                    minLength={8}
                    autoComplete={
                      state.data.email_has_account ? "current-password" : "new-password"
                    }
                    className="pr-10"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                    aria-label={showPassword ? "Hide password" : "Show password"}
                  >
                    {showPassword ? (
                      <EyeOff className="h-4 w-4" />
                    ) : (
                      <Eye className="h-4 w-4" />
                    )}
                  </button>
                </div>
                {!state.data.email_has_account && <PasswordStrength password={password} />}
                {state.data.email_has_account && (
                  <p className="text-[11px] text-muted-foreground">
                    You already have a Trilli account. Sign in with your existing
                    password to accept this invite.
                  </p>
                )}
              </div>

              {!state.data.email_has_account && (
                <label className="flex items-start gap-2 text-xs leading-relaxed text-foreground/80">
                  <input
                    type="checkbox"
                    checked={acceptTerms}
                    onChange={(e) => setAcceptTerms(e.target.checked)}
                    required
                    className={cn(checkboxCls, "mt-0.5")}
                  />
                  <span>
                    I agree to the Trilli{" "}
                    <a className="text-primary hover:underline" href="/terms">
                      Terms
                    </a>{" "}
                    and{" "}
                    <a className="text-primary hover:underline" href="/privacy">
                      Privacy Policy
                    </a>
                    .
                  </span>
                </label>
              )}

              {submitError && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  {submitError}
                </div>
              )}

              <Button
                type="submit"
                className="w-full"
                disabled={
                  submitting ||
                  password.length < 8 ||
                  (!state.data.email_has_account &&
                    (firstName.trim().length < 1 ||
                      lastName.trim().length < 1 ||
                      !acceptTerms))
                }
              >
                {submitting ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                ) : null}
                {state.data.email_has_account ? "Sign in & join" : "Create account & join"}
              </Button>
            </form>
          </div>
        )}
      </div>
    </div>
  );
}

function InviteError({ status, message }: { status: number; message: string }) {
  const title =
    status === 410
      ? "This invite is no longer valid"
      : status === 404
        ? "Invite not found"
        : "Something went wrong";
  const hint =
    status === 410
      ? "It may have expired, been revoked, or already been accepted. Ask the workspace owner to send a new one."
      : status === 404
        ? "Check the link is correct, or ask for a fresh invite."
        : message;
  return (
    <div className="rounded-xl border border-border bg-card p-8 text-center shadow-sm">
      <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-muted">
        <UserPlus className="h-6 w-6 text-muted-foreground" />
      </div>
      <h1 className="mb-1 text-base font-semibold text-foreground">{title}</h1>
      <p className="text-sm text-muted-foreground">{hint}</p>
    </div>
  );
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}
