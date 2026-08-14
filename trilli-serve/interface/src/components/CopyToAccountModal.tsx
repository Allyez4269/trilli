import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  Building2,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Copy,
  Folder,
  FolderRoot,
  Info,
  Layers,
  Loader2,
  Users,
  X,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";

export interface CopyAccount {
  id: number;
  name: string;
  is_owner: boolean;
}

interface CopyResult {
  copied_files: number;
  copied_folders: number;
  errors?: string[];
}

interface DestWorkspace {
  id: number;
  name: string;
}
interface NamedRef {
  id: number;
  name: string;
}
interface DestBrowse {
  workspaces: DestWorkspace[];
  folder: NamedRef | null;
  breadcrumb: NamedRef[] | null;
  folders: NamedRef[] | null;
}

// CopyToAccountModal copies the selected files/folders from the CURRENT account
// into another account the user belongs to. Cross-account transfers are always
// copies (never moves) — the originals stay put — which the modal discloses.
// Two steps: (1) pick the destination account, (2) browse that account's
// workspaces/folders and choose exactly where the copy lands. The backend
// (POST /api/files/copy-to-account) authorizes write access and does the real
// re-encrypted byte copy; /api/copy-destinations powers the folder browser.
export default function CopyToAccountModal({
  fileCount,
  folderCount,
  fileIds,
  folderIds,
  accounts,
  currentAccountName,
  initialDestId,
  onClose,
  onCopied,
}: {
  fileCount: number;
  folderCount: number;
  fileIds: number[];
  folderIds: number[];
  accounts: CopyAccount[];
  currentAccountName: string;
  initialDestId?: number | null;
  onClose: () => void;
  onCopied?: () => void;
}) {
  const total = fileCount + folderCount;
  const single = accounts.length === 1;
  const firstId = initialDestId ?? (single ? accounts[0]?.id ?? null : null);
  const [destId, setDestId] = useState<number | null>(firstId);
  const [step, setStep] = useState<"account" | "folder">(firstId != null ? "folder" : "account");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<CopyResult | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !busy && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, busy]);

  const selectionLabel = [
    fileCount > 0 ? `${fileCount} file${fileCount === 1 ? "" : "s"}` : null,
    folderCount > 0 ? `${folderCount} folder${folderCount === 1 ? "" : "s"}` : null,
  ]
    .filter(Boolean)
    .join(" and ");

  const destName = accounts.find((a) => a.id === destId)?.name ?? "the other account";

  const submit = async (workspaceId: number | null, folderId: number | null) => {
    if (busy || destId == null) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<CopyResult>("/api/files/copy-to-account", {
        file_ids: fileIds,
        folder_ids: folderIds,
        dest_tenant_id: destId,
        dest_workspace_id: workspaceId ?? undefined,
        dest_folder_id: folderId ?? undefined,
      });
      setResult(res);
      onCopied?.();
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Couldn't copy." : "Network error.");
    } finally {
      setBusy(false);
    }
  };

  const canGoBack = step === "folder" && !single && initialDestId == null && !result;

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}
    >
      <div className="flex max-h-[85vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200 ease-out">
        {/* Header */}
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
          {canGoBack ? (
            <button
              type="button"
              onClick={() => setStep("account")}
              className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              aria-label="Back to account list"
            >
              <ChevronLeft className="h-[18px] w-[18px]" />
            </button>
          ) : (
            <span className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <Copy className="h-[18px] w-[18px]" />
            </span>
          )}
          <div className="min-w-0 flex-1">
            <h2 className="text-[14px] font-semibold text-foreground">
              {step === "folder" && !result ? `Copy to ${destName}` : "Copy to another account"}
            </h2>
            <p className="truncate text-[12px] text-muted-foreground">
              {step === "folder" && !result
                ? "Choose where the copy lands"
                : `${selectionLabel || `${total} item${total === 1 ? "" : "s"}`} from ${currentAccountName}`}
            </p>
          </div>
          <button
            type="button"
            onClick={() => !busy && onClose()}
            className="text-muted-foreground transition-colors hover:text-foreground disabled:opacity-40"
            disabled={busy}
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {result ? (
          // ----- success / result summary -----
          <div className="flex flex-col gap-3 px-5 py-6">
            <div className="flex items-start gap-3">
              <CheckCircle2 className="mt-0.5 h-5 w-5 flex-shrink-0 text-emerald-600" />
              <div className="min-w-0">
                <p className="text-[14px] font-semibold text-foreground">Copied to {destName}</p>
                <p className="mt-0.5 text-[12.5px] text-muted-foreground">
                  {result.copied_files} file{result.copied_files === 1 ? "" : "s"}
                  {result.copied_folders > 0 && (
                    <> and {result.copied_folders} folder{result.copied_folders === 1 ? "" : "s"}</>
                  )}{" "}
                  copied. The originals are still in {currentAccountName}.
                </p>
              </div>
            </div>
            {result.errors && result.errors.length > 0 && (
              <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[12px] text-amber-700">
                <p className="font-medium">Some items couldn't be copied:</p>
                <ul className="mt-1 list-disc space-y-0.5 pl-4">
                  {result.errors.slice(0, 6).map((m, i) => (
                    <li key={i}>{m}</li>
                  ))}
                  {result.errors.length > 6 && <li>…and {result.errors.length - 6} more</li>}
                </ul>
              </div>
            )}
            <button
              type="button"
              onClick={onClose}
              className="mt-1 h-10 w-full rounded-lg bg-primary text-[13.5px] font-semibold text-primary-foreground hover:bg-primary/90"
            >
              Done
            </button>
          </div>
        ) : step === "account" ? (
          // ----- step 1: pick destination account -----
          <>
            <div className="flex-1 overflow-y-auto px-4 py-3">
              <p className="mb-2 px-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Destination account
              </p>
              <div className="space-y-1">
                {accounts.map((a) => (
                  <button
                    key={a.id}
                    type="button"
                    onClick={() => {
                      setDestId(a.id);
                      setStep("folder");
                    }}
                    className="group flex w-full items-center gap-2.5 rounded-lg border border-border bg-card px-3 py-2.5 text-left transition-colors hover:border-primary/40 hover:bg-primary/[0.03]"
                  >
                    <span className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                      {a.is_owner ? <Building2 className="h-4 w-4" /> : <Users className="h-4 w-4" />}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[13.5px] font-medium text-foreground">{a.name}</span>
                      <span className="block text-[11.5px] text-muted-foreground">
                        {a.is_owner ? "Your account" : "Shared with you"}
                      </span>
                    </span>
                    <ChevronRight className="h-4 w-4 flex-shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
                  </button>
                ))}
              </div>
              <Disclosure currentAccountName={currentAccountName} />
            </div>
            <div className="flex items-center justify-end border-t border-border px-4 py-3">
              <button
                type="button"
                onClick={onClose}
                className="h-9 rounded-lg border border-border bg-card px-3.5 text-[13px] font-medium text-foreground hover:bg-muted"
              >
                Cancel
              </button>
            </div>
          </>
        ) : (
          // ----- step 2: pick destination folder in the chosen account -----
          <>
            <Disclosure currentAccountName={currentAccountName} className="mx-4 mt-3" />
            <DestPicker
              key={destId ?? 0}
              tenantId={destId!}
              total={total}
              busy={busy}
              onCopy={submit}
            />
          </>
        )}

        {error && !result && (
          <p className="border-t border-border px-4 py-2.5 text-[12.5px] text-destructive">{error}</p>
        )}
      </div>
    </div>,
    document.body,
  );
}

