import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { loadStripe } from "@stripe/stripe-js";
import { AddressElement, Elements, PaymentElement, useElements, useStripe } from "@stripe/react-stripe-js";
import type { Stripe } from "@stripe/stripe-js";
import {
  ArrowLeft,
  Check,
  ChevronDown,
  CreditCard,
  Feather,
  Infinity as InfinityIcon,
  Loader2,
  Lock,
  Plus,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";

import { api, type CatalogPlan, type PlansResponse } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { STRIPE_APPEARANCE, type Prefill } from "@/lib/stripeTheme";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";

function fmtPrice(n: number): string {
  return `$${n.toFixed(2)}`;
}

const PLAN_ICONS: Record<string, LucideIcon> = {
  feather: Feather,
  plus: Plus,
  infinity: InfinityIcon,
};

const inputCls = "h-7 border-foreground/25 bg-background text-xs";
const selectCls =
  "h-7 w-full appearance-none rounded-md border border-foreground/25 bg-background pl-2.5 pr-8 text-xs text-foreground outline-none focus-visible:ring-1 focus-visible:ring-primary/40";

const US_STATES = [
  "AL", "AK", "AZ", "AR", "CA", "CO", "CT", "DE", "DC", "FL", "GA", "HI", "ID",
  "IL", "IN", "IA", "KS", "KY", "LA", "ME", "MD", "MA", "MI", "MN", "MS", "MO",
  "MT", "NE", "NV", "NH", "NJ", "NM", "NY", "NC", "ND", "OH", "OK", "OR", "PA",
  "RI", "SC", "SD", "TN", "TX", "UT", "VT", "VA", "WA", "WV", "WI", "WY",
];
const CA_PROVINCES = [
  "AB", "BC", "MB", "NB", "NL", "NS", "NT", "NU", "ON", "PE", "QC", "SK", "YT",
];

function PoweredByStripe() {
  return (
    <span className="inline-flex items-baseline gap-1">
      <Lock className="h-3 w-3 translate-y-0.5 text-muted-foreground" />
      <span className="text-[10px] text-muted-foreground">Powered by</span>
      <span className="text-[14px] font-bold lowercase tracking-tight text-primary">stripe</span>
    </span>
  );
}

function Field({ label, htmlFor, children }: { label: string; htmlFor?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs font-medium text-foreground">
        {label}
      </Label>
      {children}
    </div>
  );
}

// Amounts derived from a plan + billing period (matches server-side lock math).
function amountsFor(plan: CatalogPlan, isAnnual: boolean) {
  const annualMonthly = plan.price_monthly * (1 - plan.annual_discount_pct / 100);
  const perMonth = isAnnual ? annualMonthly : plan.price_monthly;
  const subtotal = isAnnual ? annualMonthly * 12 : plan.price_monthly;
  const annualSavings = plan.price_monthly * 12 - annualMonthly * 12;
  return { perMonth, subtotal, estTax: 0, total: subtotal, annualSavings };
}

// Shared order-summary / "Pay" widget. The Pay button delegates to onPay.
function OrderSummary({
  plan,
  isAnnual,
  paying,
  onPay,
}: {
  plan: CatalogPlan;
  isAnnual: boolean;
  paying: boolean;
  onPay: () => void;
}) {
  const Icon = PLAN_ICONS[plan.icon] ?? Feather;
  const { perMonth, subtotal, estTax, total, annualSavings } = amountsFor(plan, isAnnual);
  return (
    <aside className="flex h-full flex-col space-y-4 rounded-xl border border-border bg-card p-5 shadow-sm">
      <div className="flex items-center gap-2.5">
        <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
          <Icon className="h-5 w-5" strokeWidth={2.25} />
        </span>
        <div>
          <p className="text-[15px] font-bold leading-tight text-foreground">{plan.name}</p>
          <p className="text-[11px] text-muted-foreground">{isAnnual ? "Billed annually" : "Billed monthly"}</p>
        </div>
        <span className="ml-auto text-[13px] font-semibold text-foreground">{fmtPrice(perMonth)}/mo</span>
      </div>

      <div className="!mt-[23px] rounded-lg bg-muted/40 p-3">
        <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">What's included</p>
        <ul className="mt-2 space-y-1.5">
          {plan.features.map((f) => (
            <li key={f} className="flex items-start gap-2 text-[12px] text-foreground">
              <Check className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-emerald-600" />
              <span className="leading-snug">{f}</span>
            </li>
          ))}
        </ul>
      </div>

      <div className="!mt-9 space-y-1.5 border-t border-border pt-3 text-[13px]">
        <div className="flex justify-between text-muted-foreground">
          <span>Subtotal{isAnnual ? " (12 mo)" : ""}</span>
          <span className="tabular-nums text-foreground">{fmtPrice(subtotal)}</span>
        </div>
        <div className="flex justify-between text-muted-foreground">
          <span>Estimated tax</span>
          <span className="tabular-nums">{fmtPrice(estTax)}</span>
        </div>
        <div className="mt-1 flex items-baseline justify-between border-t border-border pt-2.5">
          <span className="text-[13px] font-semibold text-foreground">Total due today</span>
          <span className="text-xl font-bold tabular-nums text-foreground">{fmtPrice(total)}</span>
        </div>
        {isAnnual && annualSavings > 0 && (
          <p className="pt-1 text-[11.5px] font-medium text-emerald-700">
            You're saving {fmtPrice(annualSavings)} a year vs. monthly billing.
          </p>
        )}
      </div>

      <Button onClick={onPay} disabled={paying} className="h-10 w-full text-[13px] font-semibold">
        {paying ? (
          <>
            <Loader2 className="mr-2 h-4 w-4 animate-spin" /> Processing…
          </>
        ) : (
          `Pay ${fmtPrice(total)}`
        )}
      </Button>
      <p className="flex items-center justify-center gap-1.5 text-center text-[11px] text-muted-foreground">
        <ShieldCheck className="h-3.5 w-3.5 text-emerald-600" />
        Cancel anytime · Final tax calculated at payment
      </p>
    </aside>
  );
}

type AddrValue = { name?: string; address?: Prefill["address"] };

// Stripe's themed Address Element, rendered in its OWN Elements instance with NO
// customer session. With the session, the Element adopts the saved card's
// billing address and collapses it into a read-only "Update" summary; without
// it, the Element always shows the full open form, prefilled via defaultValues.
function StripeBillingAddress({
  stripePromise,
  prefill,
  onChange,
  onReady,
}: {
  stripePromise: Promise<Stripe | null>;
  prefill?: Prefill | null;
  onChange: (v: AddrValue) => void;
  onReady?: () => void;
}) {
  return (
    <Elements stripe={stripePromise} options={{ mode: "setup", currency: "usd", appearance: STRIPE_APPEARANCE }}>
      <AddressElement
        options={{
          mode: "billing",
          defaultValues: prefill ? { name: prefill.name, address: prefill.address } : undefined,
        }}
        onChange={(e) => onChange(e.value)}
        onReady={() => onReady?.()}
      />
    </Elements>
  );
}

// ---- REAL: Stripe Payment Element + Stripe Address Element (both themed) --
function StripeForm({
  plan,
  billing,
  prefill,
  stripePromise,
  onPaid,
}: {
  plan: CatalogPlan;
  billing: "monthly" | "annual";
  prefill?: Prefill | null;
  stripePromise: Promise<Stripe | null>;
  onPaid: () => Promise<void>;
}) {
  const stripe = useStripe();
  const elements = useElements();
  const [paying, setPaying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [addr, setAddr] = useState<AddrValue>(() => ({ name: prefill?.name, address: prefill?.address }));

  // Hold the skeleton until BOTH Stripe Elements have rendered, then reveal the
  // whole form at once (no secondary "Stripe is loading" flash). A timeout
  // failsafe reveals it anyway if a ready event never fires.
  const [pmReady, setPmReady] = useState(false);
  const [addrReady, setAddrReady] = useState(false);
  const [timedOut, setTimedOut] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setTimedOut(true), 6000);
    return () => clearTimeout(t);
  }, []);
  const ready = (pmReady && addrReady) || timedOut;

  const pay = async () => {
    if (!stripe || !elements) return;
    setPaying(true);
    setError(null);

    // Capture the billing address (from the Address Element) so we can persist
    // it on our side after a successful payment.
    const billing = {
      name: addr.name ?? "",
      address: {
        line1: addr.address?.line1 ?? "",
        line2: addr.address?.line2 ?? "",
        city: addr.address?.city ?? "",
        state: addr.address?.state ?? "",
        postal_code: addr.address?.postal_code ?? "",
        country: addr.address?.country ?? "",
      },
    };

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
      if (billing) {
        try {
          await api.post("/api/account/billing-address", {
            name: billing.name ?? "",
            line1: billing.address?.line1 ?? "",
            line2: billing.address?.line2 ?? "",
            city: billing.address?.city ?? "",
            state: billing.address?.state ?? "",
            postal_code: billing.address?.postal_code ?? "",
            country: billing.address?.country ?? "",
          });
        } catch {
          /* non-fatal — Stripe still has it */
        }
      }
      try {
        await onPaid();
      } catch {
        setError("Payment went through but we couldn't activate the plan. Contact support.");
        setPaying(false);
      }
      return;
    }
    setPaying(false);
  };

  return (
    <>
      {/* Skeleton stays until Stripe is ready; the real form mounts off-screen
          so its Elements load behind it, then we swap in the finished form. */}
      {!ready && <CheckoutBodySkeleton />}
      <div className={ready ? undefined : "pointer-events-none fixed left-[-9999px] top-0 w-[1000px] opacity-0"}>
        <div className="mt-8 grid gap-6 lg:grid-cols-[1fr_minmax(320px,380px)] lg:items-stretch">
          <div className="flex flex-col gap-6 lg:h-full">
            <section className="space-y-4 rounded-xl border border-border bg-card p-5 lg:flex-1">
              <div className="flex items-center justify-between">
                <h2 className="text-[15px] font-semibold text-foreground">Payment details</h2>
                <PoweredByStripe />
              </div>
              <PaymentElement
                options={{ layout: { type: "accordion", defaultCollapsed: false } }}
                onReady={() => setPmReady(true)}
              />
              <div className="space-y-4 border-t border-border pt-4">
                <p className="text-xs font-medium text-foreground">Billing address</p>
                <StripeBillingAddress
                  stripePromise={stripePromise}
                  prefill={prefill}
                  onChange={setAddr}
                  onReady={() => setAddrReady(true)}
                />
              </div>
            </section>
            {error && <p className="text-[13px] text-destructive">{error}</p>}
          </div>
          <OrderSummary plan={plan} isAnnual={billing === "annual"} paying={paying} onPay={pay} />
        </div>
      </div>
    </>
  );
}

