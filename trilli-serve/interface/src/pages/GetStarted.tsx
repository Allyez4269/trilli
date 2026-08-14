import { useEffect, useState } from "react";
import { Navigate, useNavigate, Link } from "react-router-dom";
import { Check, Loader2, ArrowLeft } from "lucide-react";
import { api, ApiError, type CatalogPlan, type PlansResponse } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { TrilliLogo } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// GetStarted is the authenticated "buy your own account" entry: an existing
// user (often a member of someone else's workspace) picks a plan, then is sent
// into the shared Stripe checkout. After payment a NEW tenant is provisioned
// for them (reusing their identity) and their session switches into it.
export default function GetStarted() {
  const { identity, loading } = useAuth();
  const navigate = useNavigate();
  const [plans, setPlans] = useState<CatalogPlan[]>([]);
  const [discount, setDiscount] = useState(0);
  const [annual, setAnnual] = useState(true);
  const [starting, setStarting] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void api
      .get<PlansResponse>("/api/plans")
      .then((r) => {
        if (cancelled) return;
        setPlans(r.plans);
        setDiscount(r.plans[0]?.annual_discount_pct ?? 0);
      })
      .catch(() => !cancelled && setError("Couldn't load plans. Please try again."));
    return () => {
      cancelled = true;
    };
  }, []);

  if (loading) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30">
        <Loader2 className="h-8 w-8 animate-spin text-primary" />
      </div>
    );
  }
  if (!identity) return <Navigate to="/login" replace />;

  const choose = async (code: string) => {
    if (starting) return;
    setStarting(code);
    setError(null);
    try {
      const r = await api.post<{ token: string }>("/api/account/new", {
        plan_code: code,
        billing_cycle: annual ? "annual" : "monthly",
      });
      navigate(`/signup/checkout/${r.token}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Couldn't start checkout." : "Network error.");
      setStarting(null);
    }
  };

  const ownsAccount = (identity.tenants ?? []).some((t) => t.is_owner);

  return (
    <div className="signup-fullscreen bg-muted/30 px-4 py-10">
      <div className="mx-auto max-w-5xl">
        <div className="mb-8 flex flex-col items-center text-center">
          <TrilliLogo className="h-[30px] text-primary" />
          <h1 className="mt-4 text-3xl font-bold text-foreground">Create your own Trilli account</h1>
          <p className="mt-2 max-w-xl text-[15px] text-muted-foreground">
            {ownsAccount
              ? "Spin up a separate account with its own storage, billing, and members."
              : "You currently have access through another account. Start your own — your existing access stays exactly as it is."}
          </p>

          {/* Billing toggle */}
          <div className="mt-6 inline-flex items-center rounded-xl border border-border bg-card p-1 text-sm font-semibold shadow-sm">
            <button
              onClick={() => setAnnual(true)}
              className={cn("rounded-lg px-4 py-2 transition-colors", annual ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground")}
            >
              Annual{discount > 0 ? ` · save ${discount}%` : ""}
            </button>
            <button
              onClick={() => setAnnual(false)}
              className={cn("rounded-lg px-4 py-2 transition-colors", !annual ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground")}
            >
              Monthly
            </button>
          </div>
        </div>

        {error && (
          <p className="mx-auto mb-6 max-w-md rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-center text-[13px] text-destructive">
            {error}
          </p>
        )}

        <div className="grid gap-5 lg:grid-cols-3">
          {plans.map((p) => {
            const perMonth = annual ? p.price_monthly * (1 - p.annual_discount_pct / 100) : p.price_monthly;
            return (
              <div
                key={p.code}
                className={cn(
                  "relative flex h-full flex-col rounded-2xl border bg-card p-6 shadow-sm",
                  p.popular ? "border-primary ring-1 ring-primary" : "border-border",
                )}
              >
                {p.popular && (
                  <span className="absolute -top-3 left-6 rounded-full bg-primary px-3 py-1 text-[11px] font-bold uppercase tracking-wide text-primary-foreground">
                    Most popular
                  </span>
                )}
                <h3 className="text-lg font-bold text-foreground">{p.name}</h3>
                {p.tagline && <p className="mt-1 text-[13px] text-muted-foreground">{p.tagline}</p>}
                <div className="mt-4 flex items-baseline gap-1.5">
                  <span className="text-3xl font-bold tabular-nums text-foreground">${perMonth.toFixed(2)}</span>
                  <span className="text-[14px] text-muted-foreground">/mo</span>
                </div>
                <p className="mt-1 text-[12px] text-muted-foreground">
                  {annual ? "billed annually" : "billed monthly"}
                </p>
                <ul className="mt-5 flex-1 space-y-2 text-[13.5px] text-foreground">
                  {p.features.map((f) => (
                    <li key={f} className="flex gap-2">
                      <Check className="mt-0.5 h-4 w-4 flex-shrink-0 text-emerald-600" />
                      <span className="leading-snug">{f}</span>
                    </li>
                  ))}
                </ul>
                <Button
                  onClick={() => void choose(p.code)}
                  disabled={starting !== null}
                  className={cn("mt-6 h-11 w-full text-[14px] font-semibold", !p.popular && "bg-foreground/90 hover:bg-foreground")}
                >
                  {starting === p.code ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> Starting…</> : `Choose ${p.name}`}
                </Button>
              </div>
            );
          })}
        </div>

        <div className="mt-8 text-center">
          <Link to="/home" className="inline-flex items-center gap-1.5 text-[13px] text-muted-foreground hover:text-foreground">
            <ArrowLeft className="h-3.5 w-3.5" /> Back to my workspace
          </Link>
        </div>
      </div>
    </div>
  );
}
