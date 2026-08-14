import { useEffect, useMemo, useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { loadStripe, type Stripe } from "@stripe/stripe-js";
import { Elements, PaymentElement, useElements, useStripe } from "@stripe/react-stripe-js";
import { Check, Loader2, Lock, ShieldCheck, AlertCircle } from "lucide-react";

import { api, ApiError, type CatalogPlan, type PlansResponse } from "@/lib/api";
import { STRIPE_APPEARANCE } from "@/lib/stripeTheme";
import { TrilliLogo } from "@/components/Logo";
import { SignupSteps } from "@/components/SignupSteps";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type CheckoutData = {
  status: "ready" | "paid" | "completed";
  client_secret?: string;
  publishable_key?: string;
  amount?: number;
  email?: string;
  plan_code?: string;
  billing_cycle?: string;
  oauth_provider?: string;
  // True when an authenticated user is buying a SECOND account — the post-payment
  // step is /account/new/finish, not the cold-funnel /signup/complete.
  for_existing_user?: boolean;
};

function money(n: number): string {
  return `$${n.toFixed(2)}`;
}

// Left pane — the selected plan, presented like the pricing card (Most Popular
// badge, price, features) but with a non-interactive "Selected plan" marker
// instead of a Choose button.
function SelectedPlanPane({ plan, isAnnual }: { plan: CatalogPlan; isAnnual: boolean }) {
  const perMonth = isAnnual ? plan.price_monthly * (1 - plan.annual_discount_pct / 100) : plan.price_monthly;
  const total = isAnnual ? perMonth * 12 : plan.price_monthly;
  const annualSavings = plan.price_monthly * 12 - perMonth * 12;
  return (
    <aside className="relative flex h-full flex-col rounded-2xl border border-border bg-card p-6 shadow-sm">
      {plan.popular && (
        <span className="absolute -top-3 left-6 rounded-full bg-primary px-3 py-1 text-[11px] font-bold uppercase tracking-wide text-primary-foreground shadow">
          Most popular
        </span>
      )}
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-bold text-foreground">{plan.name}</h2>
        <span className="rounded-full border border-primary/30 bg-primary/10 px-2.5 py-0.5 text-[11px] font-semibold text-primary">
          Selected plan
        </span>
      </div>
      {plan.tagline && <p className="mt-1 text-[13px] text-muted-foreground">{plan.tagline}</p>}

      <div className="mt-5 flex items-baseline gap-1.5">
        <span className="text-4xl font-bold tabular-nums text-foreground">{money(perMonth)}</span>
        <span className="text-[14px] text-muted-foreground">/mo</span>
      </div>
      <p className="mt-1 text-[12.5px] text-muted-foreground">
        {isAnnual ? (
          <>
            billed annually ·{" "}
            <span className="font-medium text-emerald-600">save {plan.annual_discount_pct}%</span>
          </>
        ) : (
          "billed month to month"
        )}
      </p>

      <ul className="mt-6 flex-1 space-y-2.5 text-[13.5px] text-foreground">
        {plan.features.map((f) => (
          <li key={f} className="flex gap-2">
            <Check className="mt-0.5 h-4 w-4 flex-shrink-0 text-emerald-600" />
            <span className="leading-snug">{f}</span>
          </li>
        ))}
      </ul>

      <div className="mt-6 space-y-1.5 border-t border-border pt-4 text-[13px]">
        <div className="flex justify-between text-muted-foreground">
          <span>Total due today{isAnnual ? " (12 mo)" : ""}</span>
          <span className="text-base font-bold tabular-nums text-foreground">{money(total)}</span>
        </div>
        {isAnnual && annualSavings > 0 && (
          <p className="text-[11.5px] font-medium text-emerald-700">
            You're saving {money(annualSavings)} a year vs. monthly.
          </p>
        )}
      </div>
    </aside>
  );
}

// Right pane — the Stripe Payment Element + Pay button.
function PaymentPane({
  token,
  total,
  onPaid,
}: {
  token: string;
  total: number;
  onPaid: () => Promise<void>;
}) {
  const stripe = useStripe();
  const elements = useElements();
  const [paying, setPaying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);

  const pay = async () => {
    if (!stripe || !elements) return;
    setPaying(true);
    setError(null);
    const { error: stripeErr, paymentIntent } = await stripe.confirmPayment({
      elements,
      confirmParams: { return_url: window.location.href },
      redirect: "if_required",
    });
    if (stripeErr) {
      setError(stripeErr.message ?? "Payment couldn't be completed.");
      setPaying(false);
      return;
    }
    if (paymentIntent && paymentIntent.status === "succeeded") {
      try {
        await onPaid();
      } catch {
        setError("Payment went through but we couldn't continue. Check your email for a link to finish.");
        setPaying(false);
      }
      return;
    }
    setPaying(false);
  };

  return (
    <section className="rounded-2xl border border-border bg-card p-6 shadow-sm">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-[15px] font-semibold text-foreground">Payment details</h2>
        <span className="inline-flex items-baseline gap-1">
          <Lock className="h-3 w-3 translate-y-0.5 text-muted-foreground" />
          <span className="text-[10px] text-muted-foreground">Powered by</span>
          <span className="text-[14px] font-bold lowercase tracking-tight text-primary">stripe</span>
        </span>
      </div>
      {!ready && (
        <div className="flex items-center justify-center py-10 text-muted-foreground">
          <Loader2 className="h-6 w-6 animate-spin" />
        </div>
      )}
      <div className={ready ? undefined : "pointer-events-none h-0 overflow-hidden opacity-0"}>
        <PaymentElement
          options={{ layout: { type: "accordion", defaultCollapsed: false } }}
          onReady={() => setReady(true)}
        />
        {error && <p className="mt-3 text-[13px] text-destructive">{error}</p>}
        <Button onClick={pay} disabled={paying} className="mt-5 h-11 w-full text-[14px] font-semibold">
          {paying ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> Processing…</> : `Pay ${money(total)}`}
        </Button>
        <p className="mt-3 flex items-center justify-center gap-1.5 text-center text-[11.5px] text-muted-foreground">
          <ShieldCheck className="h-3.5 w-3.5 text-emerald-600" />
          Cancel anytime · You'll set up your account next
        </p>
      </div>
    </section>
  );
}

export default function SignupCheckout() {
  const { token = "" } = useParams();
  const navigate = useNavigate();

  const [data, setData] = useState<CheckoutData | null>(null);
  const [plan, setPlan] = useState<CatalogPlan | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get<CheckoutData>(`/api/signup/checkout?token=${encodeURIComponent(token)}`);
        if (cancelled) return;
        if (res.status === "paid" || res.status === "completed") {
          navigate(res.for_existing_user ? `/account/new/finish/${token}` : `/signup/complete/${token}`, { replace: true });
          return;
        }
        setData(res);
        const plans = await api.get<PlansResponse>("/api/plans");
        if (!cancelled) setPlan(plans.plans.find((p) => p.code === res.plan_code) ?? null);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof ApiError ? err.message || "Couldn't load checkout." : "Network error.");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token, navigate]);

  const stripePromise = useMemo<Promise<Stripe | null> | null>(
    () => (data?.publishable_key ? loadStripe(data.publishable_key) : null),
    [data?.publishable_key],
  );

  const isAnnual = (data?.billing_cycle ?? "annual") !== "monthly";

  // After a confirmed payment, finalize (poll briefly — the webhook is the
  // backup) then move to account setup (Step 6).
  const finalizeAndContinue = async () => {
    for (let i = 0; i < 6; i++) {
      try {
        const r = await api.post<{ paid: boolean }>("/api/signup/finalize", { token });
        if (r.paid) break;
      } catch {
        /* transient — retry */
      }
      await new Promise((res) => setTimeout(res, 1000));
    }
    navigate(data?.for_existing_user ? `/account/new/finish/${token}` : `/signup/complete/${token}`, { replace: true });
  };

  if (error) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
        <div className="flex max-w-md flex-col items-center gap-3 rounded-2xl border border-border bg-card p-10 text-center shadow-sm">
          <AlertCircle className="h-10 w-10 text-destructive" />
          <p className="text-sm text-muted-foreground">{error}</p>
          <Link to="/signup" className="text-sm text-brand-purple hover:underline">Start again</Link>
        </div>
      </div>
    );
  }

  if (!data || !plan || !data.client_secret || !stripePromise) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30">
        <Loader2 className="h-8 w-8 animate-spin text-brand-purple" />
      </div>
    );
  }

  const perMonth = isAnnual ? plan.price_monthly * (1 - plan.annual_discount_pct / 100) : plan.price_monthly;
  const total = isAnnual ? perMonth * 12 : plan.price_monthly;

  return (
    <div className="signup-fullscreen bg-muted/30 px-4 py-10">
      <div className="mx-auto max-w-4xl">
        <div className="mb-7 flex flex-col items-center text-center">
          <TrilliLogo className="h-[30px] text-brand-purple" />
          <h1 className="mt-3 text-2xl font-bold text-foreground">Complete your subscription</h1>
          <p className="mt-1 text-sm text-muted-foreground">Secure checkout — you'll create your account in the next step.</p>
        </div>
        <div className="mb-9">
          <SignupSteps current={2} oauth={!!data.oauth_provider} />
        </div>
        <div className={cn("grid items-stretch gap-6", "lg:grid-cols-[minmax(320px,400px)_1fr]")}>
          <SelectedPlanPane plan={plan} isAnnual={isAnnual} />
          <Elements stripe={stripePromise} options={{ clientSecret: data.client_secret, appearance: STRIPE_APPEARANCE }}>
            <PaymentPane token={token} total={total} onPaid={finalizeAndContinue} />
          </Elements>
        </div>
      </div>
    </div>
  );
}
