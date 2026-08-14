import { FormEvent, useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { Fingerprint, Loader2, Rocket, ArrowRight } from "lucide-react";

import { api, ApiError, type Identity } from "@/lib/api";
import { readFlash } from "@/lib/flash";
import { continueWithProvider } from "@/lib/googlePopup";
import { checkboxCls, cn } from "@/lib/utils";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import AuthLayout from "@/components/AuthLayout";
import OtpInput from "@/components/OtpInput";
import {
  passkeysSupported,
  loginWithPasskey,
  loginWithPasskeyChallenge,
  passkeyErrorMessage,
} from "@/lib/webauthn";

// Shared field outline — --input is white in this theme, so inputs get a
// visible border from foreground/25 to match the rest of the app.
const fieldCls = "h-9 border-foreground/25";

// The marketing plans page — where a not-yet-registered visitor goes to choose a
// plan and finish signing up.
const PLANS_URL = "https://www.trilli.com/pricing";

type Mode = "identifier" | "password" | "twofa" | "forgot" | "no_account";

export default function Login() {
  const navigate = useNavigate();
  const location = useLocation();
  const { setIdentity } = useAuth();

  // A revoked/expired session bounces here with a one-time sessionStorage flag
  // (set in api.ts) rather than a ?disabled=1 query string — read it once and
  // clear it so the URL stays clean and the notice doesn't persist on reload.
  // Why the session ended (set in api.ts on a mid-session 401, or by the client
  // idle-logout): "idle" → timed out from inactivity; any other truthy value →
  // the session ended (expired/revoked) for an unknown reason. Read once and
  // clear. Note: this is NOT proof the account was disabled — that's determined
  // at login, so the generic case shows a neutral "session ended" notice.
  const [sessionEndReason] = useState<string | null>(() => {
    if (typeof window === "undefined") return null;
    try {
      const v = sessionStorage.getItem("auth:session-ended");
      if (v) {
        sessionStorage.removeItem("auth:session-ended");
        return v;
      }
    } catch {
      // ignore storage access errors
    }
    return null;
  });

  const [mode, setMode] = useState<Mode>("identifier");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  // Seed the banner from the disabled flag or a one-time flash cookie (set by
  // server redirects like the Google OAuth callback) — never from a ?error= URL.
  const [error, setError] = useState<string | null>(() => {
    if (sessionEndReason === "idle") return "You were signed out due to inactivity. Please sign in again.";
    // Any other mid-session 401 means the session is simply no longer valid
    // (expired, server-side idle timeout, or revoked) — NOT necessarily a
    // disabled account. Show a neutral notice; if the account really is
    // disabled, the login attempt below surfaces that specific message.
    if (sessionEndReason) return "Your session has ended. Please sign in again.";
    return readFlash();
  });
  // Friendly (non-error) notice, e.g. arriving from a just-accepted invite on
  // a 2FA-enrolled account: membership exists, sign-in completes it.
  const [notice] = useState<string | null>(() =>
    (location.state as { notice?: string } | null)?.notice === "invite-accepted"
      ? "Invite accepted! Sign in to access your new workspace."
      : null,
  );
  const [busy, setBusy] = useState(false);

  // 2FA challenge state.
  const [pendingToken, setPendingToken] = useState<string | null>(null);
  const [challengeMethod, setChallengeMethod] = useState<"totp" | "passkey">("totp");
  const [code, setCode] = useState("");
  const [remember, setRemember] = useState(false);
  const [useRecovery, setUseRecovery] = useState(false);

  // Passkey + forgot-password state.
  const [pkBusy, setPkBusy] = useState(false);
  const pkAvailable = passkeysSupported();
  const [forgotEmail, setForgotEmail] = useState("");
  const [forgotSent, setForgotSent] = useState(false);

  // Which provider's popup is in flight (drives the "Connecting…" interstitial);
  // the controller lets the back affordance cancel it cleanly.
  const [connecting, setConnecting] = useState<null | "google" | "microsoft">(null);
  const providerCtrl = useRef<{ cancel: () => void } | null>(null);
  // Which provider authenticated when we discover there's no account yet — drives
  // the "let's get you started" prompt's copy ("…with Google/Microsoft").
  const [noAccountProvider, setNoAccountProvider] = useState<"google" | "microsoft" | null>(null);

  const redirectTo =
    (location.state as { from?: { pathname?: string } } | null)?.from?.pathname ?? "/dashboard";

  // Arriving via /login/new-account (the marketing "Get Started" hand-off for an
  // already-registered email): after auth, route into the buy-your-own-account
  // flow rather than the app. Slash route, not a ?intent= query string.
  const newAccountIntent = location.pathname.endsWith("/new-account");

  // After any successful authentication: an explicit new-account intent wins;
  // else a user with >1 account picks which to open; everyone else goes home.
  const goAfterAuth = (id: Identity) => {
    setIdentity(id);
    if (newAccountIntent) {
      navigate("/get-started", { replace: true });
      return;
    }
    navigate((id.tenants?.length ?? 0) > 1 ? "/select-account" : redirectTo, { replace: true });
  };

  // Google sign-in that hits an account with 2FA hands the pending token here
  // via a short-lived cookie (set first-party by the OAuth callback). Read it
  // once on mount, clear it, and drop straight into the 2FA challenge — so a
  // federated login still passes through the second factor.
  useEffect(() => {
    const m = document.cookie.match(/(?:^|;\s*)trilli_2fa_pending=([^;]*)/);
    if (!m) return;
    document.cookie = "trilli_2fa_pending=; Path=/; Max-Age=0; SameSite=Lax";
    try {
      const { token, method } = JSON.parse(decodeURIComponent(m[1])) as {
        token?: string;
        method?: string;
      };
      if (token) {
        setPendingToken(token);
        setChallengeMethod(method === "passkey" ? "passkey" : "totp");
        setCode("");
        setMode("twofa");
      }
    } catch {
      // ignore a malformed cookie
    }
  }, []);

  // Full-page fallback (popup blocked): the OAuth callback set a flag cookie and
  // bounced here because the email has no account yet. Read it once, clear it,
  // and show the same "let's get you started" prompt the popup flow shows.
  useEffect(() => {
    const m = document.cookie.match(/(?:^|;\s*)trilli_oauth_no_account=([^;]*)/);
    if (!m) return;
    document.cookie = "trilli_oauth_no_account=; Path=/; Max-Age=0; SameSite=Lax";
    const provider = decodeURIComponent(m[1]);
    setNoAccountProvider(provider === "microsoft" ? "microsoft" : "google");
    setMode("no_account");
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await api.post<
        Identity & { twofa_required?: boolean; pending_token?: string; method?: "totp" | "passkey" }
      >("/api/auth/login", { email, password });
      if (res.twofa_required && res.pending_token) {
        setPendingToken(res.pending_token);
        setChallengeMethod(res.method === "passkey" ? "passkey" : "totp");
        setCode("");
        setMode("twofa");
        setBusy(false);
        return;
      }
      goAfterAuth(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Login failed" : "Network error");
    } finally {
      setBusy(false);
    }
  }

  async function verify2fa(e: FormEvent, codeOverride?: string) {
    e.preventDefault();
    if (!pendingToken || busy) return;
    const submitCode = (codeOverride ?? code).replace(/\s/g, "");
    setError(null);
    setBusy(true);
    try {
      const id = await api.post<Identity>("/api/auth/login/2fa", {
        pending_token: pendingToken,
        code: submitCode,
        remember,
      });
      goAfterAuth(id);
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Verification failed" : "Network error");
    } finally {
      setBusy(false);
    }
  }

  async function verifyChallengeWithPasskey() {
    if (!pendingToken || pkBusy || busy) return;
    setError(null);
    setPkBusy(true);
    try {
      const id = await loginWithPasskeyChallenge(pendingToken);
      goAfterAuth(id);
    } catch (err) {
      setError(passkeyErrorMessage(err, "Couldn't verify your passkey. Try again."));
    } finally {
      setPkBusy(false);
    }
  }

  async function signInWithPasskey() {
    if (pkBusy || busy) return;
    setError(null);
    setPkBusy(true);
    try {
      const id = await loginWithPasskey();
      goAfterAuth(id);
    } catch (err) {
      setError(passkeyErrorMessage(err, "Passkey sign-in failed. Try your password."));
    } finally {
      setPkBusy(false);
    }
  }

  async function submitForgot(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setError(null);
    setBusy(true);
    try {
      // Always succeeds (anti-enumeration) — show the same confirmation either way.
      await api.post("/api/auth/forgot-password", { email: forgotEmail });
      setForgotSent(true);
    } catch {
      setForgotSent(true);
    } finally {
      setBusy(false);
    }
  }

  function backToSignIn() {
    setMode("identifier");
    setPendingToken(null);
    setCode("");
    setUseRecovery(false);
    setForgotSent(false);
    setError(null);
  }

  // startProvider launches the chosen provider's popup and flips into the
  // "Connecting…" state so the form gives feedback and offers a clean way back.
  function startProvider(provider: "google" | "microsoft") {
    setError(null);
    const ctrl = continueWithProvider(provider, {
      mode: "login",
      onError: (m) => {
        providerCtrl.current = null;
        setConnecting(null);
        setError(m);
      },
      onClosed: () => {
        providerCtrl.current = null;
        setConnecting(null);
      },
      onNoAccount: () => {
        providerCtrl.current = null;
        setConnecting(null);
        setNoAccountProvider(provider);
        setMode("no_account");
      },
    });
    if (ctrl) {
      providerCtrl.current = ctrl;
      setConnecting(provider);
    }
  }

  // handleBack is the single "return to the start" control rendered on the
  // container: it cancels an in-flight provider popup, or resets any sub-step
  // (password / 2FA / forgot) back to the original sign-in options.
  function handleBack() {
    if (connecting) {
      providerCtrl.current?.cancel();
      providerCtrl.current = null;
      setConnecting(null);
      setError(null);
      return;
    }
    backToSignIn();
  }

  return (
    <AuthLayout onBack={connecting || mode !== "identifier" ? handleBack : undefined}>
      {/* ---- Connecting to a provider (popup in flight) ---- */}
      {connecting ? (
        <div className="space-y-6 duration-200 animate-in fade-in">
          <div className="flex flex-col items-center text-center">
            <span className="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl border border-border bg-card shadow-sm">
              {connecting === "google" ? (
                <GoogleIcon className="h-7 w-7" />
              ) : (
                <MicrosoftIcon className="h-7 w-7" />
              )}
            </span>
            <h1 className="text-2xl font-semibold text-foreground">
              Continue with {connecting === "google" ? "Google" : "Microsoft"}
            </h1>
            <p className="mt-2 max-w-xs text-sm text-muted-foreground">
              Choose your account in the {connecting === "google" ? "Google" : "Microsoft"} window —
              it'll bring you right back here.
            </p>
          </div>

          <div className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            Waiting for {connecting === "google" ? "Google" : "Microsoft"}…
          </div>

          {error && (
            <p className="text-center text-sm text-destructive" role="alert">
              {error}
            </p>
          )}

          <button
            type="button"
            onClick={handleBack}
            className="block w-full text-center text-sm font-medium text-brand-purple hover:underline"
          >
            Use a different sign-in option
          </button>
        </div>
      ) : /* ---- No account yet — warm "let's get you started" prompt ---- */
      mode === "no_account" ? (
        <div className="space-y-7 duration-200 animate-in fade-in">
          <div className="flex flex-col items-center text-center">
            <span className="mb-6 flex h-[4.5rem] w-[4.5rem] items-center justify-center rounded-[1.25rem] bg-gradient-to-br from-primary to-[oklch(0.55_0.18_280)] text-primary-foreground shadow-lg shadow-primary/30">
              <Rocket className="h-9 w-9" />
            </span>
            <h1 className="max-w-[18rem] text-[1.75rem] font-semibold leading-[1.15] text-foreground">
              Looks like you don't have an account yet
            </h1>
            <p className="mt-3.5 max-w-[19rem] text-[15px] leading-relaxed text-muted-foreground">
              No problem — pick a plan and you'll be up and running in about a minute.
            </p>
          </div>

          <Button
            type="button"
            onClick={() => {
              window.location.href = PLANS_URL;
            }}
            className="h-11 w-full gap-2 text-[15px] font-semibold"
          >
            View plans
            <ArrowRight className="h-[18px] w-[18px]" />
          </Button>

          <button
            type="button"
            onClick={handleBack}
            className="block w-full text-center text-[13.5px] font-medium text-brand-purple hover:underline"
          >
            Back to sign in
          </button>
        </div>
      ) : /* ---- 2FA challenge ---- */
      mode === "twofa" ? (
        challengeMethod === "passkey" ? (
          <div className="space-y-5">
            <div>
              <h1 className="text-2xl font-semibold text-foreground">Verify it's you</h1>
              <p className="mt-1 text-sm text-muted-foreground">
                Use your passkey to finish signing in.
              </p>
            </div>
            {error && (
              <p className="text-center text-sm text-destructive" role="alert">
                {error}
              </p>
            )}
            <Button
              type="button"
              onClick={() => void verifyChallengeWithPasskey()}
              disabled={pkBusy}
              className="h-9 w-full"
            >
              <Fingerprint className="mr-2 h-4 w-4" />
              {pkBusy ? "Waiting for passkey…" : "Verify with passkey"}
            </Button>
            <button
              type="button"
              onClick={backToSignIn}
              className="block w-full text-center text-sm text-muted-foreground hover:text-foreground"
            >
              Back to sign in
            </button>
          </div>
        ) : (
          <form onSubmit={verify2fa} className="space-y-5">
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Enter your code</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Two-factor authentication is on for this account.
            </p>
          </div>

          {useRecovery ? (
            <div className="space-y-2">
              <Label htmlFor="code">Recovery code</Label>
              <Input
                id="code"
                autoFocus
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="abcd-efgh"
                className={`${fieldCls} text-center font-mono tracking-widest`}
              />
            </div>
          ) : (
            <div className="space-y-3">
              <Label className="block text-center">6-digit code</Label>
              <OtpInput value={code} onChange={setCode} autoFocus />
            </div>
          )}

          <label className="flex cursor-pointer items-center justify-center gap-2 text-[13px] text-muted-foreground">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => setRemember(e.target.checked)}
              className={cn(checkboxCls, "cursor-pointer")}
            />
            Trust this device for 30 days
          </label>

          {error && (
            <p className="text-center text-sm text-destructive" role="alert">
              {error}
            </p>
          )}

          <Button
            type="submit"
            disabled={busy || (useRecovery ? code.trim().length < 4 : code.length < 6)}
            className="h-9 w-full"
          >
            {busy ? "Verifying…" : "Verify & sign in"}
          </Button>
          <div className="flex items-center justify-between text-sm">
            <button type="button" onClick={backToSignIn} className="text-muted-foreground hover:text-foreground">
              Back to sign in
            </button>
            <button
              type="button"
              onClick={() => {
                setUseRecovery((v) => !v);
                setCode("");
                setError(null);
              }}
              className="text-brand-purple hover:underline"
            >
              {useRecovery ? "Use authenticator code" : "Use a recovery code"}
            </button>
          </div>
        </form>
        )
      ) : mode === "forgot" ? (
        /* ---- Forgot password ---- */
        <div className="space-y-5">
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Reset your password</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {forgotSent
                ? "Check your inbox."
                : "Enter your email and we'll send you a reset link."}
            </p>
          </div>

          {forgotSent ? (
            <>
              <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3 text-sm text-muted-foreground">
                If an account exists for{" "}
                <span className="font-medium text-foreground">{forgotEmail}</span>, a password-reset
                link is on its way. It expires in 30 minutes.
              </div>
              <Button type="button" onClick={backToSignIn} className="h-9 w-full">
                Back to sign in
              </Button>
            </>
          ) : (
            <form onSubmit={submitForgot} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="forgot-email">Email</Label>
                <Input
                  id="forgot-email"
                  type="email"
                  autoFocus
                  required
                  value={forgotEmail}
                  onChange={(e) => setForgotEmail(e.target.value)}
                  placeholder=""
                  className={fieldCls}
                />
              </div>
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button type="submit" disabled={busy} className="h-9 w-full">
                {busy ? "Sending…" : "Send reset link"}
              </Button>
              <button
                type="button"
                onClick={backToSignIn}
                className="block w-full text-center text-sm text-muted-foreground hover:text-foreground"
              >
                Back to sign in
              </button>
            </form>
          )}
        </div>
      ) : mode === "password" ? (
        /* ---- Step 2: password (slides in after email) ---- */
        <form
          key="password"
          onSubmit={onSubmit}
          className="space-y-5 duration-200 animate-in fade-in slide-in-from-right-4"
        >
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Enter your password</h1>
            <p className="mt-1 truncate text-sm text-muted-foreground" title={email}>
              Signing in as <span className="font-medium text-foreground">{email}</span>
            </p>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label htmlFor="password">Password</Label>
              <button
                type="button"
                onClick={() => {
                  setForgotEmail(email);
                  setForgotSent(false);
                  setError(null);
                  setMode("forgot");
                }}
                className="text-[12px] text-brand-purple hover:underline"
              >
                Forgot your password?
              </button>
            </div>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              required
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className={fieldCls}
            />
          </div>

          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}

          <Button type="submit" disabled={busy} className="h-9 w-full">
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      ) : (
        /* ---- Step 1: identifier — providers first, then email ---- */
        <div key="identifier" className="space-y-5 duration-200 animate-in fade-in slide-in-from-left-4">
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Sign in to your account</h1>
            <p className="mt-1 text-sm text-muted-foreground">Welcome back. Let's get you in.</p>
          </div>

          {notice && !error && (
            <p className="rounded-md bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700" role="status">
              {notice}
            </p>
          )}
          {error && (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          )}

          <div className="space-y-2.5">
            <button
              type="button"
              onClick={() => startProvider("google")}
              className="flex h-10 w-full items-center justify-center gap-2.5 rounded-md border border-foreground/25 text-sm font-medium text-foreground transition-colors hover:bg-muted"
            >
              <GoogleIcon className="h-[18px] w-[18px]" />
              Continue with Google
            </button>
            <button
              type="button"
              onClick={() => startProvider("microsoft")}
              className="flex h-10 w-full items-center justify-center gap-2.5 rounded-md border border-foreground/25 text-sm font-medium text-foreground transition-colors hover:bg-muted"
            >
              <MicrosoftIcon className="h-[18px] w-[18px]" />
              Continue with Microsoft
            </button>
          </div>

          <div className="flex items-center gap-3 text-[11px] uppercase tracking-wide text-muted-foreground">
            <span className="h-px flex-1 bg-border" />
            or
            <span className="h-px flex-1 bg-border" />
          </div>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (email.trim()) {
                setError(null);
                setMode("password");
              }
            }}
            className="space-y-4"
          >
            <div className="space-y-2">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder=""
                className={fieldCls}
              />
            </div>
            <Button type="submit" className="h-9 w-full">
              Continue
            </Button>
          </form>

          {pkAvailable && (
            <button
              type="button"
              onClick={() => void signInWithPasskey()}
              disabled={pkBusy}
              className="block w-full text-center text-sm text-muted-foreground transition-colors hover:text-foreground"
            >
              <span className="inline-flex items-center gap-1.5">
                <Fingerprint className="h-4 w-4" />
                {pkBusy ? "Waiting for passkey…" : "Sign in with a passkey"}
              </span>
            </button>
          )}

          <p className="text-center text-sm text-muted-foreground">
            Don't have an account?{" "}
            <Link to="/signup" className="font-medium text-brand-purple hover:underline">
              Sign up
            </Link>
          </p>
        </div>
      )}
    </AuthLayout>
  );
}

