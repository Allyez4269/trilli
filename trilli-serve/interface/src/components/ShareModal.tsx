import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Globe, Link2, Loader2, Mail, User as UserIcon, X } from "lucide-react";

import { api, ApiError, type Share, type ShareRecipient } from "@/lib/api";
import { checkboxCls, cn } from "@/lib/utils";

export interface ShareTarget {
  kind: "file" | "folder";
  id: number;
  name: string;
}

type Mode = "public" | "member" | "email";

// ShareModal shares a file or folder three ways: a public "anyone with the
// link" URL, a specific account member, or an external email (magic link).
export default function ShareModal({
  target,
  onClose,
  onCreated,
}: {
  target: ShareTarget;
  onClose: () => void;
  onCreated?: (s: Share) => void;
}) {
  const [mode, setMode] = useState<Mode>("public");
  const [expiry, setExpiry] = useState("0");
  const [password, setPassword] = useState("");
  const [email, setEmail] = useState("");
  const [memberId, setMemberId] = useState<number | "">("");
  const [notify, setNotify] = useState(true);
  const [recipients, setRecipients] = useState<ShareRecipient[] | null>(null);

  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [share, setShare] = useState<Share | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Lazy-load members the first time the "Specific person" tab is opened.
  useEffect(() => {
    if (mode === "member" && recipients === null) {
      api
        .get<{ recipients: ShareRecipient[] }>("/api/shares/recipients")
        .then((r) => setRecipients(r.recipients ?? []))
        .catch(() => setRecipients([]));
    }
  }, [mode, recipients]);

  const fullUrl = share ? `${window.location.origin}${share.url}` : "";

  const create = async () => {
    setCreating(true);
    setError(null);
    try {
      const days = parseInt(expiry, 10);
      const payload: Record<string, unknown> = {
        resource_type: target.kind,
        [target.kind === "file" ? "file_id" : "folder_id"]: target.id,
        grantee_type: mode,
        // Shares are always download-capable; the view-only option was removed.
        capability: "download",
        expires_in_days: days > 0 ? days : null,
        password: password.trim() || undefined,
      };
      if (mode === "member") {
        if (!memberId) {
          setError("Pick a person to share with.");
          setCreating(false);
          return;
        }
        payload.grantee_user_id = memberId;
        payload.notify = notify;
      } else if (mode === "email") {
        if (!email.trim()) {
          setError("Enter an email address.");
          setCreating(false);
          return;
        }
        payload.grantee_email = email.trim();
      }
      const s = await api.post<Share>("/api/shares", payload);
      setShare(s);
      onCreated?.(s);
    } catch (e) {
      setError(e instanceof ApiError && e.message ? e.message : "Couldn't create the share.");
    } finally {
      setCreating(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(fullUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* clipboard blocked */
    }
  };

  const sentTo =
    mode === "email"
      ? email.trim()
      : mode === "member"
        ? recipients?.find((r) => r.user_id === memberId)?.name ?? "the member"
        : "";

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <div className="flex min-w-0 items-center gap-2">
            <Link2 className="h-4 w-4 flex-shrink-0 text-primary" />
            <h2 className="truncate text-sm font-semibold text-foreground">Share “{target.name}”</h2>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </div>

        {!share ? (
          <div className="space-y-4 px-5 py-4">
            {/* Recipient mode */}
            <div className="grid grid-cols-3 gap-1.5">
              <ModeBtn active={mode === "public"} onClick={() => setMode("public")} icon={Globe} label="Anyone w/ link" />
              <ModeBtn active={mode === "member"} onClick={() => setMode("member")} icon={UserIcon} label="A member" />
              <ModeBtn active={mode === "email"} onClick={() => setMode("email")} icon={Mail} label="By email" />
            </div>

            {mode === "member" && (
              <Field label="Member">
                <select
                  value={memberId}
                  onChange={(e) => setMemberId(e.target.value ? Number(e.target.value) : "")}
                  className="h-9 w-full rounded-md border border-border bg-background px-2 text-[13px]"
                >
                  <option value="">{recipients === null ? "Loading…" : "Select a member…"}</option>
                  {(recipients ?? []).map((r) => (
                    <option key={r.user_id} value={r.user_id}>
                      {r.name} ({r.email})
                    </option>
                  ))}
                </select>
                <label className="mt-2 flex items-center gap-2 text-[12px] text-muted-foreground">
                  <input type="checkbox" checked={notify} onChange={(e) => setNotify(e.target.checked)} className={checkboxCls} />
                  Email them the link
                </label>
              </Field>
            )}

            {mode === "email" && (
              <Field label="Recipient email">
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="name@example.com"
                  className="h-9 w-full rounded-md border border-border bg-background px-2.5 text-[13px]"
                />
                <p className="mt-1 text-[11px] text-muted-foreground">We’ll email them a secure link.</p>
              </Field>
            )}

            <div className="grid grid-cols-2 gap-3">
              <Field label="Link expires">
                <select
                  value={expiry}
                  onChange={(e) => setExpiry(e.target.value)}
                  className="h-9 w-full rounded-md border border-border bg-background px-2 text-[13px]"
                >
                  <option value="0">No expiry</option>
                  <option value="1">1 day</option>
                  <option value="7">7 days</option>
                  <option value="30">30 days</option>
                  <option value="90">90 days</option>
                </select>
              </Field>
              <Field label="Password (optional)">
                <input
                  type="text"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="None"
                  className="h-9 w-full rounded-md border border-border bg-background px-2.5 text-[13px]"
                />
              </Field>
            </div>

            {error && <p className="text-[12px] text-destructive">{error}</p>}

            <div className="flex justify-end gap-2 pt-1">
              <button onClick={onClose} className="h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground hover:bg-muted">
                Cancel
              </button>
              <button
                onClick={create}
                disabled={creating}
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
              >
                {creating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Link2 className="h-3.5 w-3.5" />}
                {mode === "public" ? "Create link" : mode === "email" ? "Send link" : "Share"}
              </button>
            </div>
          </div>
        ) : (
          <div className="space-y-3 px-5 py-4">
            <p className="text-[13px] font-medium text-foreground">
              {share.grantee_type === "public"
                ? "Your link is ready"
                : `Shared with ${sentTo}`}
              {share.grantee_type === "email" && " — email sent"}
            </p>
            <div className="flex items-center gap-2">
              <input
                readOnly
                value={fullUrl}
                onFocus={(e) => e.currentTarget.select()}
                className="h-9 flex-1 rounded-md border border-border bg-muted/40 px-2.5 text-[12px] text-foreground"
              />
              <button onClick={copy} className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-semibold text-primary-foreground hover:bg-primary/90">
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? "Copied" : "Copy"}
              </button>
            </div>
            <ul className="space-y-1 text-[12px] text-muted-foreground">
              <li>· {share.expires_at ? `Expires ${new Date(share.expires_at).toLocaleDateString()}` : "No expiry"}</li>
              <li>· {share.has_password ? "Password protected" : "No password"}</li>
            </ul>
            <div className="flex justify-end pt-1">
              <button onClick={onClose} className="h-9 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90">
                Done
              </button>
            </div>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

function ModeBtn({
  active,
  onClick,
  icon: Icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof Globe;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex flex-col items-center gap-1 rounded-lg border px-2 py-2.5 text-[11px] font-medium transition-colors",
        active ? "border-primary bg-primary/5 text-foreground" : "border-border text-muted-foreground hover:bg-muted/40",
      )}
    >
      <Icon className="h-4 w-4" />
      {label}
    </button>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1.5 block text-[12px] font-medium text-foreground">{label}</label>
      {children}
    </div>
  );
}
