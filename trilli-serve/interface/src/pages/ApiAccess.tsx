import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";
import {
  Check,
  Code2,
  Copy,
  KeyRound,
  Loader2,
  Plus,
  ShieldAlert,
  Trash2,
  X,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/files-meta";
import { ConfirmDialog } from "@/components/Dialogs";
import { ApiReference } from "@/components/ApiReference";
import { Skeleton } from "@/components/ui/skeleton";

interface ApiKey {
  id: number;
  name: string;
  key_prefix: string;
  last_four: string;
  created_by_name?: string;
  created_at: string;
  last_used_at?: string | null;
}

interface KeysResponse {
  keys: ApiKey[];
  api_access: boolean;
}

export default function ApiAccess() {
  const [keys, setKeys] = useState<ApiKey[] | null>(null);
  // null = still resolving the plan's API entitlement. Start unknown (not true)
  // so accounts without API access never flash the real keys/docs before the
  // upgrade gate renders.
  const [apiAccess, setApiAccess] = useState<boolean | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);

  const load = () => {
    void api
      .get<KeysResponse>("/api/keys")
      .then((r) => {
        setKeys(r.keys ?? []);
        setApiAccess(r.api_access);
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : "Couldn't load API keys."));
  };
  useEffect(load, []);

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-4xl space-y-6 p-6 lg:p-8">
        <header>
          <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-foreground">
            <Code2 className="h-6 w-6 text-primary" />
            API access
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Create keys to use Trilli programmatically. Keys carry full access to this
            account&apos;s files — keep them secret.
          </p>
        </header>

        {error && (
          <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {apiAccess === null && !error ? (
          <ApiAccessSkeleton />
        ) : !apiAccess ? (
          <UpgradeGate />
        ) : (
          <>
            {/* Keys card */}
            <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
              <div className="flex items-center justify-between border-b border-border px-4 py-3">
                <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
                  <KeyRound className="h-4 w-4 text-muted-foreground" /> Your API keys
                </h2>
                <button
                  onClick={() => setCreating(true)}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90"
                >
                  <Plus className="h-3.5 w-3.5" /> Create key
                </button>
              </div>

              {keys === null ? (
                <KeyRowsSkeleton />
              ) : keys.length === 0 ? (
                <div className="px-4 py-12 text-center">
                  <p className="text-sm font-medium text-foreground">No API keys yet</p>
                  <p className="mx-auto mt-1 max-w-sm text-[13px] text-muted-foreground">
                    Create a key to start using the Trilli API. You&apos;ll see the secret once,
                    so copy it somewhere safe.
                  </p>
                </div>
              ) : (
                <table className="w-full text-[13px]">
                  <thead className="bg-card">
                    <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
                      <th className="px-4 py-2.5 font-medium">Name</th>
                      <th className="px-3 py-2.5 font-medium">Key</th>
                      <th className="px-3 py-2.5 font-medium">Created</th>
                      <th className="px-3 py-2.5 font-medium">Last used</th>
                      <th className="px-3 py-2.5 text-right font-medium">Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {keys.map((k) => (
                      <tr key={k.id} className="border-b border-border last:border-0 hover:bg-muted/30">
                        <td className="px-4 py-2.5 font-medium text-foreground">{k.name}</td>
                        <td className="px-3 py-2.5">
                          <code className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[12px] text-muted-foreground">
                            {k.key_prefix}…{k.last_four}
                          </code>
                        </td>
                        <td className="px-3 py-2.5 text-muted-foreground">
                          {formatDateTime(k.created_at)}
                        </td>
                        <td className="px-3 py-2.5 text-muted-foreground">
                          {k.last_used_at ? formatDateTime(k.last_used_at) : "Never"}
                        </td>
                        <td className="px-3 py-2.5">
                          <div className="flex justify-end">
                            <button
                              onClick={() => setRevoking(k)}
                              title="Revoke"
                              className="rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>

            <ApiReference />
          </>
        )}
      </div>

      {creating && (
        <CreateKeyModal
          onClose={() => setCreating(false)}
          onCreated={() => {
            setCreating(false);
            load();
          }}
        />
      )}

      {revoking && (
        <ConfirmDialog
          title="Revoke this key?"
          danger
          confirmLabel="Revoke key"
          message={
            <>
              Any integration using <strong className="text-foreground">{revoking.name}</strong> will
              immediately stop working. This can&apos;t be undone.
            </>
          }
          onConfirm={async () => {
            await api.delete(`/api/keys/${revoking.id}`);
            load();
          }}
          onClose={() => setRevoking(null)}
        />
      )}
    </div>
  );
}

// ApiAccessSkeleton stands in for the keys card while the plan's API
// entitlement resolves, so the page lays out in place instead of flashing a
// spinner. Matches the skeleton style used across the other sections.
function ApiAccessSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <Skeleton className="h-4 w-32" />
        <Skeleton className="h-8 w-24 rounded-md" />
      </div>
      <KeyRowsSkeleton />
    </div>
  );
}

// KeyRowsSkeleton is the placeholder for the API-keys table body.
function KeyRowsSkeleton() {
  return (
    <div className="divide-y divide-border">
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="flex items-center gap-4 px-4 py-3.5">
          <Skeleton className="h-4 w-36" />
          <Skeleton className="hidden h-4 w-28 sm:block" />
          <Skeleton className="ml-auto h-4 w-24" />
          <Skeleton className="h-7 w-7 rounded" />
        </div>
      ))}
    </div>
  );
}

function UpgradeGate() {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card py-16 text-center">
      <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10">
        <ShieldAlert className="h-7 w-7 text-primary" />
      </div>
      <h3 className="mb-1 text-lg font-semibold text-foreground">API access isn&apos;t in your plan</h3>
      <p className="mb-4 max-w-md text-sm text-muted-foreground">
        Programmatic access via API keys is included on the <strong>Plus</strong> and{" "}
        <strong>Infinity</strong> plans. Upgrade to create keys and use the Trilli API.
      </p>
      <Link
        to="/settings/plan"
        className="inline-flex h-9 items-center rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90"
      >
        View plans
      </Link>
    </div>
  );
}

