import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertTriangle,
  Bell,
  CalendarClock,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ChevronsUpDown,
  Copy,
  CreditCard,
  Feather,
  Fingerprint,
  Globe,
  Infinity as InfinityIcon,
  Info,
  KeyRound,
  Laptop,
  Lightbulb,
  Lock,
  Mail,
  Loader2,
  type LucideIcon,
  MonitorSmartphone,
  ExternalLink,
  PiggyBank,
  Plus,
  RefreshCw,
  Search,
  Settings as SettingsIcon,
  Shield,
  ShieldCheck,
  Smartphone,
  Trash2,
  User as UserIcon,
  UserMinus,
  UserPlus,
  Wallet,
  X,
} from "lucide-react";
import { createPortal } from "react-dom";
import { loadStripe } from "@stripe/stripe-js";
import { Elements, PaymentElement, useElements, useStripe } from "@stripe/react-stripe-js";
import { Link, useNavigate, useParams } from "react-router-dom";

import { useAuth } from "@/contexts/AuthContext";
import {
  api,
  ApiError,
  type BillingOverview,
  type BillingTransaction,
  type ChangePlanResult,
  type ChangePreviewResult,
  type CatalogPlan,
  type Identity,
  type PlansResponse,
  type SavedCard,
} from "@/lib/api";
import { STRIPE_APPEARANCE } from "@/lib/stripeTheme";
import { AckModal } from "@/components/AckModal";
import { AvatarUploadModal } from "@/components/AvatarUploadModal";
import { ChangeEmailModal } from "@/components/ChangeEmailModal";
import { UserAvatar } from "@/components/UserAvatar";
import { TwoFactorSetupModal } from "@/components/TwoFactorSetupModal";
import TwoFactorDisableModal from "@/components/TwoFactorDisableModal";
import PasswordResetModal from "@/components/PasswordResetModal";
import DeleteAccountModal from "@/components/DeleteAccountModal";
import AppliedModal from "@/components/AppliedModal";
import PlanChangeConfirmModal from "@/components/PlanChangeConfirmModal";
import { passkeysSupported, registerPasskey, passkeyErrorMessage } from "@/lib/webauthn";
import googleAuthIcon from "@/assets/google-authenticator.svg";
import { PageHeader } from "@/components/PageHeader";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/Checkbox";

// ===========================================================================
// Settings (design concept — controls are not wired yet)
//
// A unified, tabbed settings hub. Read-only info is pulled live from the
// session; every editable control is inert (no persistence) until the
// matching endpoints land. Workspace + Plan tabs are owner/admin only.
// ===========================================================================

type TabKey = "account" | "security" | "notifications" | "plan" | "billing";

interface TabDef {
  key: TabKey;
  label: string;
  icon: LucideIcon;
  adminOnly?: boolean;
}

const TABS: TabDef[] = [
  { key: "account", label: "Account", icon: UserIcon },
  { key: "security", label: "Security", icon: Shield },
  { key: "notifications", label: "Notifications", icon: Bell },
  { key: "plan", label: "Plans", icon: CreditCard, adminOnly: true },
  { key: "billing", label: "Billing", icon: Wallet, adminOnly: true },
];

function isAdminOrOwner(role: string | undefined | null): boolean {
  const r = (role ?? "").toLowerCase().trim();
  return r === "owner" || r === "admin";
}

// Tabs whose controls actually persist now. The "design preview" banner is
// suppressed on these so we don't tell the user nothing saves when it does.
const WIRED_TABS = new Set<TabKey>(["account", "security", "notifications", "plan", "billing"]);

// Shared field styling: compact height, a faint-but-visible 1px gray border,
// and a wider max so fields line up with the rest of the app. Overrides the
// Input defaults via twMerge (h-8/text-sm/border-input).
const inputCls = "h-7 max-w-2xl border-foreground/25 bg-background text-xs";

// Read-only variant of inputCls — same form-field shape, but muted + a softer
// fill so non-editable values (Workspace ID, Created) read as informational.
const readonlyCls =
  "block h-7 w-full max-w-2xl rounded-md border border-foreground/25 bg-muted/40 px-2.5 text-xs leading-7 text-muted-foreground outline-none";

// Compact checkbox for the sessions table — subtle focus, muted when disabled
// (the current session can't be selected).

export default function Settings() {
  const { identity } = useAuth();
  const isAdmin = isAdminOrOwner(identity?.membership?.role);
  const tabs = TABS.filter((t) => !t.adminOnly || isAdmin);

  // The active tab is a clean path segment (/settings/account). The active tab
  // is derived straight from the URL — no local state — so a refresh or a
  // shared link lands on the same subsection and the sidebar "Upgrade plan"
  // jump just works. Falls back to the role's default when the segment is
  // missing or not allowed for this user.
  const navigate = useNavigate();
  const { tab: tabSegment } = useParams();
  const allowed = new Set(tabs.map((t) => t.key));
  const validTab =
    tabSegment && allowed.has(tabSegment as TabKey) ? (tabSegment as TabKey) : null;
  const tab: TabKey = validTab ?? "account";

  const setTab = (next: TabKey) => {
    navigate(`/settings/${next}`, { replace: true });
  };

  // Canonicalize the URL so the address bar always reflects the visible
  // subsection: bare /settings, an unknown tab, or one this role can't see all
  // resolve to /settings/account.
  useEffect(() => {
    if (!validTab) navigate("/settings/account", { replace: true });
  }, [validTab, navigate]);

  // Settings reads from the already-loaded session rather than its own fetch,
  // so to match the skeletal-load feel of every other section we show the
  // layout in skeletons for a brief mount phase, then reveal the real content.
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    const t = setTimeout(() => setLoading(false), 450);
    return () => clearTimeout(t);
  }, []);

  if (loading) return <SettingsSkeleton tabCount={tabs.length} />;

  return (
    <div className="flex-1 overflow-y-auto [scrollbar-color:rgba(0,0,0,0.12)_transparent] [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-foreground/10 [&::-webkit-scrollbar-track]:bg-transparent [&::-webkit-scrollbar]:w-2.5">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        <PageHeader
          title="Settings"
          icon={<SettingsIcon className="h-6 w-6 text-sidebar-foreground" />}
          subtitle={identity?.tenant?.name ?? "Workspace"}
        />

        {/* Honest preview note — only for tabs whose controls aren't wired yet. */}
        {!WIRED_TABS.has(tab) && (
          <div className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-[11px] text-muted-foreground">
            <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
            Design preview — changes aren't saved yet
          </div>
        )}

        {/* Tab bar */}
        <div className="flex flex-wrap gap-1 border-b border-border">
          {tabs.map((t) => {
            const Icon = t.icon;
            const active = tab === t.key;
            return (
              <button
                key={t.key}
                type="button"
                onClick={() => setTab(t.key)}
                className={cn(
                  "-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
                  active
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
                {t.label}
              </button>
            );
          })}
        </div>

        {tab === "account" && <AccountTab />}
        {tab === "security" && <SecurityTab />}
        {tab === "notifications" && <NotificationsTab />}
        {tab === "plan" && <PlanTab />}
        {tab === "billing" && <BillingTab />}
      </div>
    </div>
  );
}

// ----- skeleton -------------------------------------------------------------

// SettingsSkeleton mirrors the real content area while it loads: the header
// (subtitle + title), the tab bar, and a couple of section cards each with
// field placeholders + a save bar — so the whole area and its subsections come
// in as skeletons, consistent with Activity / Members / Recent.
function SettingsSkeleton({ tabCount }: { tabCount: number }) {
  return (
    <div className="flex-1 overflow-y-auto [scrollbar-color:rgba(0,0,0,0.12)_transparent] [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-foreground/10 [&::-webkit-scrollbar-track]:bg-transparent [&::-webkit-scrollbar]:w-2.5">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        {/* Header — matches PageHeader: subtitle line above the title. */}
        <div className="space-y-2">
          <Skeleton className="h-4 w-44" />
          <Skeleton className="h-8 w-36" />
        </div>

        {/* Tab bar */}
        <div className="flex flex-wrap gap-2 border-b border-border pb-2">
          {Array.from({ length: Math.max(1, tabCount) }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-24 rounded" />
          ))}
        </div>

        {/* Subsection cards */}
        <div className="space-y-6">
          <SkeletonSection fields={3} />
          <SkeletonSection fields={2} />
        </div>
      </div>
    </div>
  );
}

// SkeletonSection mirrors one Section card: a title + description, a few
// label/field rows, and a trailing save bar.
function SkeletonSection({ fields }: { fields: number }) {
  return (
    <section className="rounded-lg border border-border bg-card p-5 shadow-sm">
      <div className="mb-4 space-y-1.5">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="h-3 w-56" />
      </div>
      <div className="space-y-4">
        {Array.from({ length: fields }).map((_, i) => (
          <div key={i} className="space-y-1.5">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-7 w-full max-w-2xl" />
          </div>
        ))}
        <div className="flex justify-end gap-2 border-t border-border pt-4">
          <Skeleton className="h-7 w-20 rounded-md" />
          <Skeleton className="h-7 w-28 rounded-md" />
        </div>
      </div>
    </section>
  );
}

// ----- tabs ----------------------------------------------------------------

