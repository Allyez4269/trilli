import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowRight,
  CheckCircle2,
  Feather,
  FileSignature,
  FolderUp,
  Infinity as InfinityIcon,
  Mail,
  Plus,
  Receipt,
  ShieldCheck,
  Sparkles,
  Users,
  type LucideIcon,
} from "lucide-react";

import { api, type BillingTransaction, type CatalogPlan, type PlansResponse } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

const PLAN_ICONS: Record<string, LucideIcon> = {
  feather: Feather,
  plus: Plus,
  infinity: InfinityIcon,
};

function fmtPrice(n: number): string {
  return `$${n.toFixed(2)}`;
}

// "Make the most of your plan" — curated next steps, each pointing somewhere
// actionable. Applies across the current plan line-up (all include apps, 2FA,
// shared workspaces, and access controls).
const NEXT_STEPS: { icon: LucideIcon; title: string; body: string; cta: string; to: string }[] = [
  {
    icon: FolderUp,
    title: "Move your files in",
    body: "Drag and drop your documents into Trilli and organize them with folders and workspaces.",
    cta: "Go to Files",
    to: "/files",
  },
  {
    icon: Users,
    title: "Bring your team",
    body: "Invite teammates into shared workspaces — no seat minimums on your plan.",
    cta: "Invite members",
    to: "/members",
  },
  {
    icon: FileSignature,
    title: "Use your included apps",
    body: "Sign documents with Trilli Sign and edit PDFs with Trilli PDF, built right in.",
    cta: "Open files",
    to: "/files",
  },
  {
    icon: ShieldCheck,
    title: "Lock it down",
    body: "Turn on 2FA and passkeys and set workspace + folder access control for your team.",
    cta: "Security settings",
    to: "/settings/security",
  },
];

function WelcomeSkeleton() {
  return (
    <div className="px-6 py-12">
      <div className="mx-auto max-w-3xl">
        <div className="flex flex-col items-center text-center">
          <Skeleton className="h-16 w-16 rounded-full" />
          <Skeleton className="mt-5 h-8 w-72" />
          <Skeleton className="mt-3 h-4 w-96" />
        </div>
        <Skeleton className="mt-10 h-40 w-full rounded-xl" />
        <div className="mt-6 grid gap-4 sm:grid-cols-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-32 w-full rounded-xl" />
          ))}
        </div>
      </div>
    </div>
  );
}