// MicrosoftIcon — the four-square Microsoft glyph (Continue with Microsoft).
function MicrosoftIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <path fill="#F25022" d="M3 3h8.5v8.5H3z" />
      <path fill="#7FBA00" d="M12.5 3H21v8.5h-8.5z" />
      <path fill="#00A4EF" d="M3 12.5h8.5V21H3z" />
      <path fill="#FFB900" d="M12.5 12.5H21V21h-8.5z" />
    </svg>
  );
}

// GoogleIcon — the multi-color Google "G" used on the Continue-with-Google button.
function GoogleIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true">
      <path fill="#4285F4" d="M22.5 12.2c0-.7-.06-1.4-.18-2.06H12v3.9h5.9a5.05 5.05 0 0 1-2.19 3.32v2.76h3.54c2.07-1.9 3.25-4.71 3.25-7.92z" />
      <path fill="#34A853" d="M12 23c2.95 0 5.43-.98 7.24-2.64l-3.54-2.76c-.98.66-2.24 1.05-3.7 1.05-2.85 0-5.26-1.92-6.12-4.5H2.23v2.85A11 11 0 0 0 12 23z" />
      <path fill="#FBBC05" d="M5.88 14.15a6.6 6.6 0 0 1 0-4.2V7.1H2.23a11 11 0 0 0 0 9.9z" />
      <path fill="#EA4335" d="M12 5.4c1.6 0 3.05.55 4.18 1.63l3.14-3.14A10.97 10.97 0 0 0 12 1 11 11 0 0 0 2.23 7.1l3.65 2.85C6.74 7.32 9.15 5.4 12 5.4z" />
    </svg>
  );
}
