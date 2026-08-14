import { Link } from "react-router-dom";
import {
  Building2,
  Users,
  Package,
  CreditCard,
  Server,
  BarChart3,
  Shield,
  ArrowRight,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { cn } from "@/lib/utils";
import type { Role } from "@/lib/api";

// Dashboard cards — each surfaces just enough to orient the operator quickly.
// The `hint` is a compact preview of what that section tracks.
interface CardDef {
  key: string;
  to: string;
  title: string;
  minRole: Role;
  hint: string;
  icon: typeof Building2;
  metrics: string[];
}

const CARDS: CardDef[] = [
  {
    key: "accounts",
    to: "/accounts",
    title: "Accounts",
    minRole: "cmx",
    hint: "Tenant workspaces, plans, storage",
    icon: Building2,
    metrics: ["Active", "Suspended", "Near quota"],
  },
  {
    key: "customers",
    to: "/customers",
    title: "Customers",
    minRole: "cmx",
    hint: "People and organizations",
    icon: Users,
    metrics: ["Active", "Trial", "Churned"],
  },
  {
    key: "catalog",
    to: "/catalog",
    title: "Catalog",
    minRole: "cmx",
    hint: "Plans, pricing, inventory",
    icon: Package,
    metrics: ["Available", "Draft", "Subscribers"],
  },
  {
    key: "revenue",
    to: "/revenue",
    title: "Revenue",
    minRole: "global",
    hint: "MRR, subscriptions, past-due",
    icon: CreditCard,
    metrics: ["MRR", "Active subs", "Past-due"],
  },
  {
    key: "infrastructure",
    to: "/infrastructure",
    title: "Infrastructure",
    minRole: "global",
    hint: "Jobs, health, storage tiering",
    icon: Server,
    metrics: ["Jobs", "Health", "Storage"],
  },
  {
    key: "reports",
    to: "/reports",
    title: "Reports",
    minRole: "global",
    hint: "ARR, funnel, unit economics",
    icon: BarChart3,
    metrics: ["ARR", "Funnel", "Margin"],
  },
  {
    key: "administration",
    to: "/administration",
    title: "Administration",
    minRole: "global",
    hint: "Operators, audit log, vault",
    icon: Shield,
    metrics: ["Operators", "Audit", "Vault"],
  },
];

export default function Main() {
  const { operator } = useAuth();
  const role = operator?.role ?? "cmx";

  const visible = CARDS.filter((c) => c.minRole === "cmx" || role === "global");

  return (
    <div className="mx-auto max-w-[95.2rem] space-y-6">
      <div className="animate-fade-up">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          Welcome back{operator?.name ? `, ${operator.name.split(" ")[0]}` : ""}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Operator console · {visible.length} section{visible.length !== 1 ? "s" : ""} available
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {visible.map((c, i) => (
          <DashCard key={c.key} card={c} index={i} />
        ))}
      </div>
    </div>
  );
}

function DashCard({ card, index }: { card: CardDef; index: number }) {
  const Icon = card.icon;
  return (
    <Link
      to={card.to}
      className={cn(
        "group animate-fade-up flex flex-col rounded-xl border border-border bg-card p-5",
        "shadow-sm ring-1 ring-black/[0.02]",
        "transition-all duration-200 hover:border-border/80 hover:shadow-md",
      )}
      style={{ animationDelay: `${index * 55}ms` }}
    >
      <div className="mb-4 flex items-center justify-between">
        <Icon className="h-[18px] w-[18px] text-muted-foreground/60 transition-colors group-hover:text-primary/70" />
        <ArrowRight className="h-3.5 w-3.5 translate-x-0 text-muted-foreground/30 opacity-0 transition-all duration-200 group-hover:translate-x-0.5 group-hover:opacity-100" />
      </div>

      <div className="flex-1">
        <h2 className="text-[13px] font-semibold text-foreground">{card.title}</h2>
        <p className="mt-0.5 text-xs text-muted-foreground/70">{card.hint}</p>
      </div>

      <div className="mt-4 flex items-center gap-1.5">
        {card.metrics.map((m) => (
          <span
            key={m}
            className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground/70"
          >
            {m}
          </span>
        ))}
      </div>
    </Link>
  );
}
