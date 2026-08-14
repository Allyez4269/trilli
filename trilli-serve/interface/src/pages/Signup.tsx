import { FormEvent, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Mail, Loader2, CheckCircle2 } from "lucide-react";
import { TrilliLogo } from "@/components/Logo";
import { api, ApiError } from "@/lib/api";
import { readFlash } from "@/lib/flash";
import { continueWithGoogle, continueWithMicrosoft } from "@/lib/googlePopup";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

// GoogleIcon / MicrosoftIcon — brand glyphs for the (currently disabled)
// social sign-up buttons. Rendered muted while "coming soon".
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

// SocialButton: an enabled provider button when `onClick` is given (opens the
// Google account-picker popup), or the muted "Soon" stub otherwise.
function SocialButton({
  icon,
  label,
  onClick,
}: {
  icon: React.ReactNode;
  label: string;
  onClick?: () => void;
}) {
  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        className="flex w-full items-center justify-center gap-2.5 rounded-lg border border-border bg-card px-4 py-2.5 text-sm font-medium text-foreground transition-colors hover:bg-muted"
      >
        {icon}
        {label}
      </button>
    );
  }
  return (
    <button
      type="button"
      disabled
      className="relative flex w-full cursor-not-allowed items-center justify-center gap-2.5 rounded-lg border border-border bg-card px-4 py-2.5 text-sm font-medium text-foreground opacity-60"
    >
      <span className="grayscale">{icon}</span>
      {label}
      <span className="absolute right-3 rounded-full bg-muted px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
        Soon
      </span>
    </button>
  );
}


const PLAN_NAMES: Record<string, string> = { lite: "Lite", plus: "Plus", infinity: "Infinity" };

export default function Signup() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const plan = params.get("plan") ?? "";
  const billing = params.get("billing") === "monthly" ? "monthly" : "annual";

  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  // One-time notice from a server redirect (e.g. "pick a plan to finish your
  // Google sign-up") — read from a flash cookie, not a ?query= param.
  const [flash] = useState<string | null>(() => readFlash());

  const planLabel = PLAN_NAMES[plan] ?? (plan ? plan[0].toUpperCase() + plan.slice(1) : "");

  async function startEmail(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const res = await api.post<{ ok?: boolean; registered?: boolean }>("/api/signup/start", {
        email: email.trim(),
        plan,
        billing,
      });
      if (res.registered) {
        navigate("/login", { replace: true });
        return;
      }
      setSent(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Something went wrong" : "Network error");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 px-4 py-10">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <CardTitle className="flex justify-center">
            <TrilliLogo className="h-[34px] text-brand-purple" />
          </CardTitle>
          <CardDescription>
            {sent
              ? "Check your inbox to continue"
              : planLabel
                ? `Get Trilli — ${planLabel} plan, billed ${billing === "annual" ? "annually" : "monthly"}`
                : "Get Trilli"}
          </CardDescription>
        </CardHeader>

        {sent ? (
          <CardContent className="flex flex-col items-center gap-3 py-4 text-center">
            <CheckCircle2 className="h-12 w-12 text-emerald-500" />
            <p className="text-[15px] font-medium text-foreground">Confirm your email</p>
            <p className="text-sm text-muted-foreground">
              We sent a confirmation link to <span className="font-medium text-foreground">{email}</span>.
              Just follow the instructions in the email to finish setting up your account.
            </p>
            <button
              type="button"
              onClick={() => setSent(false)}
              className="mt-2 text-sm text-brand-purple hover:underline"
            >
              Use a different email
            </button>
          </CardContent>
        ) : (
          <CardContent className="space-y-3">
            {flash && (
              <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[13px] text-amber-700">
                {flash}
              </div>
            )}
            <SocialButton
              icon={<GoogleIcon className="h-[18px] w-[18px]" />}
              label="Continue with Google"
              onClick={() =>
                continueWithGoogle({ mode: "signup", plan: plan || undefined, cycle: billing, onError: setError })
              }
            />
            <SocialButton
              icon={<MicrosoftIcon className="h-[18px] w-[18px]" />}
              label="Continue with Microsoft"
              onClick={() =>
                continueWithMicrosoft({ mode: "signup", plan: plan || undefined, cycle: billing, onError: setError })
              }
            />

            <div className="flex items-center gap-3 py-1">
              <span className="h-px flex-1 bg-border" />
              <span className="text-[11px] uppercase tracking-wide text-muted-foreground">or</span>
              <span className="h-px flex-1 bg-border" />
            </div>

            <form onSubmit={startEmail} className="space-y-3">
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
                />
              </div>
              {error && (
                <p className="text-sm text-destructive" role="alert">
                  {error}
                </p>
              )}
              <Button type="submit" disabled={busy} className="w-full gap-2">
                {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Mail className="h-4 w-4" />}
                {busy ? "Sending…" : "Continue with email"}
              </Button>
            </form>
          </CardContent>
        )}

        <CardFooter className="flex flex-col gap-2">
          <p className="text-center text-sm text-muted-foreground">
            Already have an account?{" "}
            <Link to="/login" className="text-brand-purple hover:underline">
              Sign in
            </Link>
          </p>
        </CardFooter>
      </Card>
    </div>
  );
}
