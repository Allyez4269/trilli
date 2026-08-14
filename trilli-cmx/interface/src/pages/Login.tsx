import { type FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Loader2, ShieldCheck, KeyRound, Copy, Check } from "lucide-react";

import { api, ApiError, type BeginLoginResult, type TOTPSetup } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import AuthLayout from "@/components/AuthLayout";
import OtpInput from "@/components/OtpInput";

const fieldCls = "h-8 border-foreground/25";

// CMX login is email + password + MANDATORY TOTP 2FA (SPEC §5.3, §6.9). No
// OAuth/passkey. First login forces authenticator enrollment before a session
// is issued; operator password resets happen via another Global admin, so there
// is no self-service "forgot password" here.
type Mode = "credentials" | "twofa" | "enroll" | "recovery_codes";

export default function Login() {
  const navigate = useNavigate();
  const { refresh } = useAuth();

  const [mode, setMode] = useState<Mode>("credentials");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [challengeId, setChallengeId] = useState("");
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [copied, setCopied] = useState(false);

  async function finishAndEnter() {
    await refresh();
    navigate("/", { replace: true });
  }

  async function onCredentials(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await api.post<BeginLoginResult>("/api/cmx/login", { email, password });
      setChallengeId(res.challenge_id);
      setCode("");
      if (res.stage === "enroll") {
        const s = await api.post<TOTPSetup>("/api/cmx/login/2fa/setup", {
          challenge_id: res.challenge_id,
        });
        setSetup(s);
        setMode("enroll");
      } else {
        setMode("twofa");
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Network error");
    } finally {
      setBusy(false);
    }
  }

  async function onVerify(codeVal: string) {
    if (busy) return;
    setError(null);
    setBusy(true);
    try {
      await api.post("/api/cmx/login/2fa", { challenge_id: challengeId, code: codeVal.replace(/\s/g, "") });
      await finishAndEnter();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Network error");
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  async function onEnrollConfirm(codeVal: string) {
    if (busy) return;
    setError(null);
    setBusy(true);
    try {
      const res = await api.post<{ ok: boolean; recovery_codes: string[] }>(
        "/api/cmx/login/2fa/enroll",
        { challenge_id: challengeId, code: codeVal.replace(/\s/g, "") },
      );
      setRecoveryCodes(res.recovery_codes ?? []);
      setMode("recovery_codes");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Network error");
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  function copyCodes() {
    void navigator.clipboard?.writeText(recoveryCodes.join("\n"));
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  function backToStart() {
    setMode("credentials");
    setCode("");
    setRecovery(false);
    setError(null);
    setSetup(null);
  }

  // ---- Recovery-codes hand-off (after enrollment) ----
  if (mode === "recovery_codes") {
    return (
      <AuthLayout>
        <div className="space-y-5">
          <div className="flex flex-col items-center text-center">
            <span className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <KeyRound className="h-6 w-6" />
            </span>
            <h1 className="text-2xl font-semibold text-foreground">Save your recovery codes</h1>
            <p className="mt-1.5 text-sm text-muted-foreground">
              Each code works once if you lose your authenticator. Store them somewhere safe — they
              won't be shown again.
            </p>
          </div>
          <div className="grid grid-cols-2 gap-2 rounded-lg border border-border bg-muted/40 p-4 font-mono text-sm">
            {recoveryCodes.map((c) => (
              <span key={c} className="text-center text-foreground">
                {c}
              </span>
            ))}
          </div>
          <Button type="button" variant="outline" onClick={copyCodes} className="h-8 w-full gap-2">
            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            {copied ? "Copied" : "Copy codes"}
          </Button>
          <Button type="button" onClick={() => void finishAndEnter()} className="h-8 w-full">
            I've saved them — continue
          </Button>
        </div>
      </AuthLayout>
    );
  }

  // ---- Mandatory enrollment (first login) ----
  if (mode === "enroll") {
    return (
      <AuthLayout onBack={backToStart}>
        <div className="space-y-5">
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Set up two-factor</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              CMX requires an authenticator app. Scan the code, then enter the 6-digit code to
              finish.
            </p>
          </div>
          {setup && (
            <div className="flex flex-col items-center gap-3">
              <img
                src={setup.qr_data_url}
                alt="Authenticator QR code"
                className="h-44 w-44 rounded-lg border border-border bg-card p-2"
              />
              <code className="break-all rounded bg-muted px-2 py-1 text-center text-xs text-muted-foreground">
                {setup.secret}
              </code>
            </div>
          )}
          <div className="space-y-3">
            <Label className="block text-center">6-digit code</Label>
            <OtpInput value={code} onChange={setCode} autoFocus disabled={busy} onComplete={(v) => void onEnrollConfirm(v)} />
          </div>
          {busy && (
            <p className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> Verifying…
            </p>
          )}
          {error && (
            <p className="text-center text-sm text-destructive" role="alert">
              {error}
            </p>
          )}
        </div>
      </AuthLayout>
    );
  }

  // ---- 2FA challenge (enrolled) ----
  if (mode === "twofa") {
    return (
      <AuthLayout onBack={backToStart}>
        <div className="space-y-5">
          <div className="flex flex-col items-center text-center">
            <span className="mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <ShieldCheck className="h-6 w-6" />
            </span>
            <h1 className="text-2xl font-semibold text-foreground">Enter your code</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Two-factor is required for every operator.
            </p>
          </div>
          {recovery ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void onVerify(code);
              }}
              className="space-y-3"
            >
              <Label htmlFor="code" className="block text-center">
                Recovery code
              </Label>
              <Input
                id="code"
                autoFocus
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="abcd-efgh"
                className={`${fieldCls} text-center font-mono tracking-widest`}
              />
              <Button type="submit" disabled={busy || code.trim().length < 4} className="h-8 w-full">
                {busy ? "Verifying…" : "Verify & sign in"}
              </Button>
            </form>
          ) : (
            <div className="space-y-3">
              <Label className="block text-center">6-digit code</Label>
              <OtpInput value={code} onChange={setCode} autoFocus disabled={busy} onComplete={(v) => void onVerify(v)} />
              {busy && (
                <p className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" /> Verifying…
                </p>
              )}
            </div>
          )}
          {error && (
            <p className="text-center text-sm text-destructive" role="alert">
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
            className="block w-full text-center text-[13px] text-muted-foreground transition-colors hover:text-foreground"
          >
            {recovery ? "Use your authenticator code" : "Use a recovery code instead"}
          </button>
        </div>
      </AuthLayout>
    );
  }

  // ---- Step 1: email + password ----
  return (
    <AuthLayout>
      <form onSubmit={onCredentials} className="space-y-5">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Sign in</h1>
          <p className="mt-1 text-sm text-muted-foreground">Operator access to Trilli CMX.</p>
        </div>
        <div className="space-y-2">
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            autoComplete="username"
            autoFocus
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className={fieldCls}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            required
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
        <Button type="submit" disabled={busy} className="h-8 w-full gap-2">
          {busy && <Loader2 className="h-4 w-4 animate-spin" />}
          {busy ? "Signing in…" : "Continue"}
        </Button>
      </form>
    </AuthLayout>
  );
}