// Disclosure: the cross-account-transfer-is-a-copy notice. Shown on both steps.
function Disclosure({ currentAccountName, className }: { currentAccountName: string; className?: string }) {
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2.5 text-[12px] text-muted-foreground",
        className,
      )}
    >
      <Info className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-primary" />
      <span>
        This <span className="font-medium text-foreground">copies</span> your selection — the
        originals stay in {currentAccountName}. Counts against the destination account's storage.
      </span>
    </div>
  );
}

// DestPicker browses a DESTINATION tenant's workspaces + folder tree (via
// /api/copy-destinations) so the user can choose exactly where the copy lands.
// Mirrors the in-account MovePicker, but cross-tenant and read-only navigation.
function DestPicker({
  tenantId,
  total,
  busy,
  onCopy,
}: {
  tenantId: number;
  total: number;
  busy: boolean;
  onCopy: (workspaceId: number | null, folderId: number | null) => void;
}) {
  const [workspaces, setWorkspaces] = useState<DestWorkspace[]>([]);
  const [wsId, setWsId] = useState<number | null>(null);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [folders, setFolders] = useState<NamedRef[]>([]);
  const [crumb, setCrumb] = useState<NamedRef[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Load the destination account's workspaces once, default to the first.
  useEffect(() => {
    let cancelled = false;
    api
      .get<DestBrowse>(`/api/copy-destinations?tenant_id=${tenantId}`)
      .then((b) => {
        if (cancelled) return;
        const ws = b.workspaces ?? [];
        setWorkspaces(ws);
        setWsId(ws[0]?.id ?? null);
        if (ws.length === 0) setLoading(false);
      })
      .catch(() => {
        if (!cancelled) {
          setLoadError("Couldn't open that account.");
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [tenantId]);

  // Load folders whenever the workspace or current folder changes.
  useEffect(() => {
    if (wsId == null) return;
    let cancelled = false;
    setLoading(true);
    const qs = new URLSearchParams({ tenant_id: String(tenantId), workspace_id: String(wsId) });
    if (folderId != null) qs.set("folder_id", String(folderId));
    api
      .get<DestBrowse>(`/api/copy-destinations?${qs.toString()}`)
      .then((b) => {
        if (cancelled) return;
        setFolders(b.folders ?? []);
        setCrumb(b.breadcrumb ?? []);
      })
      .catch(() => {
        if (!cancelled) {
          setFolders([]);
          setCrumb([]);
        }
      })
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [tenantId, wsId, folderId]);

  const wsName = workspaces.find((w) => w.id === wsId)?.name ?? "Workspace";
  const hereLabel = folderId == null ? `${wsName} (home)` : crumb[crumb.length - 1]?.name ?? "here";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Workspace selector */}
      <div className="mt-3 flex items-center gap-2 border-y border-border px-4 py-2.5">
        <Layers className="h-4 w-4 flex-shrink-0 text-primary" />
        {workspaces.length > 1 ? (
          <select
            value={wsId ?? ""}
            onChange={(e) => {
              setWsId(Number(e.target.value));
              setFolderId(null);
            }}
            className="flex-1 rounded-md border border-border bg-background px-2 py-1 text-[13px] text-foreground outline-none focus:border-primary/40"
          >
            {workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </select>
        ) : (
          <span className="flex-1 truncate text-[13px] font-medium text-foreground">{wsName}</span>
        )}
      </div>

      {/* Breadcrumb path within the chosen workspace */}
      <div className="flex flex-wrap items-center gap-0.5 border-b border-border px-4 py-2 text-[12px]">
        <button
          type="button"
          onClick={() => setFolderId(null)}
          className={cn(
            "flex items-center gap-1 rounded px-1 py-0.5 font-mono transition-colors",
            folderId == null
              ? "font-semibold text-foreground"
              : "text-muted-foreground hover:bg-primary/10 hover:text-primary",
          )}
        >
          <FolderRoot className="h-3.5 w-3.5" />
          ..\
        </button>
        {crumb.map((c, i) => (
          <span key={c.id} className="flex items-center">
            {i > 0 && <span className="text-muted-foreground/50">\</span>}
            {i === crumb.length - 1 ? (
              <span className="px-1 font-medium text-foreground">{c.name}</span>
            ) : (
              <button
                type="button"
                onClick={() => setFolderId(c.id)}
                className="rounded px-1 py-0.5 text-muted-foreground hover:bg-primary/10 hover:text-primary"
              >
                {c.name}
              </button>
            )}
          </span>
        ))}
      </div>

      {/* Folder list (drill in) */}
      <div className="min-h-[140px] flex-1 overflow-y-auto p-2">
        {loading ? (
          <div className="flex items-center justify-center py-12 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : loadError ? (
          <p className="px-3 py-8 text-center text-[13px] text-destructive">{loadError}</p>
        ) : folders.length === 0 ? (
          <p className="px-3 py-10 text-center text-[13px] text-muted-foreground">
            No subfolders here. Use “Copy here” to drop into {hereLabel}.
          </p>
        ) : (
          folders.map((f) => (
            <button
              key={f.id}
              type="button"
              onClick={() => setFolderId(f.id)}
              className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13.5px] text-foreground transition-colors hover:bg-muted"
            >
              <Folder className="h-4 w-4 flex-shrink-0 fill-amber-300 text-amber-500" />
              <span className="flex-1 truncate">{f.name}</span>
              <ChevronRight className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
            </button>
          ))
        )}
      </div>

      {/* Copy-here action */}
      <div className="border-t border-border px-4 py-3">
        <button
          type="button"
          disabled={busy || wsId == null}
          onClick={() => onCopy(wsId, folderId)}
          className="flex w-full items-center justify-center gap-2 rounded-lg bg-primary px-4 py-2.5 text-[13.5px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" /> Copying…
            </>
          ) : (
            <>
              <Copy className="h-4 w-4" />
              Copy {total} item{total === 1 ? "" : "s"} to {hereLabel}
            </>
          )}
        </button>
      </div>
    </div>
  );
}
