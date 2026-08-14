import { FormEvent, useEffect, useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { Loader2, Building2, User as UserIcon, AlertCircle } from "lucide-react";
import { TrilliLogo } from "@/components/Logo";
import { api, ApiError, type Identity } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { SignupSteps } from "@/components/SignupSteps";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn, checkboxCls } from "@/lib/utils";
import PasswordStrength from "@/components/PasswordStrength";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

type IntentState = {
  email: string;
  plan_code: string;
  billing_cycle: string;
  status: string;
  oauth_provider?: string;
  full_name?: string;
  // Complimentary ("ambassador") invites share this registration page; when
  // is_comp is set there's no payment/billing and the copy reflects a free term.
  is_comp?: boolean;
  comp_free_term_days?: number;
};

const PLAN_NAMES: Record<string, string> = { lite: "Lite", plus: "Plus", infinity: "Infinity" };

// Visible 1px outline on registration inputs (the --input token is white, so the
// primitive's default border is invisible on the white card).
const inputCls = "border-foreground/25";

// SignupComplete is Step 6: payment is secured, so we collect the final account
// details (account type, name, password, billing address) and provision +
// log the user in.
export default function SignupComplete() {
  const { token = "" } = useParams();
  const navigate = useNavigate();
  const { setIdentity } = useAuth();

  const [loading, setLoading] = useState(true);
  const [intent, setIntent] = useState<IntentState | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [accountType, setAccountType] = useState<"individual" | "business">("individual");
  const [businessName, setBusinessName] = useState("");
  const [firstName, setFirstName] = useState("");
  const [lastName, setLastName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [addr, setAddr] = useState({
    line1: "", line2: "", city: "", state: "", postal_code: "", country: "",
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Terms acceptance is pre-checked for both paid and comp sign-ups.
  const [agreeTos, setAgreeTos] = useState(true);

  useEffect(() => {
    (async () => {
      try {
        const res = await api.get<IntentState>(`/api/signup/intent?token=${encodeURIComponent(token)}`);
        setIntent(res);
        // OAuth signups arrive with a verified name from the provider — prefill it.
        if (res.oauth_provider && res.full_name) {
          const parts = res.full_name.trim().split(/\s+/);
          setFirstName(parts[0] ?? "");
          setLastName(parts.slice(1).join(" "));
        }
      } catch {
        setLoadError("We couldn't find this signup. Please start again.");
      } finally {
        setLoading(false);
      }
    })();
  }, [token]);

  const oauth = !!intent?.oauth_provider;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!agreeTos) {
      setError("Please accept the Terms of Service to continue.");
      return;
    }
    if (!oauth) {
      if (password !== confirm) {
        setError("Passwords don't match.");
        return;
      }
      if (password.length < 8) {
        setError("Password must be at least 8 characters.");
        return;
      }
    }
    setBusy(true);
    try {
      const id = await api.post<Identity>("/api/signup/complete", {
        token,
        account_type: accountType,
        business_name: businessName,
        first_name: firstName,
        last_name: lastName,
        password: oauth ? "" : password,
        confirm_password: oauth ? "" : confirm,
        address: addr,
      });
      setIdentity(id);
      navigate("/home", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Could not create your account" : "Network error");
    } finally {
      setBusy(false);
    }
  }

  if (loading) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30">
        <Loader2 className="h-8 w-8 animate-spin text-brand-purple" />
      </div>
    );
  }

  if (loadError || !intent) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <AlertCircle className="h-10 w-10 text-destructive" />
            <p className="text-sm text-muted-foreground">{loadError ?? "Not found."}</p>
            <Link to="/signup" className="text-sm text-brand-purple hover:underline">Start again</Link>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (intent.status === "completed") {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <p className="text-[15px] font-medium text-foreground">This account is already set up.</p>
            <Link to="/login" className="text-sm text-brand-purple hover:underline">Sign in</Link>
          </CardContent>
        </Card>
      </div>
    );
  }

  const isComp = !!intent.is_comp;

  // A comp invite is ready only while live (email_verified); an expired/revoked
  // link is dead. A paid funnel intent needs payment secured first.
  if (isComp && intent.status !== "email_verified") {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <AlertCircle className="h-10 w-10 text-amber-500" />
            <p className="text-sm text-muted-foreground">This invite has expired or already been used.</p>
            <Link to="/login" className="text-sm text-brand-purple hover:underline">Go to sign in</Link>
          </CardContent>
        </Card>
      </div>
    );
  }
  if (!isComp && intent.status !== "paid") {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
        <Card className="w-full max-w-md">
          <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
            <AlertCircle className="h-10 w-10 text-amber-500" />
            <p className="text-sm text-muted-foreground">
              Your payment hasn't completed yet. If you just paid, give it a moment and refresh.
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  const planLabel = PLAN_NAMES[intent.plan_code] ?? intent.plan_code;
  const year = new Date().getFullYear();
  const compDays = intent.comp_free_term_days ?? 0;
  const compTerm = compDays <= 0 ? "free, with no expiry" : `free for ${compDays} days`;

  return (
    <div className="flex signup-fullscreen flex-col items-center bg-muted/30 px-4 py-10">
      {!isComp && (
        <div className="mb-9 w-full max-w-xl">
          <SignupSteps current={3} oauth={oauth} />
        </div>
      )}
      <Card className="w-full max-w-lg">
        <CardHeader className="text-center">
          <CardTitle className="flex justify-center">
            <TrilliLogo className="h-[34px] text-brand-purple" />
          </CardTitle>
          <CardDescription>
            {isComp
              ? `Set up your complimentary ${planLabel} account — ${compTerm}, no payment required`
              : `Payment received — finish setting up your ${planLabel} account`}
          </CardDescription>
        </CardHeader>
        <form onSubmit={submit}>
          <CardContent className="space-y-5">
            {/* Account type */}
            <div className="grid grid-cols-2 gap-3">
              {(["individual", "business"] as const).map((t) => {
                const Icon = t === "business" ? Building2 : UserIcon;
                const active = accountType === t;
                return (
                  <button
                    key={t}
                    type="button"
                    onClick={() => setAccountType(t)}
                    className={cn(
                      "flex items-center gap-2.5 rounded-lg border p-3 text-left transition-colors",
                      active ? "border-primary bg-primary/5 ring-1 ring-primary" : "border-border hover:bg-muted",
                    )}
                  >
                    <span className={cn("flex h-8 w-8 items-center justify-center rounded-lg", active ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")}>
                      <Icon className="h-[18px] w-[18px]" />
                    </span>
                    <span className="text-sm font-medium capitalize text-foreground">{t}</span>
                  </button>
                );
              })}
            </div>

            {accountType === "business" && (
              <div className="space-y-2">
                <Label htmlFor="businessName">Company name</Label>
                <Input id="businessName" required value={businessName} onChange={(e) => setBusinessName(e.target.value)} placeholder="Acme Inc." className={inputCls} />
              </div>
            )}

            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label htmlFor="firstName">First name</Label>
                <Input id="firstName" required value={firstName} onChange={(e) => setFirstName(e.target.value)} className={inputCls} />
              </div>
              <div className="space-y-2">
                <Label htmlFor="lastName">Last name</Label>
                <Input id="lastName" required value={lastName} onChange={(e) => setLastName(e.target.value)} className={inputCls} />
              </div>
            </div>

            <div className="space-y-2">
              <Label>Email</Label>
              <Input value={intent.email} readOnly disabled className={`${inputCls} opacity-70`} />
            </div>

            {oauth ? (
              <p className="rounded-lg border border-border bg-muted/40 px-3 py-2 text-[13px] text-muted-foreground">
                Signed in with Google — no password needed. You can add one later from Settings.
              </p>
            ) : (
              <>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-2">
                    <Label htmlFor="password">Password</Label>
                    <Input id="password" type="password" autoComplete="new-password" required minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} className={inputCls} />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="confirm">Confirm password</Label>
                    <Input id="confirm" type="password" autoComplete="new-password" required minLength={8} value={confirm} onChange={(e) => setConfirm(e.target.value)} className={inputCls} />
                  </div>
                </div>
                {password ? <PasswordStrength password={password} /> : <p className="text-xs text-muted-foreground">Minimum 8 characters.</p>}
              </>
            )}

            {/* Billing address — only for paid sign-ups; comp accounts have no card. */}
            {!isComp && (
            <div className="space-y-3 rounded-lg border border-border p-3">
              <p className="text-[13px] font-semibold text-foreground">Billing address</p>
              <Input placeholder="Address" value={addr.line1} onChange={(e) => setAddr({ ...addr, line1: e.target.value })} className={inputCls} />
              <Input placeholder="Apartment, suite, etc. (optional)" value={addr.line2} onChange={(e) => setAddr({ ...addr, line2: e.target.value })} className={inputCls} />
              <div className="grid grid-cols-2 gap-3">
                <Input placeholder="City" value={addr.city} onChange={(e) => setAddr({ ...addr, city: e.target.value })} className={inputCls} />
                <Input placeholder="State / Region" value={addr.state} onChange={(e) => setAddr({ ...addr, state: e.target.value })} className={inputCls} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Input placeholder="Postal code" value={addr.postal_code} onChange={(e) => setAddr({ ...addr, postal_code: e.target.value })} className={inputCls} />
                <Input placeholder="Country" value={addr.country} onChange={(e) => setAddr({ ...addr, country: e.target.value })} className={inputCls} />
              </div>
            </div>
            )}

            {/* Terms of Service — pre-checked, required (paid + complimentary). */}
            <label className="flex items-start gap-2.5 rounded-lg border border-border bg-muted/30 p-3 text-[13px] leading-relaxed text-muted-foreground">
              <input
                type="checkbox"
                checked={agreeTos}
                onChange={(e) => setAgreeTos(e.target.checked)}
                className={cn(checkboxCls, "mt-0.5 cursor-pointer")}
              />
              <span>
                I agree to Trilli's{" "}
                <a href="/terms" target="_blank" rel="noreferrer" className="font-medium text-brand-purple hover:underline">
                  Terms of Service
                </a>{" "}
                and{" "}
                <a href="/privacy" target="_blank" rel="noreferrer" className="font-medium text-brand-purple hover:underline">
                  Privacy Policy
                </a>
                .
              </span>
            </label>

            {error && <p className="text-sm text-destructive" role="alert">{error}</p>}

            <Button type="submit" disabled={busy || !agreeTos} className="w-full gap-2">
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {busy ? "Creating your account…" : "Create account & enter Trilli"}
            </Button>
          </CardContent>
        </form>
      </Card>

      {/* Anchored copyright footer. */}
      <footer className="mt-auto pt-10 text-center text-[13px] text-muted-foreground">
        © {year} Trilli Media LLC. All rights reserved.
      </footer>
    </div>
  );
}