export default function Welcome() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const { identity } = useAuth();
  const code = params.get("plan") ?? "";
  const isAnnual = params.get("billing") !== "monthly";

  const [plan, setPlan] = useState<CatalogPlan | null>(null);
  const [loading, setLoading] = useState(true);
  const [tx, setTx] = useState<BillingTransaction | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .get<PlansResponse>("/api/plans")
      .then((r) => {
        if (!cancelled) setPlan(r.plans.find((p) => p.code === code) ?? null);
      })
      .catch(() => {
        /* fall through to a generic thank-you below */
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [code]);

  // Poll for our order record. It's written by the webhook, which can lag the
  // page by a second or two; retry a few times, accepting only a fresh order
  // for this plan (so an older purchase never shows here).
  useEffect(() => {
    if (!code) return;
    let cancelled = false;
    let tries = 0;
    const poll = async () => {
      try {
        const r = await api.get<Partial<BillingTransaction>>("/api/billing/transactions/latest");
        const fresh =
          r.order_number &&
          r.plan_code === code &&
          r.created_at &&
          Date.now() - new Date(r.created_at).getTime() < 10 * 60 * 1000;
        if (fresh) {
          if (!cancelled) setTx(r as BillingTransaction);
          return;
        }
      } catch {
        /* ignore; retry below */
      }
      tries += 1;
      if (!cancelled && tries < 5) setTimeout(poll, 1500);
    };
    poll();
    return () => {
      cancelled = true;
    };
  }, [code]);

  const firstName = useMemo(() => {
    const u = identity?.user;
    if (u?.first_name) return u.first_name;
    if (u?.full_name) return u.full_name.split(" ")[0];
    if (u?.email) return u.email.split("@")[0];
    return "there";
  }, [identity]);

  const email = identity?.user?.email ?? "your email";

  if (loading) return <WelcomeSkeleton />;

  const Icon = (plan && PLAN_ICONS[plan.icon]) || Sparkles;
  const planName = plan?.name ?? "your new plan";
  const annualMonthly = plan ? plan.price_monthly * (1 - plan.annual_discount_pct / 100) : 0;
  const perMonth = isAnnual ? annualMonthly : plan?.price_monthly ?? 0;

  return (
    <div className="px-6 py-12">
      <div className="mx-auto max-w-3xl">
        {/* Hero */}
        <div className="flex flex-col items-center text-center">
          <span className="flex h-16 w-16 items-center justify-center rounded-full bg-emerald-500/10">
            <CheckCircle2 className="h-9 w-9 text-emerald-600" />
          </span>
          <h1 className="mt-5 text-3xl font-bold tracking-tight text-foreground">
            Thank you, {firstName}!
          </h1>
          <p className="mt-2 max-w-xl text-[14px] leading-relaxed text-muted-foreground">
            Your upgrade to <span className="font-semibold text-foreground">{planName}</span> is active.
            Your card is saved for next time, and everything below is ready to use right now.
          </p>
          <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
            <span className="inline-flex items-center gap-2 rounded-full bg-muted px-3 py-1.5 text-[12.5px] text-muted-foreground">
              <Mail className="h-3.5 w-3.5 text-primary" />
              A receipt is on its way to <span className="font-medium text-foreground">{email}</span>
            </span>
            {tx?.order_number && (
              <span className="inline-flex items-center gap-2 rounded-full bg-muted px-3 py-1.5 text-[12.5px] text-muted-foreground">
                <Receipt className="h-3.5 w-3.5 text-primary" />
                Order <span className="font-medium text-foreground">{tx.order_number}</span>
              </span>
            )}
            {tx?.receipt_url && (
              <a
                href={tx.receipt_url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 rounded-full border border-border px-3 py-1.5 text-[12.5px] font-medium text-primary transition-colors hover:bg-muted/60"
              >
                View receipt
                <ArrowRight className="h-3.5 w-3.5" />
              </a>
            )}
          </div>
        </div>

        {/* Plan summary */}
        {plan && (
          <div className="mt-10 rounded-xl border border-border bg-card p-5 shadow-sm">
            <div className="flex items-center gap-3">
              <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-primary/10 text-primary">
                <Icon className="h-6 w-6" strokeWidth={2.25} />
              </span>
              <div className="flex-1">
                <p className="text-[16px] font-bold text-foreground">{plan.name}</p>
                <p className="text-[12px] text-muted-foreground">{plan.tagline}</p>
              </div>
              <div className="text-right">
                <p className="text-[15px] font-semibold text-foreground">{fmtPrice(perMonth)}/mo</p>
                <p className="text-[11px] text-muted-foreground">{isAnnual ? "billed annually" : "billed monthly"}</p>
              </div>
            </div>
            <div className="mt-4 border-t border-border pt-4">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                Everything in your plan
              </p>
              <ul className="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-2">
                {plan.features.map((f) => (
                  <li key={f} className="flex items-start gap-2 text-[12.5px] text-foreground">
                    <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-emerald-600" />
                    <span className="leading-snug">{f}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        )}

        {/* Make the most of it */}
        <div className="mt-10">
          <h2 className="flex items-center gap-2 text-[15px] font-semibold text-foreground">
            <Sparkles className="h-4 w-4 text-primary" />
            Make the most of {planName}
          </h2>
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            {NEXT_STEPS.map((s) => {
              const StepIcon = s.icon;
              return (
                <Link
                  key={s.title}
                  to={s.to}
                  className="group flex flex-col rounded-xl border border-border bg-card p-4 transition-colors hover:border-primary/40 hover:bg-muted/40"
                >
                  <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
                    <StepIcon className="h-5 w-5" strokeWidth={2.25} />
                  </span>
                  <p className="mt-3 text-[13.5px] font-semibold text-foreground">{s.title}</p>
                  <p className="mt-1 flex-1 text-[12px] leading-relaxed text-muted-foreground">{s.body}</p>
                  <span className="mt-3 inline-flex items-center gap-1 text-[12px] font-medium text-primary">
                    {s.cta}
                    <ArrowRight className="h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" />
                  </span>
                </Link>
              );
            })}
          </div>
        </div>

        {/* Footer CTA */}
        <div className="mt-10 flex justify-center">
          <Button onClick={() => navigate("/files")} className="h-9 px-5 text-[13px] font-medium">
            Go to Files
          </Button>
        </div>
      </div>
    </div>
  );
}