// ---- FALLBACK: mock form (billing not configured) ------------------------
function MockForm({
  plan,
  billing,
  onPaid,
}: {
  plan: CatalogPlan;
  billing: "monthly" | "annual";
  onPaid: () => Promise<void>;
}) {
  const [paying, setPaying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [country, setCountry] = useState("US");
  const stateLabel = country === "CA" ? "Province" : country === "US" ? "State" : "Region";
  const zipLabel = country === "US" ? "ZIP" : "Postal code";
  const stateOptions = country === "US" ? US_STATES : country === "CA" ? CA_PROVINCES : null;

  const pay = async () => {
    setPaying(true);
    setError(null);
    try {
      await new Promise((r) => setTimeout(r, 1100));
      await onPaid();
    } catch {
      setError("Payment couldn't be completed. Please try again.");
      setPaying(false);
    }
  };

  return (
    <div className="mt-8 grid gap-6 lg:grid-cols-[1fr_minmax(320px,380px)] lg:items-stretch">
      <div className="flex flex-col gap-7 lg:h-full">
        <section className="space-y-4 rounded-xl border border-border bg-card p-5 lg:flex-1">
          <div className="flex items-center justify-between">
            <h2 className="text-[15px] font-semibold text-foreground">Payment details</h2>
            <PoweredByStripe />
          </div>
          <Field label="Card information">
            <div className="overflow-hidden rounded-md border border-foreground/25 bg-background">
              <div className="flex items-center gap-2 border-b border-foreground/15 px-2.5">
                <CreditCard className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                <input disabled={paying} placeholder="1234 1234 1234 1234" className="h-7 w-full bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground/60" />
              </div>
              <div className="flex">
                <input disabled={paying} placeholder="MM / YY" className="h-7 w-1/2 border-r border-foreground/15 bg-transparent px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/60" />
                <input disabled={paying} placeholder="CVC" className="h-7 w-1/2 bg-transparent px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/60" />
              </div>
            </div>
          </Field>
          <Field label="Name on card" htmlFor="co-name">
            <Input id="co-name" disabled={paying} placeholder="Jane Appleseed" className={inputCls} />
          </Field>
          <div className="space-y-4 border-t border-border pt-4">
            <p className="text-xs font-medium text-foreground">Billing address</p>
            <Field label="Country" htmlFor="co-country">
            <div className="relative">
              <select id="co-country" disabled={paying} value={country} onChange={(e) => setCountry(e.target.value)} className={selectCls}>
                <option value="US">United States</option>
                <option value="CA">Canada</option>
                <option value="GB">United Kingdom</option>
                <option value="AU">Australia</option>
                <option value="other">Other</option>
              </select>
              <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            </div>
          </Field>
          <Field label="Address" htmlFor="co-addr">
            <Input id="co-addr" disabled={paying} placeholder="Address line 1" className={inputCls} />
            <Input disabled={paying} placeholder="Address line 2 (optional)" className={inputCls} />
          </Field>
          <div className="grid grid-cols-3 gap-3">
            <Field label="City" htmlFor="co-city">
              <Input id="co-city" disabled={paying} className={inputCls} />
            </Field>
            <Field label={stateLabel} htmlFor="co-state">
              {stateOptions ? (
                <div className="relative" key={country}>
                  <select id="co-state" disabled={paying} defaultValue="" className={selectCls}>
                    <option value="" disabled>Select</option>
                    {stateOptions.map((s) => (
                      <option key={s} value={s}>{s}</option>
                    ))}
                  </select>
                  <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                </div>
              ) : (
                <Input id="co-state" disabled={paying} className={inputCls} />
              )}
            </Field>
            <Field label={zipLabel} htmlFor="co-zip">
              <Input id="co-zip" disabled={paying} className={inputCls} />
            </Field>
          </div>
          </div>
        </section>
        {error && <p className="text-[13px] text-destructive">{error}</p>}
      </div>
      <OrderSummary plan={plan} isAnnual={billing === "annual"} paying={paying} onPay={pay} />
    </div>
  );
}

// CheckoutBodySkeleton mirrors the two-column body (payment card + order
// summary) and is shown both during the initial fetch and while the Stripe
// Elements hydrate — so the page reveals fully formed in a single step.
function CheckoutBodySkeleton() {
  return (
        <div className="mt-8 grid gap-6 lg:grid-cols-[1fr_minmax(320px,380px)] lg:items-stretch">
          {/* Left: payment details card — mirrors saved card + card row + the
              full billing-address form so its height matches the real card. */}
          <div className="flex flex-col gap-6 lg:h-full">
            <div className="space-y-4 rounded-xl border border-border bg-card p-5 lg:flex-1">
              <div className="flex items-center justify-between">
                <Skeleton className="h-5 w-32" />
                <Skeleton className="h-4 w-24" />
              </div>
              {/* Saved-card box + "Card" row */}
              <Skeleton className="h-28 w-full rounded-lg" />
              <Skeleton className="h-11 w-full rounded-lg" />
              {/* Billing address: label + 5 stacked fields + State/ZIP row */}
              <div className="space-y-4 border-t border-border pt-4">
                <Skeleton className="h-4 w-28" />
                {Array.from({ length: 5 }).map((_, i) => (
                  <div key={i} className="space-y-1.5">
                    <Skeleton className="h-3 w-24" />
                    <Skeleton className="h-9 w-full" />
                  </div>
                ))}
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Skeleton className="h-3 w-16" />
                    <Skeleton className="h-9 w-full" />
                  </div>
                  <div className="space-y-1.5">
                    <Skeleton className="h-3 w-16" />
                    <Skeleton className="h-9 w-full" />
                  </div>
                </div>
              </div>
            </div>
          </div>
          {/* Right: order summary — mirrors plan header, the full What's-included
              list, totals (with the same nudge gap) and Pay button + footer. */}
          <div className="flex flex-col gap-4 rounded-xl border border-border bg-card p-5 shadow-sm">
            <div className="flex items-center gap-2.5">
              <Skeleton className="h-10 w-10 rounded-xl" />
              <div className="flex-1 space-y-1.5">
                <Skeleton className="h-4 w-24" />
                <Skeleton className="h-3 w-20" />
              </div>
              <Skeleton className="h-4 w-16" />
            </div>
            <div className="space-y-2 rounded-lg bg-muted/40 p-3">
              <Skeleton className="h-3 w-28" />
              <div className="space-y-2 pt-1">
                {Array.from({ length: 13 }).map((_, i) => (
                  <Skeleton key={i} className="h-3 w-full" />
                ))}
              </div>
            </div>
            <div className="!mt-9 space-y-2 border-t border-border pt-3">
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="mt-1 h-7 w-full" />
              <Skeleton className="h-3 w-3/4" />
            </div>
            <Skeleton className="h-10 w-full" />
            <Skeleton className="mx-auto h-3 w-2/3" />
          </div>
        </div>
  );
}

// CheckoutSkeleton is the full-page loader (header + body) shown during the
// initial plan + Stripe-intent fetch.
function CheckoutSkeleton() {
  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-5xl">
        <Skeleton className="h-4 w-28" />
        <Skeleton className="mt-4 h-8 w-40" />
        <Skeleton className="mt-2 h-4 w-80" />
        <div className="mt-5 border-t border-border" />
        <CheckoutBodySkeleton />
      </div>
    </div>
  );
}