// CreateKeyModal: step 1 collects a name; step 2 reveals the secret exactly once.
function CreateKeyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const create = async () => {
    if (!name.trim()) {
      setError("Please name this key.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<{ token: string }>("/api/keys", { name: name.trim() });
      setToken(res.token);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't create the key.");
      setBusy(false);
    }
  };

  const copy = async () => {
    if (!token) return;
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* clipboard blocked */
    }
  };

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onMouseDown={(e) => e.target === e.currentTarget && (token ? onCreated() : onClose())}
    >
      <div className="w-full max-w-lg overflow-hidden rounded-xl border border-border bg-card shadow-2xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 className="text-sm font-semibold text-foreground">
            {token ? "Copy your API key" : "Create API key"}
          </h2>
          <button
            onClick={token ? onCreated : onClose}
            className="text-muted-foreground hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {!token ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void create();
            }}
          >
            <div className="space-y-1.5 px-5 py-4">
              <label className="block text-[12px] font-medium text-foreground">Key name</label>
              <input
                autoFocus
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (error) setError(null);
                }}
                placeholder="e.g. Production backup script"
                className="h-9 w-full rounded-md border border-border bg-background px-2.5 text-[13px] outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
              />
              <p className="text-[11px] text-muted-foreground">
                A label to help you recognize this key later.
              </p>
              {error && <p className="pt-0.5 text-[12px] text-destructive">{error}</p>}
            </div>
            <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
              <button
                type="button"
                onClick={onClose}
                className="h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground hover:bg-muted"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={busy}
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
              >
                {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                Create key
              </button>
            </div>
          </form>
        ) : (
          <div className="space-y-3 px-5 py-4">
            <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[12px] text-amber-700">
              <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
              <span>
                Copy this key now — for your security, we won&apos;t show it again. Store it
                somewhere safe.
              </span>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 truncate rounded-md border border-border bg-muted/40 px-2.5 py-2 font-mono text-[12px] text-foreground">
                {token}
              </code>
              <button
                onClick={copy}
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-semibold text-primary-foreground hover:bg-primary/90"
              >
                {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? "Copied" : "Copy"}
              </button>
            </div>
            <div className="flex justify-end pt-1">
              <button
                onClick={onCreated}
                className="h-9 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90"
              >
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

