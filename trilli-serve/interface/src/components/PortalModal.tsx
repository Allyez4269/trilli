import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, Inbox, Loader2, X } from "lucide-react";

import { api, ApiError, type Portal } from "@/lib/api";
import { checkboxCls } from "@/lib/utils";

export interface PortalTarget {
  folderId: number;
  folderName: string;
}

// PortalModal creates OR edits a drop portal (file request). Pass `target` to
// create a new portal on a folder, or `editing` to edit an existing one.
export default function PortalModal({
  target,
  editing,
  onClose,
  onSaved,
}: {
  target?: PortalTarget;
  editing?: Portal;
  onClose: () => void;
  onSaved?: (p: Portal) => void;
}) {
  const isEdit = !!editing;
  const folderName = editing?.folder_name ?? target?.folderName ?? "this folder";

  const [title, setTitle] = useState(editing?.title || (target ? `Upload to ${target.folderName}` : ""));
  const [expiry, setExpiry] = useState(isEdit ? "keep" : "0");
  const [password, setPassword] = useState("");
  const [removePw, setRemovePw] = useState(false);
  const [notify, setNotify] = useState(editing?.notify_on_upload ?? false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [portal, setPortal] = useState<Portal | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const fullUrl = portal ? `${window.location.origin}${portal.url}` : "";

  const submit = async () => {
    setCreating(true);
    setError(null);
    try {
      const days = parseInt(expiry, 10);
      if (isEdit) {
        const payload: Record<string, unknown> = { title: title.trim(), notify_on_upload: notify };
        if (expiry !== "keep") payload.expires_in_days = days > 0 ? days : 0;
        if (removePw) payload.password = "";
        else if (password.trim()) payload.password = password.trim();
        const p = await api.patch<Portal>(`/api/portals/${editing!.id}`, payload);
        onSaved?.(p);
        onClose();
        return;
      }
      const p = await api.post<Portal>("/api/portals", {
        folder_id: target!.folderId,
        title: title.trim(),
        expires_in_days: days > 0 ? days : null,
        password: password.trim() || undefined,
        notify_on_upload: notify,
      });
      setPortal(p);
      onSaved?.(p);
    } catch (e) {
      setError(e instanceof ApiError && e.message ? e.message : "Couldn't save the request.");
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

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <div className="flex min-w-0 items-center gap-2">
            <Inbox className="h-4 w-4 flex-shrink-0 text-primary" />
            <h2 className="truncate text-sm font-semibold text-foreground">
              {isEdit ? "Edit file request" : `Request files into “${folderName}”`}
            </h2>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </div>

        {!portal ? (
          <div className="space-y-4 px-5 py-4">
            {!isEdit && (
              <p className="text-[13px] text-muted-foreground">
                Anyone with this link can upload files straight into this folder — no Trilli account needed.
              </p>
            )}
            <Field label="What to show them">
              <input
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                className="h-9 w-full rounded-md border border-border bg-background px-2.5 text-[13px]"
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Request expires">
                <select
                  value={expiry}
                  onChange={(e) => setExpiry(e.target.value)}
                  className="h-9 w-full rounded-md border border-border bg-background px-2 text-[13px]"
                >
                  {isEdit && <option value="keep">Keep current</option>}
                  <option value="0">No expiry</option>
                  <option value="1">1 day</option>
                  <option value="7">7 days</option>
                  <option value="30">30 days</option>
                  <option value="90">90 days</option>
                </select>
              </Field>
              <Field label={isEdit && editing?.has_password ? "Change password" : "Password (optional)"}>
                <input
                  type="text"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={removePw}
                  placeholder={isEdit && editing?.has_password ? "Leave blank to keep" : "None"}
                  className="h-9 w-full rounded-md border border-border bg-background px-2.5 text-[13px] disabled:opacity-50"
                />
              </Field>
            </div>
            {isEdit && editing?.has_password && (
              <label className="flex items-center gap-2 text-[12px] text-muted-foreground">
                <input type="checkbox" checked={removePw} onChange={(e) => setRemovePw(e.target.checked)} className={checkboxCls} />
                Remove password
              </label>
            )}

            <label className="flex items-center gap-2 rounded-lg border border-border bg-muted/20 px-3 py-2.5 text-[13px]">
              <input type="checkbox" checked={notify} onChange={(e) => setNotify(e.target.checked)} className={checkboxCls} />
              <span>
                <span className="font-medium text-foreground">Email me on each upload</span>
                <span className="block text-[11px] text-muted-foreground">Get an alert whenever someone drops a file here.</span>
              </span>
            </label>

            {error && <p className="text-[12px] text-destructive">{error}</p>}
            <div className="flex justify-end gap-2 pt-1">
              <button onClick={onClose} className="h-9 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground hover:bg-muted">
                Cancel
              </button>
              <button
                onClick={submit}
                disabled={creating}
                className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
              >
                {creating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Inbox className="h-3.5 w-3.5" />}
                {isEdit ? "Save changes" : "Create request link"}
              </button>
            </div>
          </div>
        ) : (
          <div className="space-y-3 px-5 py-4">
            <p className="text-[13px] font-medium text-foreground">Your file-request link is ready</p>
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
              <li>· Uploads land in “{folderName}”</li>
              <li>· {portal.expires_at ? `Expires ${new Date(portal.expires_at).toLocaleDateString()}` : "No expiry"}</li>
              <li>· {portal.has_password ? "Password protected" : "No password"}</li>
              <li>· {portal.notify_on_upload ? "You'll be emailed on each upload" : "No upload alerts"}</li>
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

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1.5 block text-[12px] font-medium text-foreground">{label}</label>
      {children}
    </div>
  );
}
