import { useState } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { Building2, Check, ChevronRight, Loader2, Search, Users } from "lucide-react";
import AuthLayout from "@/components/AuthLayout";
import { useAuth } from "@/contexts/AuthContext";
import { firstNameOf } from "@/lib/user-name";
import { cn } from "@/lib/utils";

// SelectAccount is the post-authentication account picker shown when a user can
// access more than one tenant (their own + ones they're a member of). The
// security profile is verified at login (on the user); this step only chooses
// which account to enter. Reuses the login split layout.
export default function SelectAccount() {
  const { identity, loading, switchTenant } = useAuth();
  const navigate = useNavigate();
  const [busyId, setBusyId] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");

  if (loading) {
    return (
      <AuthLayout>
        <div className="flex justify-center py-10">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      </AuthLayout>
    );
  }
  if (!identity) return <Navigate to="/login" replace />;

  const tenants = identity.tenants ?? [];
  // Nothing to choose between — go straight in.
  if (tenants.length <= 1) return <Navigate to="/home" replace />;

  const owned = tenants.filter((t) => t.is_owner);
  const shared = tenants.filter((t) => !t.is_owner);
  const activeId = identity.tenant?.id;

  const choose = async (tenantId: number) => {
    if (busyId) return;
    setBusyId(tenantId);
    setError(null);
    try {
      if (tenantId !== activeId) await switchTenant(tenantId);
      navigate("/home", { replace: true });
    } catch {
      setError("Couldn't open that account. Please try again.");
      setBusyId(null);
    }
  };

  const Row = ({ t }: { t: (typeof tenants)[number] }) => (
    <button
      type="button"
      onClick={() => void choose(t.id)}
      disabled={busyId !== null}
      className={cn(
        "group flex w-full items-center gap-3 rounded-xl border border-border bg-card px-4 py-3.5 text-left transition-colors",
        busyId === null && "hover:border-primary/40 hover:bg-primary/[0.03]",
        busyId !== null && "opacity-60",
      )}
    >
      <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
        {t.is_owner ? <Building2 className="h-[18px] w-[18px]" /> : <Users className="h-[18px] w-[18px]" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[15px] font-semibold text-foreground">{t.name}</span>
        <span className="block text-[12px] text-muted-foreground">
          {t.is_owner ? "Your account" : "Member"}
          {t.plan_name ? ` · ${t.plan_name}` : ""}
        </span>
      </span>
      {busyId === t.id ? (
        <Loader2 className="h-4 w-4 flex-shrink-0 animate-spin text-primary" />
      ) : t.id === activeId ? (
        <Check className="h-4 w-4 flex-shrink-0 text-primary" />
      ) : (
        <ChevronRight className="h-4 w-4 flex-shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
      )}
    </button>
  );

  const Group = ({ label, items }: { label: string; items: typeof tenants }) =>
    items.length === 0 ? null : (
      <div>
        <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
        <div className="space-y-2">
          {items.map((t) => (
            <Row key={t.id} t={t} />
          ))}
        </div>
      </div>
    );

  // Past a handful of accounts the flat list gets unwieldy, so switch to a
  // searchable, scrollable box. At or below the threshold we keep the calmer
  // per-account listing exactly as-is.
  const MANY_THRESHOLD = 5;
  const isMany = tenants.length > MANY_THRESHOLD;
  const q = query.trim().toLowerCase();
  const matches = (t: (typeof tenants)[number]) =>
    !q || t.name.toLowerCase().includes(q) || (t.plan_name ?? "").toLowerCase().includes(q);
  const ownedShown = owned.filter(matches);
  const sharedShown = shared.filter(matches);
  const noMatches = isMany && q !== "" && ownedShown.length === 0 && sharedShown.length === 0;

  return (
    <AuthLayout>
      <h1 className="text-2xl font-semibold text-foreground">
        Welcome back, {firstNameOf(identity.user) || "there"}
      </h1>
      <p className="mt-1.5 text-[14px] text-muted-foreground">
        You have access to more than one account. Choose which one to open.
      </p>

      {error && (
        <p className="mt-4 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[13px] text-destructive">
          {error}
        </p>
      )}

      {isMany ? (
        // Many accounts: a beautiful search box up top + a scrollable list.
        <div className="mt-5">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              autoFocus
              placeholder="Search your accounts…"
              aria-label="Search accounts"
              className="h-11 w-full rounded-xl border border-border bg-background pl-9 pr-3 text-[14px] outline-none transition-colors focus:border-primary/50"
            />
          </div>
          <div className="mt-3 max-h-[44vh] space-y-5 overflow-y-auto rounded-xl border border-border bg-muted/20 p-3">
            {noMatches ? (
              <p className="py-8 text-center text-[13px] text-muted-foreground">
                No accounts match “{query.trim()}”.
              </p>
            ) : (
              <>
                <Group label={`Your account${owned.length > 1 ? "s" : ""}`} items={ownedShown} />
                <Group label="Shared with you" items={sharedShown} />
              </>
            )}
          </div>
          <p className="mt-2 text-right text-[11px] text-muted-foreground">
            {tenants.length} accounts
          </p>
        </div>
      ) : (
        // A handful of accounts: the calm per-account listing.
        <div className="mt-6 space-y-6">
          <Group label={`Your account${owned.length > 1 ? "s" : ""}`} items={owned} />
          <Group label="Shared with you" items={shared} />
        </div>
      )}
    </AuthLayout>
  );
}
