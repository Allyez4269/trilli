import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  ChevronRight,
  Folder,
  FolderRoot,
  Layers,
  Loader2,
  Move,
  Save,
  X,
} from "lucide-react";

import { api, type BreadcrumbItem, type BrowseResponse } from "@/lib/api";
import { cn } from "@/lib/utils";

export interface WorkspaceLite {
  id: number;
  name: string;
}

// MoveItemsModal wraps the workspace-aware destination browser. The other
// bulk actions (star/download/delete/clear) live inline in the Files
// selection bar; only "Move to…" needs a folder browser, so it stays a modal.
export default function MoveItemsModal({
  fileCount,
  folderCount,
  workspaces,
  currentWorkspaceId,
  currentFolderId,
  selectedFolderIds,
  onMove,
  onClose,
  // Reused as a generic destination chooser (e.g. Sign's "Envelopes save
  // to"): custom chrome + action label, and noop destinations stay clickable.
  title = "Move to…",
  subtitle = "Pick a workspace and destination folder",
  actionPrefix,
  allowNoop = false,
  iconKind = "move",
}: {
  fileCount: number;
  folderCount: number;
  workspaces: WorkspaceLite[];
  currentWorkspaceId: number | null;
  currentFolderId: number | null;
  selectedFolderIds: number[];
  onMove: (targetFolderId: number | null, targetWorkspaceId: number) => void;
  onClose: () => void;
  title?: string;
  subtitle?: string;
  actionPrefix?: string;
  allowNoop?: boolean;
  iconKind?: "move" | "save";
}) {
  const total = fileCount + folderCount;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="flex max-h-[80vh] w-full max-w-md flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl animate-in fade-in zoom-in-95 duration-200 ease-out">
        {/* Header */}
        <div className="flex items-center gap-3 border-b border-border px-5 py-3.5">
          <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            {iconKind === "save" ? <Save className="h-[18px] w-[18px]" /> : <Move className="h-[18px] w-[18px]" />}
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-[14px] font-semibold text-foreground">{title}</h2>
            <p className="truncate text-[12px] text-muted-foreground">{subtitle}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-muted-foreground transition-colors hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <MovePicker
          workspaces={workspaces}
          currentWorkspaceId={currentWorkspaceId}
          currentFolderId={currentFolderId}
          selectedFolderIds={selectedFolderIds}
          total={total}
          actionPrefix={actionPrefix}
          allowNoop={allowNoop}
          onCancel={onClose}
          onChoose={(folderId, wsId) => {
            onMove(folderId, wsId);
            onClose();
          }}
        />
      </div>
    </div>,
    document.body,
  );
}

// MovePicker browses workspaces + their folder trees to choose a destination.
function MovePicker({
  workspaces,
  currentWorkspaceId,
  currentFolderId,
  selectedFolderIds,
  total,
  onChoose,
  onCancel,
  actionPrefix,
  allowNoop = false,
}: {
  workspaces: WorkspaceLite[];
  currentWorkspaceId: number | null;
  currentFolderId: number | null;
  selectedFolderIds: number[];
  total: number;
  onChoose: (folderId: number | null, wsId: number) => void;
  onCancel: () => void;
  actionPrefix?: string;
  allowNoop?: boolean;
}) {
  const [wsId, setWsId] = useState<number>(currentWorkspaceId ?? workspaces[0]?.id ?? 0);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [folders, setFolders] = useState<{ id: number; name: string }[]>([]);
  const [crumb, setCrumb] = useState<BreadcrumbItem[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const qs = new URLSearchParams({ workspace_id: String(wsId) });
    if (folderId != null) qs.set("folder_id", String(folderId));
    api
      .get<BrowseResponse>(`/api/browse?${qs.toString()}`)
      .then((b) => {
        if (cancelled) return;
        setFolders(
          (b.folders ?? [])
            .filter((f) => !selectedFolderIds.includes(f.id)) // can't move into a moved folder
            .map((f) => ({ id: f.id, name: f.name })),
        );
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
  }, [wsId, folderId, selectedFolderIds]);

  const wsName = workspaces.find((w) => w.id === wsId)?.name ?? "Workspace";
  // Disable "Move here" when the destination is exactly where the items live.
  const isNoop = !allowNoop && wsId === currentWorkspaceId && folderId === currentFolderId;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Workspace selector */}
      <div className="flex items-center gap-2.5 border-b border-border px-5 py-3">
        <Layers className="h-4 w-4 flex-shrink-0 text-primary" />
        <select
          value={wsId}
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
      </div>

      {/* Path within the chosen workspace */}
      <div className="flex flex-wrap items-center gap-0.5 border-b border-border px-5 py-2.5 text-[12px]">
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
      <div className="min-h-[140px] flex-1 overflow-y-auto p-3">
        {loading ? (
          <div className="flex items-center justify-center py-10 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : folders.length === 0 ? (
          <p className="px-3 py-8 text-center text-[13px] text-muted-foreground">
            No subfolders here. Drop the items at this level with "Move here".
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

      {/* Footer — compact, right-aligned, same rhythm as the app's dialogs */}
      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3.5">
        <button
          type="button"
          onClick={onCancel}
          className="h-8 rounded-md border border-border bg-background px-2.5 text-[12.5px] font-medium text-foreground outline-none hover:bg-muted focus:outline-none focus-visible:outline-none"
        >
          Cancel
        </button>
        <button
          type="button"
          disabled={isNoop}
          onClick={() => onChoose(folderId, wsId)}
          className="inline-flex h-8 max-w-[280px] items-center gap-1.5 rounded-md bg-primary px-3 text-[12.5px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <span className="truncate">
            {isNoop
              ? "Already here"
              : actionPrefix
                ? `${actionPrefix} ${folderId == null ? `${wsName} root` : crumb[crumb.length - 1]?.name ?? "here"}`
                : `Move ${total} item${total === 1 ? "" : "s"} to ${
                    folderId == null ? `${wsName} root` : crumb[crumb.length - 1]?.name ?? "here"
                  }`}
          </span>
        </button>
      </div>
    </div>
  );
}