export default function Checkout() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { refresh } = useAuth();
  const code = params.get("plan") ?? "";
  const billing = params.get("billing") === "monthly" ? "monthly" : "annual";

  const [plan, setPlan] = useState<CatalogPlan | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pubKey, setPubKey] = useState<string | null>(null);
  const [clientSecret, setClientSecret] = useState<string | null>(null);
  const [customerSession, setCustomerSession] = useState<string | null>(null);
  const [prefill, setPrefill] = useState<Prefill | null>(null);

  // Load the plan, then (if billing is live) a PaymentIntent for the Elements.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const r = await api.get<PlansResponse>("/api/plans");
        const found = r.plans.find((p) => p.code === code) ?? null;
        if (cancelled) return;
        setPlan(found);
        if (found) {
          try {
            const cfg = await api.get<{ enabled: boolean; publishable_key: string }>("/api/billing/config");
            if (cfg.enabled && cfg.publishable_key) {
              const intent = await api.post<{
                client_secret: string;
                customer_session_client_secret?: string;
                prefill?: Prefill;
              }>("/api/billing/intent", {
                code: found.code,
                billing_period: billing,
              });
              if (!cancelled) {
                setPubKey(cfg.publishable_key);
                setClientSecret(intent.client_secret);
                setCustomerSession(intent.customer_session_client_secret ?? null);
                setPrefill(intent.prefill ?? null);
              }
            }
          } catch {
            /* billing not ready → fall back to the mock form */
          }
        }
      } catch {
        if (!cancelled) setError("Couldn't load the plan.");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [code, billing]);

  const stripePromise = useMemo(() => (pubKey ? loadStripe(pubKey) : null), [pubKey]);

  // Activate the plan after a successful (or mock) payment, then send the buyer
  // to the dedicated thank-you / welcome page.
  const onPaid = async () => {
    await api.post("/api/account/plan", { code, billing_period: billing });
    await refresh({ silent: true });
    navigate(`/welcome?plan=${code}&billing=${billing}`, { replace: true });
  };

  if (loading) {
    return <CheckoutSkeleton />;
  }
  if (!plan) {
    return (
      <div className="mx-auto max-w-md px-6 py-20 text-center">
        <p className="text-[13px] text-muted-foreground">{error ?? "Plan not found."}</p>
        <Link to="/settings/plan" className="mt-3 inline-block text-[13px] font-medium text-primary hover:underline">
          ← Back to plans
        </Link>
      </div>
    );
  }

  const useReal = Boolean(stripePromise && clientSecret);

  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-5xl">
        <Link
          to="/settings/plan"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to plans
        </Link>
        <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Checkout</h1>
          <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-amber-700">
            Test mode
          </span>
        </div>
        <p className="mt-1 text-[13px] text-muted-foreground">
          You're upgrading to the <span className="font-medium text-foreground">{plan.name}</span> plan — review and
          complete your purchase below.
        </p>

        <div className="mt-5 border-t border-border" />

        {useReal ? (
          <Elements
            stripe={stripePromise}
            options={{
              clientSecret: clientSecret!,
              customerSessionClientSecret: customerSession ?? undefined,
              appearance: STRIPE_APPEARANCE,
            }}
          >
            <StripeForm plan={plan} billing={billing} prefill={prefill} stripePromise={stripePromise!} onPaid={onPaid} />
          </Elements>
        ) : (
          <MockForm plan={plan} billing={billing} onPaid={onPaid} />
        )}
      </div>
    </div>
  );
}