// MembersInvitesSection wires the invite-policy defaults: default role for new
// invites, whether admins may invite (owner-only to change), and a domain
// allowlist. Loads from / saves to /api/workspace/invite-settings.
function MembersInvitesSection() {
  const { identity } = useAuth();
  const isOwner = (identity?.membership?.role ?? "").toLowerCase().trim() === "owner";

  const [role, setRole] = useState("member");
  const [allowAdmin, setAllowAdmin] = useState(true);
  const [domains, setDomains] = useState(""); // comma-separated in the field
  const [orig, setOrig] = useState({ role: "member", allowAdmin: true, domains: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ackOpen, setAckOpen] = useState(false);

  type InviteSettings = {
    default_invite_role: string;
    allow_admin_invites: boolean;
    invite_domains: string[];
  };

  const apply = (s: InviteSettings) => {
    const d = (s.invite_domains ?? []).join(", ");
    setRole(s.default_invite_role);
    setAllowAdmin(s.allow_admin_invites);
    setDomains(d);
    setOrig({ role: s.default_invite_role, allowAdmin: s.allow_admin_invites, domains: d });
  };

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const s = await api.get<InviteSettings>("/api/workspace/invite-settings");
        if (!cancelled) apply(s);
      } catch {
        // leave defaults; the form still works and saving will create the row
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const dirty =
    role !== orig.role ||
    allowAdmin !== orig.allowAdmin ||
    domains.trim() !== orig.domains.trim();

  async function save() {
    if (!dirty || busy) return;
    setBusy(true);
    setError(null);
    try {
      const s = await api.patch<InviteSettings>("/api/workspace/invite-settings", {
        default_invite_role: role,
        allow_admin_invites: allowAdmin,
        invite_domains: domains
          .split(",")
          .map((d) => d.trim())
          .filter(Boolean),
      });
      apply(s);
      setAckOpen(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't save — try again.");
    } finally {
      setBusy(false);
    }
  }

  function cancel() {
    setRole(orig.role);
    setAllowAdmin(orig.allowAdmin);
    setDomains(orig.domains);
    setError(null);
  }

  return (
    <Section title="Members & invites" description="Defaults applied to new invitations.">
      <Field label="Default role for new invites">
        <div className="relative w-48">
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            className="h-7 w-full appearance-none rounded-md border border-foreground/25 bg-background pl-2.5 pr-8 text-xs text-foreground outline-none transition-colors hover:bg-muted focus-visible:ring-1 focus-visible:ring-primary/40"
          >
            <option value="admin">Admin</option>
            <option value="member">Member</option>
            <option value="viewer">Viewer</option>
          </select>
          <ChevronDown className="pointer-events-none absolute right-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        </div>
      </Field>
      <ToggleRow
        label="Let admins invite people"
        description={
          isOwner
            ? "When off, only the owner can send invites."
            : "Only the workspace owner can change this."
        }
        checked={allowAdmin}
        onChange={setAllowAdmin}
        disabled={!isOwner}
      />
      <Field
        label="Restrict invites to domains"
        description="Comma-separated. Leave blank to allow any email. Matched exactly — eng.acme.com isn't covered by acme.com."
      >
        <Input
          placeholder="acme.com, acme.io"
          value={domains}
          onChange={(e) => setDomains(e.target.value)}
          className={inputCls}
        />
      </Field>

      <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
        {error && (
          <span className="mr-auto text-[11px] text-destructive" role="alert">
            {error}
          </span>
        )}
        <SButton onClick={cancel} disabled={!dirty || busy} className={cn(!dirty && "opacity-50")}>
          Cancel
        </SButton>
        <SButton onClick={save} disabled={!dirty || busy} className={cn(!dirty && "opacity-50")}>
          {busy ? "Saving…" : "Save changes"}
        </SButton>
      </div>

      <AckModal
        open={ackOpen}
        message="Your changes have been saved."
        onClose={() => setAckOpen(false)}
      />
    </Section>
  );
}

function AccountTab() {
  const { identity, setIdentity, refresh } = useAuth();
  const navigate = useNavigate();
  const [deleteOpen, setDeleteOpen] = useState(false);
  // Only the account owner may delete the whole account (admins can't).
  const isOwner = identity?.membership?.role === "owner";
  const isAdmin = isAdminOrOwner(identity?.membership?.role);
  const email = identity?.user.email ?? "";
  const origFirst = (identity?.user.first_name ?? "").trim();
  const origLast = (identity?.user.last_name ?? "").trim();

  const [first, setFirst] = useState(origFirst);
  const [last, setLast] = useState(origLast);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ackOpen, setAckOpen] = useState(false);
  const [avatarOpen, setAvatarOpen] = useState(false);
  const [emailOpen, setEmailOpen] = useState(false);

  // Avatar preview: the saved image (avatar_url) with live-typed initials as
  // the fallback, so name edits show before saving even without a photo.
  const previewUser = {
    first_name: first,
    last_name: last,
    email,
    avatar_url: identity?.user.avatar_url,
  };
  const hasAvatar = !!identity?.user.avatar_url;

  // Reseed when the underlying identity changes (e.g. saved here, or refreshed
  // elsewhere) so the fields track the source of truth.
  useEffect(() => {
    setFirst(origFirst);
    setLast(origLast);
  }, [origFirst, origLast]);

  const dirty = first.trim() !== origFirst || last.trim() !== origLast;
  const valid = first.trim() !== "" && last.trim() !== "";

  async function save() {
    if (!dirty || !valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const updated = await api.patch<Identity>("/api/me", {
        first_name: first.trim(),
        last_name: last.trim(),
      });
      setIdentity(updated); // refreshes nav greeting + avatar app-wide
      setAckOpen(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't save — try again.");
    } finally {
      setBusy(false);
    }
  }

  function cancel() {
    setFirst(origFirst);
    setLast(origLast);
    setError(null);
  }

  return (
    <div className="space-y-6">
      <Section title="Profile" description="How you appear across the workspace.">
        <div className="flex items-center gap-4">
          <UserAvatar
            user={previewUser}
            className="h-14 w-14 border-2 border-primary/20"
            fallbackClassName="text-lg font-semibold"
          />
          <SButton onClick={() => setAvatarOpen(true)} className="ml-auto">
            {hasAvatar ? "Change photo" : "Upload photo"}
          </SButton>
        </div>
        <Field label="First name" htmlFor="acct-first">
          <Input
            id="acct-first"
            placeholder="First"
            value={first}
            maxLength={60}
            autoComplete="given-name"
            onChange={(e) => setFirst(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Field label="Last name" htmlFor="acct-last">
          <Input
            id="acct-last"
            placeholder="Last"
            value={last}
            maxLength={60}
            autoComplete="family-name"
            onChange={(e) => setLast(e.target.value)}
            className={inputCls}
          />
        </Field>
        <InfoRow
          label="Email"
          value={email}
          trailing={
            <button
              type="button"
              onClick={() => setEmailOpen(true)}
              className="inline-flex h-7 items-center justify-center rounded-md border border-foreground/30 bg-background px-3 text-[11px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted"
            >
              Change
            </button>
          }
        />
        <InfoRow label="Role" value={cap(identity?.membership?.role ?? "member")} />
        <InfoRow label="Member since" value={fmtDate(identity?.user.created_at)} />

        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {error && (
            <span className="mr-auto text-[11px] text-destructive" role="alert">
              {error}
            </span>
          )}
          <SButton
            onClick={cancel}
            disabled={!dirty || busy}
            className={cn(!dirty && "opacity-50")}
          >
            Cancel
          </SButton>
          <SButton
            onClick={save}
            disabled={!dirty || !valid || busy}
            className={cn((!dirty || !valid) && "opacity-50")}
          >
            {busy ? "Saving…" : "Save changes"}
          </SButton>
        </div>
      </Section>

      {/* Account-level administration — owner/admin only. */}
      {isAdmin && <MembersInvitesSection />}

      {isOwner && (
        <Section danger>
          <DangerRow
            label="Delete account"
            description="Schedule this account for deletion. It goes read-only and is permanently erased after 30 days — reactivate any time before then. Accounts under 30 days old are fully refunded."
            action="Delete account"
            destructive
            onClick={() => setDeleteOpen(true)}
          />
        </Section>
      )}

      <DeleteAccountModal
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        onDeleted={async () => {
          setDeleteOpen(false);
          // The account is now read-only (closing); refresh identity so the
          // app reflects it, then land on Home where the banner explains it.
          await refresh({ silent: true });
          navigate("/home", { replace: true });
        }}
      />

      <AvatarUploadModal
        open={avatarOpen}
        hasAvatar={hasAvatar}
        onClose={() => setAvatarOpen(false)}
        onSaved={(updated) => setIdentity(updated)}
      />
      <ChangeEmailModal
        open={emailOpen}
        currentEmail={email}
        onClose={() => setEmailOpen(false)}
      />
      <AckModal
        open={ackOpen}
        message="Your profile changes have been saved."
        onClose={() => setAckOpen(false)}
      />
    </div>
  );
}

function SecurityTab() {
  const { identity } = useAuth();
  const email = identity?.user.email ?? "your email";

  // Authenticator-app (TOTP) 2FA — the one wired part of this tab.
  const [twofa, setTwofa] = useState<{ enabled: boolean; pending: boolean; recovery_remaining: number } | null>(null);
  const [setupOpen, setSetupOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const loadTwofa = () => {
    void api
      .get<{ enabled: boolean; pending: boolean; recovery_remaining: number }>("/api/me/2fa")
      .then(setTwofa)
      .catch(() => setTwofa({ enabled: false, pending: false, recovery_remaining: 0 }));
  };
  useEffect(loadTwofa, []);
  const totpEnabled = !!twofa?.enabled;

  // Passkeys (WebAuthn) — a user may register several. The card shows "Active"
  // when at least one exists; individual passkeys are managed in the list below.
  type Passkey = { id: number; label: string; created_at: string; last_used_at?: string };
  const [passkeys, setPasskeys] = useState<Passkey[]>([]);
  const [pkLoaded, setPkLoaded] = useState(false);
  const [pkBusy, setPkBusy] = useState(false);
  const [pkError, setPkError] = useState<string | null>(null);
  const pkSupported = passkeysSupported();
  const loadPasskeys = () => {
    void api
      .get<{ passkeys: Passkey[] }>("/api/me/passkeys")
      .then((r) => setPasskeys(r.passkeys ?? []))
      .catch(() => setPasskeys([]))
      .finally(() => setPkLoaded(true));
  };
  useEffect(loadPasskeys, []);
  const addPasskey = async () => {
    if (pkBusy) return;
    setPkBusy(true);
    setPkError(null);
    try {
      await registerPasskey();
      loadPasskeys();
    } catch (err) {
      setPkError(passkeyErrorMessage(err, "Couldn't set up the passkey. Try again."));
    } finally {
      setPkBusy(false);
    }
  };
  const removePasskey = async (id: number) => {
    try {
      await api.delete(`/api/me/passkeys/${id}`);
      loadPasskeys();
    } catch {
      /* leave the list as-is; the row stays */
    }
  };
  const hasPasskeys = passkeys.length > 0;
  // Both factor lookups must resolve before we render status-dependent bits
  // (the off-alert, the Inactive pill, the recommended tab) — otherwise they
  // flash on with default "off" state and then correct themselves on load.
  const securityLoaded = twofa !== null && pkLoaded;

  // Active sessions — real data. Owner/admin see the whole tenant; everyone
  // else sees only their own. The current device can't be selected/signed out.
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  const [selectedSessions, setSelectedSessions] = useState<string[]>([]);
  const [signingOut, setSigningOut] = useState(false);
  const loadSessions = () => {
    void api
      .get<{ sessions: SessionRow[] }>("/api/sessions")
      .then((r) => setSessions(r.sessions ?? []))
      .catch(() => setSessions([]))
      .finally(() => setSessionsLoaded(true));
  };
  useEffect(loadSessions, []);

  const selectableSessions = sessions.filter((s) => !s.current);
  const allSessionsSelected =
    selectableSessions.length > 0 && selectedSessions.length === selectableSessions.length;
  const someSessionsSelected = selectedSessions.length > 0 && !allSessionsSelected;
  const toggleAllSessions = () =>
    setSelectedSessions(allSessionsSelected ? [] : selectableSessions.map((s) => s.ref));
  const toggleSession = (ref: string) =>
    setSelectedSessions((prev) =>
      prev.includes(ref) ? prev.filter((x) => x !== ref) : [...prev, ref],
    );
  const signOutSelected = async () => {
    if (signingOut || selectedSessions.length === 0) return;
    setSigningOut(true);
    try {
      await api.post("/api/sessions/revoke", { refs: selectedSessions });
      setSelectedSessions([]);
      loadSessions();
    } catch {
      /* leave selection as-is on failure */
    } finally {
      setSigningOut(false);
    }
  };

  // Pagination — the list is tenant-wide and can be large.
  const [sessionPage, setSessionPage] = useState(0);
  const totalSessions = sessions.length;
  const sessionPageCount = Math.max(1, Math.ceil(totalSessions / SESSIONS_PAGE_SIZE));
  const sessionStart = sessionPage * SESSIONS_PAGE_SIZE;
  const pageSessions = sessions.slice(sessionStart, sessionStart + SESSIONS_PAGE_SIZE);

  // Hold the whole tab until every section's data is in (2FA + passkeys +
  // sessions), then reveal it all at once. Otherwise the static scaffolding
  // paints immediately and status-dependent bits — notably the amber
  // "2FA is off" banner — pop in a beat later once their fetches resolve.
  const ready = securityLoaded && sessionsLoaded;
  if (!ready) {
    return (
      <div className="space-y-6">
        {[0, 1, 2].map((i) => (
          <div key={i} className="rounded-xl border border-border bg-card p-5">
            <div className="h-4 w-44 animate-pulse rounded bg-foreground/10" />
            <div className="mt-2 h-3 w-72 max-w-full animate-pulse rounded bg-foreground/5" />
            <div className="mt-4 space-y-2.5">
              <div className="h-12 animate-pulse rounded-lg bg-foreground/5" />
              <div className="h-12 animate-pulse rounded-lg bg-foreground/5" />
            </div>
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <Section
        title="Two-factor authentication"
        description="Add a second step at sign-in. Even if your password is stolen, your account stays locked without your device."
      >
        {/* Status banner — only while NO second factor is active (a call to
            act). Once either the authenticator app OR a passkey is set up, the
            account has a second factor, so the banner disappears. */}
        {securityLoaded && !totpEnabled && !hasPasskeys && (
          <div className="flex items-center gap-3 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
            <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-amber-500/10">
              <Shield className="h-5 w-5 text-amber-600" />
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-foreground">Two-factor authentication is off</p>
              <p className="text-[11px] text-muted-foreground">
                Your account is protected by your password alone. Add a method below to turn it on.
              </p>
            </div>
            <span className="flex-shrink-0 rounded-full bg-amber-500/15 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-700">
              Off
            </span>
          </div>
        )}

        {/* The two methods are the call to action — separate, equal-weight cards.
            You can add both (passkey as primary, authenticator as backup).
            pt-6 pushes both cards down so there's an adequate gap below the
            "2FA is off" alert and headroom for the "Highly recommended" tab
            that pokes up above the authenticator card; both cards sit level. */}
        <div className="grid gap-3 pt-6 sm:grid-cols-2">
          <MethodCard
            brand={
              <div className="flex items-center gap-2.5">
                <span className="flex h-16 w-16 items-center justify-center rounded-xl border border-border bg-white shadow-sm">
                  <GoogleAuthenticatorLogo className="h-10 w-10" />
                </span>
                <span className="flex h-16 w-16 items-center justify-center rounded-xl border border-border bg-white shadow-sm">
                  <AuthyLogo className="h-10 w-10" />
                </span>
              </div>
            }
            title="2FA-authenticator app"
            ribbon={securityLoaded && !totpEnabled ? "Highly recommended" : undefined}
            showInactive={securityLoaded}
            description="Scan a QR code with Google Authenticator, Authy, or any TOTP app to get time-based codes. Works offline and resists phishing."
            action="Set up"
            enabled={totpEnabled}
            onAction={() => setSetupOpen(true)}
            onRemove={() => setDisableOpen(true)}
          />
          <MethodCard
            brand={<PasskeyGlyph />}
            title="Set-Up Passkey"
            description="Sign in with Face ID, Touch ID, or a hardware key (WebAuthn) — the strongest, phishing-proof option. No password needed."
            action={pkBusy ? "Setting up…" : hasPasskeys ? "Add another" : "Set up passkey"}
            enabled={hasPasskeys}
            alwaysAction
            actionDisabled={!pkSupported || pkBusy}
            onAction={() => void addPasskey()}
          />
        </div>

        {/* Passkey management — list each registered passkey with a remove. */}
        {pkError && (
          <p className="text-[11px] text-destructive" role="alert">
            {pkError}
          </p>
        )}
        {!pkSupported && (
          <p className="text-[11px] text-muted-foreground">
            This browser doesn't support passkeys. Try a recent Chrome, Safari, or Edge.
          </p>
        )}
        {hasPasskeys && (
          <div className="divide-y divide-border overflow-hidden rounded-lg border border-border">
            {passkeys.map((pk) => (
              <div key={pk.id} className="flex items-center gap-3 px-3 py-2">
                <Fingerprint className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-medium text-foreground">{pk.label}</p>
                  <p className="text-[11px] text-muted-foreground">
                    Added {new Date(pk.created_at).toLocaleDateString()}
                    {pk.last_used_at
                      ? ` · last used ${new Date(pk.last_used_at).toLocaleDateString()}`
                      : " · not used yet"}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => void removePasskey(pk.id)}
                  className="flex-shrink-0 rounded-md px-2 py-1 text-[11px] font-medium text-destructive transition-colors hover:bg-destructive/10"
                >
                  Remove
                </button>
              </div>
            ))}
          </div>
        )}

        {/* Recovery codes note. */}
        <div className="flex items-start gap-2 rounded-lg border border-dashed border-border p-3">
          <Lightbulb className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
          <p className="text-[11px] text-muted-foreground">
            When you turn on 2FA we'll generate one-time backup codes — keep them somewhere safe so
            you can regain access if you lose your device.
          </p>
        </div>
      </Section>

      <Section title="Password" description="Use a strong, unique password.">
        <div className="flex items-center gap-3 rounded-lg border border-dashed border-border p-3">
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-secondary">
            <KeyRound className="h-5 w-5 text-muted-foreground" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-foreground">Reset your password by email</p>
            <p className="text-[11px] text-muted-foreground">
              {totpEnabled
                ? "We'll confirm your authenticator code, then email a one-time link to "
                : hasPasskeys
                  ? "We'll verify your passkey, then email a one-time link to "
                  : "We'll email a one-time link to "}
              <span className="font-medium text-foreground">{email}</span> to set a new password.
            </p>
          </div>
          <SButton onClick={() => setResetOpen(true)} className="flex-shrink-0">
            Send reset link
          </SButton>
        </div>
      </Section>

      <Section
        title="Active sessions"
        description="Everyone currently signed in to this account. Select sessions to sign them out."
      >
        {/* Graphical lead — consistent with the 2FA / password reset blocks. */}
        <div className="flex items-center gap-3 rounded-lg border border-dashed border-border p-3">
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-secondary">
            <MonitorSmartphone className="h-5 w-5 text-muted-foreground" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-foreground">Devices with an active session</p>
            <p className="text-[11px] text-muted-foreground">
              Review where this account is signed in and sign out anything you don't recognize.
            </p>
          </div>
        </div>

        <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
          <div className="max-h-[420px] overflow-y-auto [scrollbar-color:rgba(0,0,0,0.1)_transparent] [&::-webkit-scrollbar]:w-3 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-foreground/10 [&::-webkit-scrollbar-track]:bg-transparent">
            <table className="w-full table-fixed text-sm">
              <colgroup>
                <col style={{ width: "44px" }} />
                <col style={{ width: "30%" }} />
                <col style={{ width: "22%" }} />
                <col style={{ width: "16%" }} />
                <col style={{ width: "18%" }} />
                <col style={{ width: "16%" }} />
              </colgroup>
              <thead className="sticky top-0 z-10 bg-card">
                <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                  <th className="px-4 py-2.5 text-left">
                    <Checkbox
                      checked={allSessionsSelected}
                      ref={(el) => {
                        if (el) el.indeterminate = someSessionsSelected;
                      }}
                      onChange={toggleAllSessions}
                      disabled={selectableSessions.length === 0}
                      aria-label="Select all other sessions"
                    />
                  </th>
                  <th className="px-4 py-2.5 text-left">Who</th>
                  <th className="px-4 py-2.5 text-left">Device</th>
                  <th className="hidden px-4 py-2.5 text-left lg:table-cell">Location</th>
                  <th className="hidden px-4 py-2.5 text-left md:table-cell">IP address</th>
                  <th className="px-4 py-2.5 text-left">Last active</th>
                </tr>
              </thead>
              <tbody>
                {/* Top spacer — breathing room below the sticky header. */}
                <tr aria-hidden="true">
                  <td colSpan={6} className="h-2 p-0" />
                </tr>
                {pageSessions.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-6 text-center text-[12px] text-muted-foreground">
                      No active sessions.
                    </td>
                  </tr>
                )}
                {pageSessions.map((s) => {
                  const selected = selectedSessions.includes(s.ref);
                  const dev = parseUserAgent(s.user_agent);
                  const DeviceIcon = dev.mobile ? Smartphone : Laptop;
                  const who = s.name || s.email;
                  const lastActive = relativeTime(s.last_seen_at);
                  const isLive = lastActive === "Active now";
                  return (
                    <tr key={s.ref} className="transition-colors hover:bg-muted/40">
                      <td className="px-4 py-2">
                        <Checkbox
                          checked={selected}
                          disabled={s.current}
                          onChange={() => toggleSession(s.ref)}
                          aria-label={`Select ${who}'s ${dev.browser} session`}
                          title={s.current ? "You can't sign out your current session" : undefined}
                        />
                      </td>
                      <td className="px-4 py-2">
                        <div className="min-w-0">
                          <div className="flex items-center gap-1.5">
                            <span className="truncate text-[12.5px] font-medium text-foreground">{who}</span>
                            <span className="flex-shrink-0 text-[10px] uppercase tracking-wide text-muted-foreground/80">
                              {s.role}
                            </span>
                          </div>
                          <p className="truncate text-[11px] text-muted-foreground">{s.email}</p>
                        </div>
                      </td>
                      <td className="px-4 py-2">
                        <div className="flex items-center gap-2.5">
                          <DeviceIcon className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                          <div className="min-w-0">
                            <div className="flex items-center gap-1.5">
                              <span className="truncate text-[12.5px] text-foreground">{dev.browser}</span>
                              {s.current && (
                                <span className="flex-shrink-0 rounded-full bg-emerald-500/10 px-1.5 py-0.5 text-[9.5px] font-medium text-emerald-600">
                                  This device
                                </span>
                              )}
                            </div>
                            <p className="truncate text-[11px] text-muted-foreground">{dev.os}</p>
                          </div>
                        </div>
                      </td>
                      <td className="hidden px-4 py-2 text-[12px] text-muted-foreground lg:table-cell">
                        {composeLocation(s)}
                      </td>
                      <td className="hidden px-4 py-2 font-mono text-[11.5px] text-muted-foreground md:table-cell">
                        {/* Truncate the long IPv6 (with full value on hover); a
                            short IPv4 fits and stays whole. */}
                        <span className="block truncate" title={s.ip}>
                          {s.ip}
                        </span>
                      </td>
                      <td className="px-4 py-2 text-[12px] text-muted-foreground">
                        <span className="flex items-center gap-1.5">
                          {isLive && <span className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-emerald-500" />}
                          {lastActive}
                        </span>
                      </td>
                    </tr>
                  );
                })}
                {/* Bottom spacer — matches the top gap. */}
                <tr aria-hidden="true">
                  <td colSpan={6} className="h-2 p-0" />
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        {/* Footer: pagination on the left, one dynamic Sign out action on the
            right (idle → count → red "all other sessions" when all selected). */}
        <div className="flex flex-wrap items-center gap-3 pt-1">
          <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <span>
              {sessionStart + 1}–{Math.min(sessionStart + SESSIONS_PAGE_SIZE, totalSessions)} of{" "}
              <span className="tabular-nums">{totalSessions}</span>
            </span>
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setSessionPage((p) => Math.max(0, p - 1))}
                disabled={sessionPage === 0}
                className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="Previous page"
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </button>
              <button
                type="button"
                onClick={() => setSessionPage((p) => Math.min(sessionPageCount - 1, p + 1))}
                disabled={sessionPage >= sessionPageCount - 1}
                className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                aria-label="Next page"
              >
                <ChevronRight className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
          <SButton
            disabled={selectedSessions.length === 0 || signingOut}
            onClick={() => void signOutSelected()}
            danger={allSessionsSelected}
            className={cn("ml-auto", selectedSessions.length === 0 && "opacity-50")}
          >
            {signingOut
              ? "Signing out…"
              : selectedSessions.length === 0
                ? "Sign out"
                : allSessionsSelected
                  ? "Sign out all other sessions"
                  : `Sign out (${selectedSessions.length})`}
          </SButton>
        </div>
      </Section>

      <TwoFactorSetupModal
        open={setupOpen}
        accountEmail={email}
        onClose={() => {
          setSetupOpen(false);
          loadTwofa();
        }}
        onEnabled={loadTwofa}
      />
      <TwoFactorDisableModal
        open={disableOpen}
        onClose={() => setDisableOpen(false)}
        onDisabled={loadTwofa}
      />
      <PasswordResetModal
        open={resetOpen}
        onClose={() => setResetOpen(false)}
        totpEnabled={totpEnabled}
        hasPasskeys={hasPasskeys}
        accountEmail={email}
      />
    </div>
  );
}

function NotificationsTab() {
  const [prefs, setPrefs] = useState<Record<string, boolean> | null>(null);
  const [saving, setSaving] = useState(false);
  const [applied, setApplied] = useState(false);

  useEffect(() => {
    void api
      .get<{ prefs: Record<string, boolean> }>("/api/me/notifications")
      .then((r) => setPrefs(r.prefs ?? {}))
      .catch(() => setPrefs({}));
  }, []);

  const get = (key: string) => (prefs ? prefs[key] ?? true : true);
  const set = (key: string) => (next: boolean) => setPrefs((p) => ({ ...(p ?? {}), [key]: next }));

  const save = async () => {
    if (saving || !prefs) return;
    setSaving(true);
    try {
      const r = await api.put<{ prefs: Record<string, boolean> }>("/api/me/notifications", { prefs });
      setPrefs(r.prefs);
      setApplied(true);
    } catch {
      /* leave toggles as-is on failure */
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <Section title="Email notifications" description="Choose what lands in your inbox.">
        <div className="space-y-4">
          <div className="space-y-2">
            <NotifGroup label="Workspace activity" />
            <NotifRow
              icon={UserPlus}
              title="Invite accepted"
              description="When someone you invited joins the workspace."
              checked={get("invite_accepted")}
              onChange={set("invite_accepted")}
            />
            <NotifRow
              icon={UserMinus}
              title="Member removed"
              description="When a member is removed from the workspace."
              checked={get("member_removed")}
              onChange={set("member_removed")}
            />
          </div>

          <div className="space-y-2">
            <NotifGroup label="Digests & updates" />
            <NotifRow
              icon={CalendarClock}
              title="Weekly activity digest"
              description="A Monday summary of files, members, and changes across your workspace."
              checked={get("weekly_digest")}
              onChange={set("weekly_digest")}
            />
            <NotifRow
              icon={Mail}
              title="Trilli newsletter"
              description="Product news, tips, and new features — about once a month. No spam."
              checked={get("newsletter")}
              onChange={set("newsletter")}
            />
          </div>

          <ComplianceFooter />

          <div className="flex justify-end pt-1">
            <SButton onClick={() => void save()} disabled={saving || !prefs}>
              {saving ? "Saving…" : "Update settings"}
            </SButton>
          </div>
        </div>
      </Section>

      <AppliedModal
        open={applied}
        title="Settings applied"
        message="Your notification preferences have been saved."
        onClose={() => setApplied(false)}
      />
    </div>
  );
}

// NotifGroup is a small uppercase subsection heading inside Notifications.
function NotifGroup({ label }: { label: string }) {
  return (
    <p className="px-0.5 text-[10.5px] font-semibold uppercase tracking-wide text-muted-foreground/70">
      {label}
    </p>
  );
}

// NotifRow is a dressed-up notification preference: icon tile + title +
// description + a switch. Controlled by the parent tab.
function NotifRow({
  icon: Icon,
  title,
  description,
  checked,
  onChange,
  disabled = false,
  disabledHint,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  disabledHint?: string;
}) {
  return (
    <div
      className={cn(
        "flex items-center gap-3 rounded-lg border border-border bg-card px-3.5 py-3 transition-colors",
        disabled ? "opacity-50" : "hover:border-foreground/20",
      )}
    >
      <span
        className={cn(
          "flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg transition-colors",
          checked ? "bg-primary/10 text-primary" : "bg-secondary text-muted-foreground",
        )}
      >
        <Icon className="h-[18px] w-[18px]" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-medium text-foreground">{title}</p>
        <p className="text-[11px] leading-relaxed text-muted-foreground">
          {disabled && disabledHint ? disabledHint : description}
        </p>
      </div>
      <button
        type="button"
        onClick={() => !disabled && onChange(!checked)}
        disabled={disabled}
        aria-pressed={checked}
        aria-label={title}
        title={disabled ? disabledHint : undefined}
        className={cn(
          "relative inline-flex h-[17px] w-[31px] flex-shrink-0 items-center rounded-full transition-colors",
          checked ? "bg-primary" : "bg-muted",
          disabled && "cursor-not-allowed",
        )}
      >
        <span
          className={cn(
            "inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform",
            checked ? "translate-x-[15px]" : "translate-x-0.5",
          )}
        />
      </button>
    </div>
  );
}

// ComplianceFooter reassures users about our email compliance posture, with
// understated grayscale badges (these laws have no official marks, so we use
// tasteful gray pills rather than fabricated logos).
function ComplianceFooter() {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/20 p-3.5">
      <div className="flex items-start gap-2">
        <ShieldCheck className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
        <p className="text-[11px] leading-relaxed text-muted-foreground">
          We respect your inbox. Trilli email complies with the{" "}
          <span className="font-medium text-foreground/80">CAN-SPAM Act</span>,{" "}
          <span className="font-medium text-foreground/80">GDPR</span>, and{" "}
          <span className="font-medium text-foreground/80">COPPA</span>. Every message includes
          one-click unsubscribe, and we never sell your data.
        </p>
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-2 pl-5">
        <ComplianceBadge icon={Mail} label="CAN-SPAM" />
        <ComplianceBadge icon={Globe} label="GDPR" />
        <ComplianceBadge icon={Shield} label="COPPA" />
      </div>
    </div>
  );
}

function ComplianceBadge({ icon: Icon, label }: { icon: LucideIcon; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-border bg-card px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground/70 grayscale">
      <Icon className="h-3 w-3" />
      {label}
    </span>
  );
}

function fmtPrice(n: number): string {
  return `$${n.toFixed(2)}`;
}
// Annual per-month rate, derived from the plan's own discount lever.
function annualMonthlyRate(monthly: number, discountPct: number): number {
  return monthly * (1 - discountPct / 100);
}

// Plan glyph icons, keyed by the plan's icon_key from the catalog.
const PLAN_ICONS: Record<string, LucideIcon> = {
  feather: Feather,
  plus: Plus,
  infinity: InfinityIcon,
};

type BillingPeriod = "monthly" | "annual";

function PlanTab() {
  const { identity, refresh } = useAuth();
  const currentPlanId = identity?.tenant?.plan_id ?? null;
  const isAdmin = isAdminOrOwner(identity?.membership?.role);

  const [plans, setPlans] = useState<CatalogPlan[]>([]);
  const [catalogDiscount, setCatalogDiscount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [billing, setBilling] = useState<BillingPeriod>("annual");
  const [switching, setSwitching] = useState<string | null>(null); // plan code in flight
  const [changeMsg, setChangeMsg] = useState<{ title: string; message: string } | null>(null);
  // In-place plan change: hold the preview + the target plan until the user
  // confirms in the modal (so we never switch the plan without confirmation).
  const [preview, setPreview] = useState<{ plan: CatalogPlan; data: ChangePreviewResult } | null>(
    null,
  );
  const [confirming, setConfirming] = useState(false);
  const [confirmError, setConfirmError] = useState<string | null>(null);
  const navigate = useNavigate();

  // Switch plans. First purchase (no live subscription) → dedicated checkout
  // page. Existing subscriber → fetch a read-only PREVIEW (prorated charge /
  // effective date) and open a confirmation modal; the change is only applied
  // once the user confirms there.
  const startUpgrade = async (plan: CatalogPlan) => {
    setSwitching(plan.code);
    setConfirmError(null);
    try {
      const r = await api.post<ChangePreviewResult>("/api/billing/preview-change", {
        code: plan.code,
        billing_period: billing,
      });
      if (r.needs_checkout) {
        navigate(`/checkout?plan=${plan.code}&billing=${billing}`);
        return;
      }
      setPreview({ plan, data: r });
    } catch (e) {
      setChangeMsg({
        title: "Couldn't load plan change",
        message: e instanceof ApiError && e.message ? e.message : "Please try again.",
      });
    } finally {
      setSwitching(null);
    }
  };

  // Commit the change the user just confirmed in the modal.
  const confirmChange = async () => {
    if (!preview) return;
    setConfirming(true);
    setConfirmError(null);
    try {
      const r = await api.post<ChangePlanResult>("/api/billing/change-plan", {
        code: preview.plan.code,
        billing_period: billing,
      });
      if (r.needs_checkout) {
        navigate(`/checkout?plan=${preview.plan.code}&billing=${billing}`);
        return;
      }
      await refresh({ silent: true });
      setPreview(null);
      if (r.effect === "scheduled" && r.effective_at) {
        setChangeMsg({
          title: "Change scheduled",
          message: `Your plan switches to ${r.plan_name} on ${new Date(r.effective_at).toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" })}. You keep your current plan until then.`,
        });
      } else {
        setChangeMsg({ title: "Plan updated", message: `You're now on the ${r.plan_name} plan.` });
      }
    } catch (e) {
      setConfirmError(e instanceof ApiError && e.message ? e.message : "Please try again.");
    } finally {
      setConfirming(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    api
      .get<PlansResponse>("/api/plans")
      .then((r) => {
        if (cancelled) return;
        setPlans(r.plans ?? []);
        setCatalogDiscount(r.annual_discount_pct ?? 0);
      })
      .catch(() => {
        if (!cancelled) setError("Couldn't load plans.");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // More than 3 plans → horizontal carousel with left/right scrollers.
  const carousel = plans.length > 3;
  const scrollRef = useRef<HTMLDivElement>(null);
  const scrollByCard = (dir: number) => {
    const el = scrollRef.current;
    if (!el) return;
    const card = el.querySelector<HTMLElement>("[data-plan-card]");
    const amount = card ? card.offsetWidth + 16 : Math.round(el.clientWidth * 0.8);
    el.scrollBy({ left: dir * amount, behavior: "smooth" });
  };

  return (
    <div className="space-y-6">
      <Section title="Plans" description="Compare plans and pick the one that fits your team.">
        {/* Billing period toggle — Annual (default) / Monthly — with a
            prominent savings callout right under the selector. */}
        <div className="flex flex-col items-center gap-2.5 pt-4">
          <div className="inline-flex items-center rounded-lg border border-border bg-muted/40 p-0.5 text-[12px] font-medium">
            <button
              type="button"
              onClick={() => setBilling("annual")}
              className={cn(
                "rounded-md px-3.5 py-1.5 transition-colors",
                billing === "annual"
                  ? "bg-card text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              Annual
            </button>
            <button
              type="button"
              onClick={() => setBilling("monthly")}
              className={cn(
                "rounded-md px-3.5 py-1.5 transition-colors",
                billing === "monthly"
                  ? "bg-card text-foreground shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              Monthly
            </button>
          </div>
          {catalogDiscount > 0 && (
            <span className="inline-flex items-center gap-1.5 text-[14px] font-medium text-emerald-700">
              <PiggyBank className="h-[18px] w-[18px]" />
              Save {catalogDiscount}% with annual billing
            </span>
          )}
        </div>

        {loading ? (
          <div className="flex justify-center py-10 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : error ? (
          <p className="py-10 text-center text-[13px] text-muted-foreground">{error}</p>
        ) : carousel ? (
          /* Plan comparison. Up to 3 plans sit centered; more than 3 switch to a
             horizontal carousel with left/right scrollers. */
          <div className="relative pt-4">
            <button
              type="button"
              aria-label="Previous plans"
              onClick={() => scrollByCard(-1)}
              className="absolute -left-3 top-1/2 z-10 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-md hover:bg-muted hover:text-foreground"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <div
              ref={scrollRef}
              className="flex snap-x snap-mandatory gap-4 overflow-x-auto scroll-smooth pb-2 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            >
              {plans.map((p) => (
                <PlanCard
                  key={p.code}
                  plan={p}
                  billing={billing}
                  current={p.id === currentPlanId}
                  canUpgrade={isAdmin}
                  onUpgrade={startUpgrade}
                  carousel
                />
              ))}
            </div>
            <button
              type="button"
              aria-label="Next plans"
              onClick={() => scrollByCard(1)}
              className="absolute -right-3 top-1/2 z-10 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-md hover:bg-muted hover:text-foreground"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        ) : (
          <div className="flex flex-col gap-4 pt-4 md:flex-row md:justify-center">
            {plans.map((p) => (
              <PlanCard
                key={p.code}
                plan={p}
                billing={billing}
                current={p.id === currentPlanId}
                canUpgrade={isAdmin}
                onUpgrade={startUpgrade}
                switching={switching === p.code}
                disabledAll={switching !== null}
              />
            ))}
          </div>
        )}

      </Section>

      {preview && (
        <PlanChangeConfirmModal
          plan={preview.plan}
          preview={preview.data}
          busy={confirming}
          error={confirmError}
          onConfirm={confirmChange}
          onClose={() => {
            if (!confirming) {
              setPreview(null);
              setConfirmError(null);
            }
          }}
        />
      )}

      <AppliedModal
        open={changeMsg !== null}
        title={changeMsg?.title ?? ""}
        message={changeMsg?.message ?? ""}
        onClose={() => setChangeMsg(null)}
      />
    </div>
  );
}

function PlanCard({
  plan,
  billing,
  current,
  canUpgrade,
  onUpgrade,
  switching,
  disabledAll,
  carousel,
}: {
  plan: CatalogPlan;
  billing: BillingPeriod;
  current?: boolean;
  canUpgrade?: boolean;
  onUpgrade?: (plan: CatalogPlan) => void;
  switching?: boolean;
  disabledAll?: boolean;
  carousel?: boolean;
}) {
  const { name, callout, tagline, features, popular } = plan;
  const Icon = PLAN_ICONS[plan.icon] ?? Feather;
  const isAnnual = billing === "annual";
  // Headline is always a per-month figure: annual shows the lower billed-yearly
  // rate, monthly shows the true month-to-month rate.
  const annualMonthly = annualMonthlyRate(plan.price_monthly, plan.annual_discount_pct);
  const price = isAnnual ? annualMonthly : plan.price_monthly;
  const annualTotal = annualMonthly * 12;
  return (
    <div
      data-plan-card
      className={cn(
        "relative flex w-full flex-col rounded-xl border bg-card p-5",
        carousel
          ? // Fixed-ish width so 3 fit the viewport and the rest scroll.
            "shrink-0 snap-start w-[86%] sm:w-[55%] md:w-[calc((100%_-_2rem)/3)]"
          : // ~98% of a full third-width (2 gaps of 1rem subtracted first).
            "md:w-[calc((100%_-_2rem)/3*0.982)] md:flex-shrink-0",
        current ? "border-primary ring-1 ring-primary/30" : "border-border",
      )}
    >
      {/* Marketing callout pill. The account's current plan shows a Current
          badge; otherwise each plan shows its own callout, popular accentuated. */}
      {current ? (
        <span className="absolute right-4 top-4 rounded-full bg-emerald-500/15 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700">
          Current
        </span>
      ) : (
        <span
          className={cn(
            "absolute right-4 top-4 rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide",
            popular ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground",
          )}
        >
          {callout}
        </span>
      )}

      {/* Plan name with its glyph accent to the right of the wording. */}
      <div className="flex items-center gap-2">
        <h4 className="text-xl font-bold tracking-tight text-foreground">{name}</h4>
        <span className="flex h-8 w-8 items-center justify-center rounded-xl bg-primary/10 text-primary">
          <Icon className="h-5 w-5" strokeWidth={2.25} />
        </span>
      </div>
      <div className="mt-2 flex items-baseline gap-1.5">
        <span className="text-3xl font-bold text-foreground">{fmtPrice(price)}</span>
        <span className="text-[11px] text-muted-foreground">/ month</span>
      </div>
      <p className="mt-1 text-[11.5px] text-muted-foreground">
        {isAnnual ? `billed annually — ${fmtPrice(annualTotal)}/yr` : "billed month to month"}
      </p>
      <p className="mt-1.5 min-h-[32px] text-[11.5px] leading-relaxed text-muted-foreground">{tagline}</p>

      <div className="my-4 border-t border-border" />

      <ul className="flex-1 space-y-2">
        {features.map((f) => (
          <li key={f} className="flex items-start gap-2 text-[12px] text-foreground">
            <Check className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-emerald-600" />
            <span className="leading-snug">{f}</span>
          </li>
        ))}
      </ul>

      {current ? (
        <button
          type="button"
          disabled
          className="mt-5 inline-flex h-9 w-full items-center justify-center rounded-md border border-primary/40 bg-primary/5 text-[12.5px] font-medium text-primary"
        >
          Current plan
        </button>
      ) : (
        <button
          type="button"
          disabled={!canUpgrade || disabledAll}
          title={canUpgrade ? undefined : "Only owners and admins can change the plan"}
          onClick={() => onUpgrade?.(plan)}
          className="mt-5 inline-flex h-9 w-full items-center justify-center gap-2 rounded-md bg-primary text-[12.5px] font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {switching ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
          Switch to {name}
        </button>
      )}
    </div>
  );
}

// ----- active sessions helpers ----------------------------------------------

const SESSIONS_PAGE_SIZE = 8;

// SessionRow mirrors auth.SessionView from GET /api/sessions.
type SessionRow = {
  ref: string;
  name: string;
  email: string;
  role: string;
  ip: string;
  ip_v4?: string;
  ip_v6?: string;
  city?: string;
  region?: string;
  country?: string;
  country_code?: string;
  user_agent: string;
  last_seen_at: string;
  current: boolean;
};

// parseUserAgent derives a friendly browser + OS + mobile flag from a raw UA.
function parseUserAgent(ua: string): { browser: string; os: string; mobile: boolean } {
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\/|Opera/.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : "Browser";
  const os = /Windows NT/.test(ua)
    ? "Windows"
    : /iPhone|iPad|iPod/.test(ua)
      ? "iOS"
      : /Mac OS X|Macintosh/.test(ua)
        ? "macOS"
        : /Android/.test(ua)
          ? "Android"
          : /Linux/.test(ua)
            ? "Linux"
            : "Unknown";
  return { browser, os, mobile: /Mobi|Android|iPhone|iPad|iPod/.test(ua) };
}

// composeLocation builds "City, Region, CC" from the resolved geo fields.
function composeLocation(s: Pick<SessionRow, "city" | "region" | "country" | "country_code">): string {
  const parts = [s.city, s.region, s.country_code || s.country].filter(Boolean);
  return parts.length ? parts.join(", ") : "Unknown";
}

// relativeTime renders a session's last-active timestamp as a short relative string.
function relativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const m = Math.floor((Date.now() - t) / 60000);
  if (m < 1) return "Active now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d === 1) return "Yesterday";
  if (d < 7) return `${d}d ago`;
  return new Date(t).toLocaleDateString();
}

// ----- reusable pieces ------------------------------------------------------

// SButton is the single button style for the whole Settings area: compact
// (h-8, matches the inputs), white background, a strong 1px outline, and a
// soft shadow so it reads clearly on the white cards. Stays full-opacity
// even when disabled (this is a design preview) so nothing looks faded.
// `danger` swaps to a red-tinted variant of the same shape; `to` renders a
// Link instead of a button.
function SButton({
  children,
  to,
  danger,
  disabled,
  title,
  onClick,
  className,
}: {
  children: React.ReactNode;
  to?: string;
  danger?: boolean;
  disabled?: boolean;
  title?: string;
  onClick?: () => void;
  className?: string;
}) {
  const cls = cn(
    // Compact (h-7) with a standard min width so every button is the same
    // length; long-label buttons grow past the floor rather than truncate.
    "inline-flex h-7 min-w-[7.5rem] items-center justify-center gap-1.5 rounded-md border px-3 text-[11px] font-medium shadow-sm transition-colors disabled:cursor-not-allowed",
    danger
      ? "border-destructive/40 bg-background text-destructive hover:bg-destructive/10"
      : "border-foreground/30 bg-background text-foreground hover:bg-muted",
    className,
  );
  if (to) {
    return (
      <Link to={to} className={cls}>
        {children}
      </Link>
    );
  }
  return (
    <button type="button" disabled={disabled} title={title} onClick={onClick} className={cls}>
      {children}
    </button>
  );
}

function Section({
  title,
  description,
  danger,
  children,
}: {
  title?: string;
  description?: string;
  danger?: boolean;
  children: React.ReactNode;
}) {
  return (
    <section
      className={cn(
        "rounded-lg border bg-card p-5 shadow-sm",
        danger ? "border-destructive/30" : "border-border",
      )}
    >
      {(title || description) && (
        <div className="mb-4">
          {title && (
            <h3
              className={cn(
                "text-sm font-semibold",
                danger ? "text-destructive" : "text-foreground",
              )}
            >
              {title}
            </h3>
          )}
          {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
        </div>
      )}
      <div className="space-y-4">{children}</div>
    </section>
  );
}

function Field({
  label,
  htmlFor,
  description,
  className,
  children,
}: {
  label: string;
  htmlFor?: string;
  description?: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={htmlFor} className="text-xs font-medium text-foreground">
        {label}
      </Label>
      {children}
      {description && <p className="text-[11px] text-muted-foreground">{description}</p>}
    </div>
  );
}

function InfoRow({
  label,
  value,
  mono,
  trailing,
}: {
  label: string;
  value: string;
  mono?: boolean;
  trailing?: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3 border-t border-border pt-3 first:border-0 first:pt-0">
      <span className="text-xs font-medium text-foreground">{label}</span>
      <div className="flex items-center gap-2">
        <span className={cn("text-xs text-muted-foreground", mono && "font-mono tabular-nums")}>
          {value}
        </span>
        {trailing}
      </div>
    </div>
  );
}

// ToggleRow works in two modes: uncontrolled (pass defaultOn — local state) or
// controlled (pass checked + onChange). `disabled` greys it out and blocks
// toggling; `soon` is the "not built yet" disabled variant with a pill.
function ToggleRow({
  label,
  description,
  defaultOn,
  soon,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  description?: string;
  defaultOn?: boolean;
  soon?: boolean;
  checked?: boolean;
  onChange?: (next: boolean) => void;
  disabled?: boolean;
}) {
  const [internalOn, setInternalOn] = useState(!!defaultOn);
  const isControlled = checked !== undefined;
  const on = isControlled ? !!checked : internalOn;
  const locked = !!soon || !!disabled;

  const toggle = () => {
    if (locked) return;
    if (isControlled) onChange?.(!on);
    else setInternalOn((v) => !v);
  };

  return (
    <div className="flex items-center justify-between gap-3">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-sm text-foreground">{label}</span>
          {soon && <SoonPill />}
        </div>
        {description && <p className="text-[11px] text-muted-foreground">{description}</p>}
      </div>
      <button
        type="button"
        disabled={locked}
        onClick={toggle}
        className={cn(
          "relative inline-flex h-[17px] w-[31px] flex-shrink-0 items-center rounded-full transition-colors disabled:opacity-50",
          on && !soon ? "bg-primary" : "bg-muted",
        )}
        aria-pressed={on}
      >
        <span
          className={cn(
            "inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform",
            on && !soon ? "translate-x-[15px]" : "translate-x-0.5",
          )}
        />
      </button>
    </div>
  );
}

// Brand logos (Simple Icons paths) for the authenticator apps we support, shown
// on the Authenticator card so users recognize the compatible apps at a glance.
function GoogleAuthenticatorLogo({ className }: { className?: string }) {
  return <img src={googleAuthIcon} alt="Google Authenticator" className={className} />;
}
function AuthyLogo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" role="img" aria-label="Authy" fill="#EC1C24" className={className}>
      <path d="M12 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0zm3.42 5.338c.274 0 .551.105.769.315l2.862 2.862c2.054 2.039 2.084 5.35.105 7.449a.21.21 0 0 1-.045.06l-.03.03-.03.03c-.015.015-.045.03-.06.045-2.098 1.978-5.41 1.948-7.463-.105l-2.863-2.863a1.05 1.05 0 0 1 0-1.499 1.05 1.05 0 0 1 1.5 0l2.861 2.863a3.23 3.23 0 0 0 4.542.03 3.244 3.244 0 0 0-.03-4.541l-2.863-2.862a1.05 1.05 0 0 1 0-1.5c.203-.209.472-.314.746-.314zM8.758 6.397a5.33 5.33 0 0 1 3.715 1.564l2.863 2.862c.42.42.42 1.08 0 1.5-.42.419-1.08.419-1.5 0L10.975 9.46a3.249 3.249 0 0 0-4.558-.015 3.243 3.243 0 0 0 .03 4.54l2.863 2.863c.42.42.42 1.08 0 1.499a1.05 1.05 0 0 1-1.499 0L4.95 15.484c-2.054-2.053-2.084-5.365-.105-7.463.015-.03.03-.045.045-.06l.03-.03.03-.03c.015-.015.045-.03.06-.045a5.355 5.355 0 0 1 3.748-1.46z" />
    </svg>
  );
}
// Passkey glyph — the standard passkey icon (Google Material Symbols, Apache-2.0),
// outline variant, in emerald on a white tile to match the authenticator tiles.
function PasskeyGlyph() {
  return (
    <div className="flex h-16 w-16 items-center justify-center rounded-xl border border-border bg-white shadow-sm">
      <svg viewBox="0 0 24 24" role="img" aria-label="Passkey" fill="currentColor" className="h-9 w-9 text-emerald-600">
        <path d="M11 18Zm-8 2v-2.8q0-.85.438-1.563T4.6 14.55q1.55-.775 3.15-1.163T11 13q.5 0 1 .038t1 .112q-.05.525.025 1.05t.25 1.025q-.575-.125-1.137-.175T11 15q-1.4 0-2.775.338T5.5 16.35q-.225.125-.363.35T5 17.2v.8h10.5v2H3Zm16 3l-1.5-1.5v-4.65q-1.1-.325-1.8-1.238T15 13.5q0-1.45 1.025-2.475T18.5 10q1.45 0 2.475 1.025T22 13.5q0 1.125-.638 2t-1.612 1.25L21 18l-1.5 1.5L21 21l-2 2Zm-.5-9q.425 0 .713-.288T19.5 13q0-.425-.288-.713T18.5 12q-.425 0-.713.288T17.5 13q0 .425.288.713T18.5 14ZM11 12q-1.65 0-2.825-1.175T7 8q0-1.65 1.175-2.825T11 4q1.65 0 2.825 1.175T15 8q0 1.65-1.175 2.825T11 12Zm0-2q.825 0 1.413-.588T13 8q0-.825-.588-1.413T11 6q-.825 0-1.413.588T9 8q0 .825.588 1.413T11 10Zm0-2Z" />
      </svg>
    </div>
  );
}

// MethodCard is one 2FA method as a standalone card: brand glyph, title (+
// optional Recommended pill), rationale, and a full-width set-up action.
function MethodCard({
  brand,
  title,
  description,
  action,
  ribbon,
  enabled,
  showInactive,
  alwaysAction,
  actionDisabled,
  onAction,
  onRemove,
  removing,
}: {
  brand: ReactNode;
  title: string;
  description: string;
  action: string;
  ribbon?: string; // tab copy (e.g. "Highly recommended"); separate from status
  enabled?: boolean; // method is active (shows "Active" + Remove)
  showInactive?: boolean; // when off, show a red "Inactive" pill (else no pill)
  alwaysAction?: boolean; // keep the action button even when enabled (e.g. "Add another" passkey)
  actionDisabled?: boolean; // disable the action button (unsupported/busy)
  onAction?: () => void; // wired action; when absent the button is an inert mock
  onRemove?: () => void;
  removing?: boolean;
}) {
  return (
    <div className="relative flex flex-col rounded-xl border border-border bg-card p-4 transition-colors hover:border-foreground/20">
      {/* A small tab that pokes up above the card's top-left edge — a subtle
          endorsement shown only on the recommended method. The grid's top
          margin reserves the headroom, so both cards stay level. */}
      {ribbon && (
        <span className="absolute -top-[16px] left-4 inline-flex items-center rounded-t-md border border-b-0 border-emerald-600/25 bg-emerald-50 px-2 pb-[3px] pt-0.5 text-[9px] font-semibold uppercase leading-none tracking-wide text-emerald-700">
          {ribbon}
        </span>
      )}
      <div className="flex items-start justify-between gap-2">
        {brand}
        {enabled ? (
          <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700">
            Active
          </span>
        ) : showInactive ? (
          <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-red-600">
            Inactive
          </span>
        ) : null}
      </div>
      <h4 className="mt-3 text-sm font-semibold text-foreground">{title}</h4>
      <p className="mt-1 flex-1 text-[11px] leading-relaxed text-muted-foreground">{description}</p>
      {enabled && !alwaysAction ? (
        <SButton danger disabled={removing} onClick={onRemove} className="mt-3 w-full">
          {removing ? "Removing…" : "Remove"}
        </SButton>
      ) : onAction ? (
        <SButton onClick={onAction} disabled={actionDisabled} className="mt-3 w-full">
          {action}
        </SButton>
      ) : (
        <SButton disabled title="Lands later" className="mt-3 w-full">
          {action}
        </SButton>
      )}
    </div>
  );
}

function DangerRow({
  label,
  description,
  action,
  destructive,
  onClick,
}: {
  label: string;
  description: string;
  action: string;
  destructive?: boolean;
  onClick?: () => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3 border-t border-destructive/20 pt-4 first:border-0 first:pt-0">
      <div className="min-w-0">
        <p className="text-sm font-medium text-foreground">{label}</p>
        <p className="text-[11px] text-muted-foreground">{description}</p>
      </div>
      <SButton
        danger={destructive}
        disabled={!onClick}
        title={onClick ? undefined : "Lands later"}
        onClick={onClick}
        className="flex-shrink-0"
      >
        {action}
      </SButton>
    </div>
  );
}

function SoonPill({ label = "Soon" }: { label?: string }) {
  return (
    <span className="rounded bg-secondary px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground">
      {label}
    </span>
  );
}

// ----- helpers --------------------------------------------------------------

function cap(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

function fmtDate(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" });
}


// ===== Billing tab ==========================================================

function fmtMoneyCents(cents: number, currency: string): string {
  const amt = cents / 100;
  return currency && currency.toLowerCase() !== "usd"
    ? `${amt.toFixed(2)} ${currency.toUpperCase()}`
    : `$${amt.toFixed(2)}`;
}

const CARD_BRAND_LABEL: Record<string, string> = {
  visa: "Visa",
  mastercard: "Mastercard",
  amex: "American Express",
  discover: "Discover",
  diners: "Diners Club",
  jcb: "JCB",
  unionpay: "UnionPay",
};

function brandLabel(b: string): string {
  return CARD_BRAND_LABEL[b?.toLowerCase()] ?? (b ? b[0].toUpperCase() + b.slice(1) : "Card");
}

function planTitle(code: string): string {
  return code ? code[0].toUpperCase() + code.slice(1) : "Plan";
}

const HISTORY_PAGE_SIZE = 7;

type HistSortKey = "date" | "order" | "plan" | "amount";

// Sortable column header for the billing-history table — mirrors the SortHeader
// used in the Trash/Users/Recent tables (white header, chevron affordance).
function HistSortHeader({
  label,
  col,
  sortBy,
  sortDir,
  onSort,
  align,
}: {
  label: string;
  col: HistSortKey;
  sortBy: HistSortKey;
  sortDir: "asc" | "desc";
  onSort: (k: HistSortKey) => void;
  align?: "right";
}) {
  const active = sortBy === col;
  return (
    <th className={cn("px-4 py-2.5", align === "right" ? "text-right" : "text-left")}>
      <button
        type="button"
        onClick={() => onSort(col)}
        aria-sort={active ? (sortDir === "asc" ? "ascending" : "descending") : "none"}
        className={cn(
          "inline-flex items-center gap-1 transition-colors hover:text-foreground",
          align === "right" && "flex-row-reverse",
        )}
      >
        {label}
        {active ? (
          sortDir === "asc" ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-40" />
        )}
      </button>
    </th>
  );
}

function BillingTab() {
  const [data, setData] = useState<BillingOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyPM, setBusyPM] = useState<string | null>(null);
  const [cardError, setCardError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [query, setQuery] = useState("");
  const [histPage, setHistPage] = useState(1);
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<HistSortKey>("date");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");

  const onHistSort = (col: HistSortKey) => {
    setHistPage(1);
    if (sortBy === col) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(col);
      setSortDir(col === "date" || col === "amount" ? "desc" : "asc");
    }
  };

  const reload = () =>
    api
      .get<BillingOverview>("/api/billing/overview")
      .then(setData)
      .catch(() => setData({ enabled: false, auto_renew: true, cards: [], transactions: [] }));

  useEffect(() => {
    let cancelled = false;
    api
      .get<BillingOverview>("/api/billing/overview")
      .then((r) => {
        if (!cancelled) setData(r);
      })
      .catch(() => {
        if (!cancelled) setData({ enabled: false, auto_renew: true, cards: [], transactions: [] });
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const setAutoRenew = async (next: boolean) => {
    setData((d) => (d ? { ...d, auto_renew: next } : d)); // optimistic
    try {
      await api.put("/api/billing/auto-renew", { enabled: next });
    } catch {
      setData((d) => (d ? { ...d, auto_renew: !next } : d)); // revert
    }
  };

  const makeDefault = async (pmId: string) => {
    setBusyPM(pmId);
    try {
      await api.post("/api/billing/payment-methods/default", { payment_method_id: pmId });
      await reload();
    } finally {
      setBusyPM(null);
    }
  };

  const removeCard = async (pmId: string) => {
    setBusyPM(pmId);
    setCardError(null);
    try {
      await api.post("/api/billing/payment-methods/remove", { payment_method_id: pmId });
      await reload();
    } catch (e) {
      setCardError(e instanceof ApiError && e.message ? e.message : "Couldn't remove that card.");
    } finally {
      setBusyPM(null);
    }
  };

  const cancelScheduled = async () => {
    try {
      await api.post("/api/billing/cancel-scheduled-change", {});
      await reload();
    } catch {
      /* ignore */
    }
  };

  const retryInvoice = async (invoiceID: string) => {
    setRetrying(true);
    setRetryError(null);
    try {
      await api.post("/api/billing/retry-invoice", { invoice_id: invoiceID });
      await reload();
    } catch (e) {
      setRetryError(e instanceof ApiError && e.message ? e.message : "Payment couldn't be processed.");
    } finally {
      setRetrying(false);
    }
  };

  if (loading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-40 w-full rounded-lg" />
        <Skeleton className="h-20 w-full rounded-lg" />
        <Skeleton className="h-56 w-full rounded-lg" />
      </div>
    );
  }

  if (!data?.enabled) {
    return (
      <Section title="Billing" description="Payment methods, auto-renew, and receipts.">
        <p className="text-[13px] text-muted-foreground">Billing isn't configured yet.</p>
      </Section>
    );
  }

  // Client-side search across every visible attribute, then paginate.
  const q = query.trim().toLowerCase();
  const filtered = q
    ? data.transactions.filter((t) =>
        [
          t.order_number,
          planTitle(t.plan_code),
          t.billing_period,
          fmtMoneyCents(t.amount_cents, t.currency),
          fmtDate(t.created_at),
          t.receipt_number,
        ]
          .join(" ")
          .toLowerCase()
          .includes(q),
      )
    : data.transactions;
  const sorted = [...filtered].sort((a, b) => {
    let base = 0;
    switch (sortBy) {
      case "date":
        base = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
        break;
      case "order":
        base = a.order_number.localeCompare(b.order_number);
        break;
      case "plan":
        base = planTitle(a.plan_code).localeCompare(planTitle(b.plan_code));
        break;
      case "amount":
        base = a.amount_cents - b.amount_cents;
        break;
    }
    return sortDir === "asc" ? base : -base;
  });
  const totalPages = Math.max(1, Math.ceil(sorted.length / HISTORY_PAGE_SIZE));
  const page = Math.min(histPage, totalPages);
  const paged = sorted.slice((page - 1) * HISTORY_PAGE_SIZE, page * HISTORY_PAGE_SIZE);

  const sub = data.subscription;

  return (
    <div className="space-y-4">
      {/* Past-due / failed-payment banner */}
      {sub?.past_due && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0 text-amber-600" />
            <div className="min-w-0 flex-1">
              <p className="text-[13px] font-semibold text-foreground">Your last payment didn't go through</p>
              <p className="mt-0.5 text-[12px] leading-relaxed text-muted-foreground">
                Update your card or retry the charge to keep your plan. We'll keep retrying automatically in the meantime.
              </p>
              {retryError && <p className="mt-1.5 text-[12px] text-destructive">{retryError}</p>}
              <div className="mt-3 flex flex-wrap items-center gap-2">
                {data.open_invoice && (
                  <button
                    type="button"
                    disabled={retrying}
                    onClick={() => retryInvoice(data.open_invoice!.id)}
                    className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-60"
                  >
                    {retrying ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
                    Retry payment
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => setAdding(true)}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md border border-foreground/25 bg-background px-3 text-[12px] font-medium text-foreground transition-colors hover:bg-muted"
                >
                  <Plus className="h-3.5 w-3.5" />
                  Update card
                </button>
                {data.open_invoice?.hosted_invoice_url && (
                  <a
                    href={data.open_invoice.hosted_invoice_url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-[12px] font-medium text-primary hover:underline"
                  >
                    View invoice
                    <ExternalLink className="h-3 w-3" />
                  </a>
                )}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Over-limit (downgrade grace) banner. Usage exceeds the current plan's
          caps — uploads/invites are blocked until they free up or upgrade.
          Never shown while lapsed (the global read-only banner covers that). */}
      {sub?.over_quota && sub.lifecycle_state !== "lapsed" && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0 text-amber-600" />
            <div className="min-w-0 flex-1">
              <p className="text-[13px] font-semibold text-foreground">You're over your plan's limits</p>
              <p className="mt-0.5 text-[12px] leading-relaxed text-muted-foreground">
                {sub.storage_over && sub.seats_over
                  ? "Your storage and member count both exceed this plan."
                  : sub.storage_over
                    ? "Your storage exceeds this plan's allowance."
                    : "Your member count exceeds this plan's seats."}{" "}
                Your data is safe and stays accessible, but you can't{" "}
                {sub.storage_over ? "upload new files" : ""}
                {sub.storage_over && sub.seats_over ? " or " : ""}
                {sub.seats_over ? "invite more people" : ""} until you free up space or upgrade.
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Payment methods */}
      <Section title="Payment method" description="Cards on file for your account.">
        {data.cards.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">No card on file yet.</p>
        ) : (
          <ul className="space-y-2.5">
            {data.cards.map((c) => {
              // The default card can't be removed while others exist; the only
              // card can't be removed at all. Promote another to default first.
              const canDelete = data.cards.length > 1 && !c.default;
              const blockReason =
                data.cards.length <= 1
                  ? "You need at least one card on file"
                  : c.default
                    ? "Make another card your default before removing this one"
                    : undefined;
              return (
                <li
                  key={c.id}
                  className="flex items-center gap-3 rounded-lg border border-border bg-card px-3.5 py-3"
                >
                  <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-secondary text-muted-foreground">
                    <CreditCard className="h-[18px] w-[18px]" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-medium text-foreground">
                      {brandLabel(c.brand)} •••• {c.last4}
                      {c.default && (
                        <span className="ml-2 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-emerald-700">
                          Default
                        </span>
                      )}
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      Expires {String(c.exp_month).padStart(2, "0")}/{String(c.exp_year).slice(-2)}
                    </p>
                  </div>
                  {!c.default && (
                    <button
                      type="button"
                      disabled={busyPM === c.id}
                      onClick={() => makeDefault(c.id)}
                      className="text-[12px] font-medium text-primary hover:underline disabled:opacity-50"
                    >
                      Make default
                    </button>
                  )}
                  <button
                    type="button"
                    disabled={!canDelete || busyPM === c.id}
                    onClick={() => removeCard(c.id)}
                    aria-label="Remove card"
                    title={blockReason}
                    className="inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-destructive disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-muted-foreground"
                  >
                    {busyPM === c.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        {cardError && <p className="mt-2 text-[12px] text-destructive">{cardError}</p>}
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="mt-3 inline-flex h-8 items-center gap-1.5 rounded-md border border-foreground/25 bg-background px-3 text-[12px] font-medium text-foreground transition-colors hover:bg-muted"
        >
          <Plus className="h-3.5 w-3.5" />
          Add payment method
        </button>
      </Section>

      {/* Auto-renew */}
      <Section title="Renewal">
        {(() => {
          const subActive = !!sub && (sub.status === "active" || sub.status === "trialing");
          // Edge case: an active subscription with no card on file can't renew —
          // force auto-renew off and dim it until a card is added.
          const noCardLock = subActive && (data.cards?.length ?? 0) === 0;
          const scheduledLock = !!(sub?.scheduled_plan && sub.scheduled_change_at);
          const autoRenewHint = noCardLock
            ? "Add a card on file to turn on auto-renew — there's no card to charge at renewal."
            : `Locked while your switch to ${sub?.scheduled_plan_name ?? "another plan"} is scheduled. Cancel it above to change auto-renew.`;
          return (
            <>
              {subActive && sub && sub.current_period_end && (
                <p className="mb-3 text-[12.5px] text-muted-foreground">
                  {noCardLock ? (
                    <span className="text-amber-700">
                      You don't have a card on file, so your plan won't auto-renew. Add a card to keep it active past{" "}
                      <span className="font-medium">{fmtDate(sub.current_period_end)}</span>.
                    </span>
                  ) : sub.auto_renew ? (
                    <>Your plan renews on <span className="font-medium text-foreground">{fmtDate(sub.current_period_end)}</span>.</>
                  ) : (
                    <span className="text-amber-700">
                      Your plan ends on <span className="font-medium">{fmtDate(sub.current_period_end)}</span> — turn auto-renew back on to keep it.
                    </span>
                  )}
                </p>
              )}
              {sub?.scheduled_plan && sub.scheduled_change_at && (
                <div className="mb-3 flex items-center gap-1.5 rounded-md bg-muted/50 px-3 py-2 text-[12px] text-foreground">
                  <CalendarClock className="h-3.5 w-3.5 flex-shrink-0 text-primary" />
                  <span className="flex-1">
                    Scheduled to switch to <span className="font-medium">{sub.scheduled_plan_name}</span> on {fmtDate(sub.scheduled_change_at)}.
                  </span>
                  <button type="button" onClick={() => void cancelScheduled()} className="font-medium text-primary hover:underline">
                    Cancel
                  </button>
                </div>
              )}
              <NotifRow
                icon={RefreshCw}
                title="Auto-renew"
                description="Automatically renew your plan at the end of each term. Turn off to let it lapse."
                checked={noCardLock ? false : data.auto_renew}
                onChange={(next) => void setAutoRenew(next)}
                disabled={scheduledLock || noCardLock}
                disabledHint={autoRenewHint}
              />
            </>
          );
        })()}
      </Section>

      {/* Billing history */}
      <Section title="Billing history" description="Your receipts and past charges.">
        {data.transactions.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">No charges yet.</p>
        ) : (
          <>
            {/* Quick search across order #, plan, amount, date */}
            <div className="relative mb-3 max-w-xs">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setHistPage(1);
                }}
                placeholder="Search receipts…"
                className="h-8 w-full rounded-md border border-foreground/25 bg-background pl-8 pr-3 text-[12px] text-foreground outline-none placeholder:text-muted-foreground/60 focus-visible:ring-1 focus-visible:ring-primary/40"
              />
            </div>

            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse text-left">
                <thead className="bg-card">
                  <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                    <HistSortHeader label="Date" col="date" sortBy={sortBy} sortDir={sortDir} onSort={onHistSort} />
                    <HistSortHeader label="Order" col="order" sortBy={sortBy} sortDir={sortDir} onSort={onHistSort} />
                    <HistSortHeader label="Plan" col="plan" sortBy={sortBy} sortDir={sortDir} onSort={onHistSort} />
                    <HistSortHeader label="Amount" col="amount" sortBy={sortBy} sortDir={sortDir} onSort={onHistSort} align="right" />
                    <th className="px-4 py-2.5 text-right">Receipt</th>
                  </tr>
                </thead>
                <tbody>
                  {paged.length === 0 ? (
                    <tr>
                      <td colSpan={5} className="px-4 py-6 text-center text-[12px] text-muted-foreground">
                        No receipts match "{query}".
                      </td>
                    </tr>
                  ) : (
                    paged.map((t) => (
                      <tr key={t.order_number} className="border-b border-border last:border-0 text-[12.5px] text-foreground transition-colors hover:bg-muted/40">
                        <td className="whitespace-nowrap px-4 py-2.5 text-muted-foreground">{fmtDate(t.created_at)}</td>
                        <td className="whitespace-nowrap px-4 py-2.5 font-medium">{t.order_number}</td>
                        <td className="whitespace-nowrap px-4 py-2.5">
                          {planTitle(t.plan_code)}
                          <span className="ml-1 text-[11px] text-muted-foreground">({t.billing_period})</span>
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums">
                          {fmtMoneyCents(t.amount_cents, t.currency)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right">
                          {t.receipt_url ? (
                            <a
                              href={t.receipt_url}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex items-center gap-1 font-medium text-primary hover:underline"
                            >
                              View
                              <ExternalLink className="h-3 w-3" />
                            </a>
                          ) : (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>

            {/* Pagination — only once there's more than one page (>7 results) */}
            {totalPages > 1 && (
              <div className="mt-3 flex items-center justify-between text-[12px] text-muted-foreground">
                <span>
                  Page {page} of {totalPages} · {filtered.length} receipt{filtered.length === 1 ? "" : "s"}
                </span>
                <div className="flex items-center gap-1.5">
                  <button
                    type="button"
                    disabled={page <= 1}
                    onClick={() => setHistPage((p) => Math.max(1, p - 1))}
                    className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2 font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <ChevronLeft className="h-3.5 w-3.5" />
                    Prev
                  </button>
                  <button
                    type="button"
                    disabled={page >= totalPages}
                    onClick={() => setHistPage((p) => Math.min(totalPages, p + 1))}
                    className="inline-flex h-7 items-center gap-1 rounded-md border border-border px-2 font-medium text-foreground transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    Next
                    <ChevronRight className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </Section>

      {adding && (
        <AddCardModal
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            void reload();
          }}
        />
      )}
    </div>
  );
}

// AddCardModal opens a Stripe Elements form backed by a SetupIntent so the user
// can save a new card without a charge.
function AddCardModal({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [pubKey, setPubKey] = useState<string | null>(null);
  const [clientSecret, setClientSecret] = useState<string | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .post<{ client_secret: string; publishable_key: string }>("/api/billing/setup-intent", {})
      .then((r) => {
        if (!cancelled) {
          setPubKey(r.publishable_key);
          setClientSecret(r.client_secret);
        }
      })
      .catch(() => {
        if (!cancelled) setLoadErr("Couldn't start. Please try again.");
      });
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      cancelled = true;
      window.removeEventListener("keydown", onKey);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const stripePromise = useMemo(() => (pubKey ? loadStripe(pubKey) : null), [pubKey]);

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="flex w-full max-w-md flex-col overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 className="text-sm font-semibold text-foreground">Add payment method</h2>
          <button type="button" onClick={onClose} className="text-muted-foreground hover:text-foreground">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="px-5 py-5">
          {loadErr ? (
            <p className="text-[13px] text-destructive">{loadErr}</p>
          ) : !stripePromise || !clientSecret ? (
            <div className="flex justify-center py-10 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
            </div>
          ) : (
            <Elements stripe={stripePromise} options={{ clientSecret, appearance: STRIPE_APPEARANCE }}>
              <AddCardForm onCancel={onClose} onDone={onAdded} />
            </Elements>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

function AddCardForm({ onCancel, onDone }: { onCancel: () => void; onDone: () => void }) {
  const stripe = useStripe();
  const elements = useElements();
  const [saving, setSaving] = useState(false);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async () => {
    if (!stripe || !elements) return;
    setSaving(true);
    setError(null);
    const { error: err } = await stripe.confirmSetup({
      elements,
      confirmParams: { return_url: window.location.href },
      redirect: "if_required",
    });
    if (err) {
      setError(err.message ?? "Couldn't save the card.");
      setSaving(false);
      return;
    }
    onDone();
  };

  return (
    <div className="space-y-4">
      <PaymentElement options={{ layout: { type: "tabs" } }} onReady={() => setReady(true)} />
      {error && <p className="text-[12px] text-destructive">{error}</p>}
      <div className="flex items-center justify-between gap-2 pt-[15px]">
        <span className="inline-flex items-baseline gap-1">
          <Lock className="h-3 w-3 translate-y-0.5 text-muted-foreground" />
          <span className="text-[10px] text-muted-foreground">Powered by</span>
          <span className="text-[14px] font-bold lowercase tracking-tight text-primary">stripe</span>
        </span>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={onCancel}
            disabled={saving}
            className="inline-flex h-8 items-center rounded-md border border-foreground/25 bg-background px-3 text-[12px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
        <button
          type="button"
          onClick={save}
          disabled={saving || !ready}
          className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-4 text-[12px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
          Save card
        </button>
        </div>
      </div>
    </div>
  );
}
