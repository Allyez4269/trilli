import { type FormEvent, type ReactNode, useState } from "react";
import { Settings as SettingsIcon, Loader2, Check } from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import StepUpModal from "@/components/StepUpModal";

// Operator self-service settings: profile (name + email) and password. Mirrors
// the app.trilli.com Settings → Account pattern (Section cards + compact fields
// + a right-aligned save bar). No security/notifications/plans/billing here.
const inputCls = "h-7 max-w-md border-foreground/25 bg-background text-xs";
const sBtn =
  "inline-flex h-7 min-w-[7.5rem] items-center justify-center gap-1.5 rounded-md border border-foreground/30 bg-background px-3 text-[11px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50";

export default function Settings() {
  const { operator } = useAuth();
  return (
    <div className="mx-auto max-w-[61.2rem] space-y-5">
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight text-foreground">
          <SettingsIcon className="h-6 w-6 text-primary" /> Settings
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">Your operator profile and password.</p>
      </div>

      <ProfileSection key={operator?.id ?? 0} />
      <PasswordSection />
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-5 shadow-sm">
      <div className="mb-4">
        <h2 className="text-sm font-semibold text-foreground">{title}</h2>
        {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs font-medium text-foreground">{label}</Label>
      {children}
    </div>
  );
}

function ProfileSection() {
  const { operator, refresh } = useAuth();
  const parts = (operator?.name ?? "").trim().split(/\s+/).filter(Boolean);
  const [first, setFirst] = useState(parts[0] ?? "");
  const [last, setLast] = useState(parts.slice(1).join(" "));
  const [email, setEmail] = useState(operator?.email ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    setSaved(false);
    setBusy(true);
    try {
      await api.post("/api/cmx/admin/profile", {
        name: `${first.trim()} ${last.trim()}`.trim(),
        email: email.trim(),
      });
      await refresh();
      setSaved(true);
    } catch (e2) {
      setErr(e2 instanceof ApiError ? e2.message : "Could not save.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Section title="Profile" description="Your name and the email you sign in with.">
      <form onSubmit={save} className="space-y-4">
        <div className="grid max-w-md grid-cols-2 gap-3">
          <Field label="First name">
            <Input value={first} onChange={(e) => setFirst(e.target.value)} className="h-7 border-foreground/25 bg-background text-xs" />
          </Field>
          <Field label="Last name">
            <Input value={last} onChange={(e) => setLast(e.target.value)} className="h-7 border-foreground/25 bg-background text-xs" />
          </Field>
        </div>
        <Field label="Email address">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} className={inputCls} />
        </Field>
        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {err && <span className="mr-auto text-[11px] text-destructive">{err}</span>}
          {saved && !err && (
            <span className="mr-auto inline-flex items-center gap-1 text-[11px] text-emerald-600">
              <Check className="h-3 w-3" /> Saved
            </span>
          )}
          <button type="submit" disabled={busy || !email.trim()} className={sBtn}>
            {busy && <Loader2 className="h-3 w-3 animate-spin" />}
            {busy ? "Saving…" : "Save profile"}
          </button>
        </div>
      </form>
    </Section>
  );
}

function PasswordSection() {
  const { logout } = useAuth();
  const [pw, setPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [stepUp, setStepUp] = useState(false);

  function start(e: FormEvent) {
    e.preventDefault();
    setErr(null);
    if (pw.length < 12) {
      setErr("Use at least 12 characters.");
      return;
    }
    if (pw !== confirm) {
      setErr("Passwords don't match.");
      return;
    }
    setStepUp(true);
  }

  return (
    <Section title="Password" description="Changing your password signs you out of all sessions.">
      <form onSubmit={start} className="space-y-4">
        <div className="grid max-w-md grid-cols-2 gap-3">
          <Field label="New password (12+ chars)">
            <Input type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} className="h-7 border-foreground/25 bg-background text-xs" />
          </Field>
          <Field label="Confirm password">
            <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} className="h-7 border-foreground/25 bg-background text-xs" />
          </Field>
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-border pt-4">
          {err && <span className="mr-auto text-[11px] text-destructive">{err}</span>}
          <button type="submit" disabled={pw.length < 12 || !confirm} className={sBtn}>
            Change password
          </button>
        </div>
      </form>

      {stepUp && (
        <StepUpModal
          title="Change your password?"
          description="You'll be signed out of all sessions and must log in again with the new password."
          confirmLabel="Change password"
          onConfirm={async () => {
            await api.post("/api/cmx/admin/password", { password: pw });
            await logout();
            window.location.href = "/login";
          }}
          onClose={() => setStepUp(false)}
        />
      )}
    </Section>
  );
}
