// "Save to Trilli" modal for the Productivity editor — pick a destination
// workspace + folder (lazy-navigated, like the Files browser) and a filename,
// then persist the document as a real Trilli file. Styled to match Files.
//
// Shows existing FILES (not just folders) in the current location so the user
// can see what's already there. No conflict dialog — if a name matches, the
// server overwrites the existing file in-place (Save As semantics).
import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import {
  ChevronRight,
  File as FileIcon,
  Folder,
  HardDrive,
  Loader2,
  X,
} from "lucide-react";

import {
  api,
  type BrowseResponse,
  type FileRecord,
  type FolderRecord,
  type Workspace,
  type WorkspacesResponse,
} from "@/lib/api";
import { cn } from "@/lib/utils";

type Crumb = { id: number | null; name: string };

export interface SaveTarget {
  workspaceId: number;
  folderId: number | null;
  name: string;
  format: string;
  location: string;
  overwriteFileId?: number;
}

export function SaveToTrilliModal({
  defaultName,
  formats,
  format,
  onFormatChange,
  onClose,
  onSave,
}: {
  defaultName: string;
  formats: { ext: string; label: string }[];
  format: { ext: string; label: string };
  onFormatChange: (f: { ext: string; label: string }) => void;
  onClose: () => void;
  onSave: (target: SaveTarget) => Promise<void>;
}) {
  const [workspaces, setWorkspaces] = useState<Workspace[] | null>(null);
  const [wsId, setWsId] = useState<number | null>(null);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [crumbs, setCrumbs] = useState<Crumb[]>([]);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [files, setFiles] = useState<FileRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [name, setName] = useState(defaultName);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    api
      .get<WorkspacesResponse>("/api/workspaces")
      .then((r) => {
        if (!alive) return;
        setWorkspaces(r.workspaces);
        if (r.workspaces[0]) setWsId(r.workspaces[0].id);
      })
      .catch(() => alive && setError("Couldn't load your workspaces."));
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (wsId == null) return;
    let alive = true;
    setLoading(true);
    const q = folderId != null ? `?folder_id=${folderId}` : `?workspace_id=${wsId}`;
    api
      .get<BrowseResponse>(`/api/browse${q}`)
      .then((r) => {
        if (!alive) return;
        setFolders(r.folders ?? []);
        setFiles(r.files ?? []);
        const wsName = workspaces?.find((w) => w.id === wsId)?.name ?? "Workspace";
        const trail = (r.breadcrumb ?? []).map((b) => ({ id: b.id, name: b.name }));
        setCrumbs([{ id: null, name: wsName }, ...trail]);
      })
      .catch(() => alive && setError("Couldn't load folders."))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [wsId, folderId, workspaces]);

  const fullFileName = `${name.trim()}.${format.ext}`;

  async function doSave() {
    if (wsId == null) return;
    const n = name.trim();
    if (!n) {
      setError("Please enter a file name.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const loc = crumbs.map((c) => c.name).join(" / ") || "—";
      // If a file with the same name exists in this folder, auto-overwrite it.
      const existing = files.find((f) => f.name.toLowerCase() === fullFileName.toLowerCase());
      await onSave({
        workspaceId: wsId,
        folderId,
        name: n,
        format: format.ext,
        location: loc,
        overwriteFileId: existing?.id,
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed. Please try again.");
      setSaving(false);
    }
  }

  const location = crumbs.map((c) => c.name).join(" / ") || "—";

  return createPortal(
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !saving) onClose();
      }}
    >
      <div className="flex max-h-[82vh] w-full max-w-lg flex-col overflow-hidden rounded-xl border border-border bg-card shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 className="text-sm font-semibold text-foreground">Save to Trilli</h2>
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4">
          {/* File name + format picker */}
          <div>
            <label className="mb-1 block text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              File name
            </label>
            <div className="flex items-stretch gap-2">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={saving}
                autoFocus
                className="h-9 min-w-0 flex-1 rounded-lg border border-border bg-background px-3 text-[13px] text-foreground outline-none focus:border-primary"
                onKeyDown={(e) => {
                  if (e.key === "Enter") doSave();
                }}
              />
              <label className="relative flex-shrink-0" title="Save as format">
                <select
                  value={format.ext}
                  onChange={(e) => {
                    const next = formats.find((f) => f.ext === e.target.value);
                    if (next) onFormatChange(next);
                  }}
                  disabled={saving || formats.length <= 1}
                  className="h-9 cursor-pointer appearance-none rounded-lg border border-border bg-background pl-2.5 pr-7 text-[12px] font-medium text-foreground outline-none transition-colors hover:bg-muted focus:border-primary disabled:cursor-default disabled:opacity-70"
                >
                  {formats.map((f) => (
                    <option key={f.ext} value={f.ext}>
                      .{f.ext}
                    </option>
                  ))}
                </select>
                <ChevronRight className="pointer-events-none absolute right-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 rotate-90 text-muted-foreground" />
              </label>
            </div>
          </div>

          {/* Workspace */}
          <div>
            <label className="mb-1 block text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Workspace
            </label>
            <div className="relative">
              <HardDrive className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <select
                value={wsId ?? ""}
                onChange={(e) => {
                  setWsId(Number(e.target.value));
                  setFolderId(null);
                }}
                disabled={!workspaces || saving}
                className="h-9 w-full appearance-none rounded-lg border border-border bg-background pl-9 pr-8 text-[13px] text-foreground outline-none focus:border-primary disabled:opacity-60"
              >
                {!workspaces && <option>Loading…</option>}
                {workspaces?.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </select>
              <ChevronRight className="pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 rotate-90 text-muted-foreground" />
            </div>
          </div>

          {/* Folder breadcrumb */}
          <div className="flex flex-wrap items-center gap-0.5 text-[12px]">
            {crumbs.map((c, i) => (
              <span key={`${c.id ?? "root"}-${i}`} className="flex items-center">
                {i > 0 && <ChevronRight className="mx-0.5 h-3 w-3 text-muted-foreground" />}
                <button
                  type="button"
                  onClick={() => setFolderId(c.id)}
                  disabled={saving}
                  className={cn(
                    "rounded px-1.5 py-0.5 transition-colors hover:bg-muted",
                    i === crumbs.length - 1
                      ? "font-medium text-foreground"
                      : "text-muted-foreground",
                  )}
                >
                  {c.name}
                </button>
              </span>
            ))}
          </div>

          {/* Folder + file list */}
          <div className="h-48 overflow-y-auto rounded-lg border border-border bg-background">
            {loading ? (
              <div className="flex h-full items-center justify-center">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground/50" />
              </div>
            ) : folders.length === 0 && files.length === 0 ? (
              <div className="flex h-full items-center justify-center px-4 text-center text-[12px] text-muted-foreground">
                This location is empty — the document will be saved here.
              </div>
            ) : (
              <ul className="divide-y divide-border/60">
                {folders.map((f) => (
                  <li key={`f-${f.id}`}>
                    <button
                      type="button"
                      onClick={() => setFolderId(f.id)}
                      disabled={saving}
                      className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-muted"
                    >
                      <Folder className="h-4 w-4 flex-shrink-0 text-primary/70" />
                      <span className="flex-1 truncate text-[13px] text-foreground">{f.name}</span>
                      <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                    </button>
                  </li>
                ))}
                {files.map((f) => {
                  const isMatch = f.name.toLowerCase() === fullFileName.toLowerCase();
                  return (
                    <li key={`file-${f.id}`}>
                      <div
                        className={cn(
                          "flex w-full items-center gap-2 px-3 py-2",
                          isMatch && "bg-primary/5",
                        )}
                      >
                        <FileIcon className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                        <span
                          className={cn(
                            "flex-1 truncate text-[13px]",
                            isMatch ? "font-medium text-primary" : "text-muted-foreground",
                          )}
                        >
                          {f.name}
                        </span>
                        {isMatch && (
                          <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-primary">
                            Will overwrite
                          </span>
                        )}
                      </div>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {error && <p className="text-[12px] text-destructive">{error}</p>}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between gap-3 border-t border-border px-5 py-3">
          <span className="min-w-0 flex-1 truncate text-[12px] text-muted-foreground">
            Saving into: <span className="text-foreground">{location}</span>
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={saving}
              className="h-8 rounded-lg border border-border px-3 text-[13px] text-foreground transition-colors hover:bg-muted disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={doSave}
              disabled={saving || wsId == null}
              className="flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3.5 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-60"
            >
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
