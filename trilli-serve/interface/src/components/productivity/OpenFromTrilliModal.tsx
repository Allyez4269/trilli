// "Open from Trilli" modal for the Productivity editor — navigate workspace →
// folder (like the Files browser) and pick a document to open, filtered by a
// file-type dropdown. Styled to match Files / the Save modal.
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronRight, FileText, Folder, HardDrive, Loader2, X } from "lucide-react";

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

export function OpenFromTrilliModal({
  extensions,
  onClose,
  onOpen,
}: {
  // Selectable file types, e.g. ["docx", "doc"]; the first is the default.
  extensions: string[];
  onClose: () => void;
  onOpen: (file: FileRecord) => Promise<void>;
}) {
  const [workspaces, setWorkspaces] = useState<Workspace[] | null>(null);
  const [wsId, setWsId] = useState<number | null>(null);
  const [folderId, setFolderId] = useState<number | null>(null);
  const [crumbs, setCrumbs] = useState<Crumb[]>([]);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [files, setFiles] = useState<FileRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [ext, setExt] = useState(extensions[0] ?? "docx");
  const [selected, setSelected] = useState<FileRecord | null>(null);
  const [opening, setOpening] = useState(false);
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
    setSelected(null);
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
      .catch(() => alive && setError("Couldn't load this folder."))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [wsId, folderId, workspaces]);

  const visibleFiles = useMemo(
    () => files.filter((f) => f.name.toLowerCase().endsWith(`.${ext.toLowerCase()}`)),
    [files, ext],
  );

  async function handleOpen() {
    if (!selected) return;
    setOpening(true);
    setError(null);
    try {
      await onOpen(selected);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't open this document.");
      setOpening(false);
    }
  }

  return createPortal(
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !opening) onClose();
      }}
    >
      <div className="flex max-h-[82vh] w-full max-w-lg flex-col overflow-hidden rounded-xl border border-border bg-card shadow-2xl">
        <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 className="text-sm font-semibold text-foreground">Open from Trilli</h2>
          <button
            type="button"
            onClick={onClose}
            disabled={opening}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4">
          {/* Workspace + file type */}
          <div className="flex gap-2">
            <div className="relative flex-1">
              <HardDrive className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <select
                value={wsId ?? ""}
                onChange={(e) => {
                  setWsId(Number(e.target.value));
                  setFolderId(null);
                }}
                disabled={!workspaces || opening}
                className="h-9 w-full appearance-none rounded-lg border border-border bg-background pl-9 pr-3 text-[13px] text-foreground outline-none focus:border-primary disabled:opacity-60"
              >
                {!workspaces && <option>Loading…</option>}
                {workspaces?.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </select>
            </div>
            <select
              value={ext}
              onChange={(e) => setExt(e.target.value)}
              disabled={opening}
              className="h-9 w-28 appearance-none rounded-lg border border-border bg-background px-3 text-[13px] text-foreground outline-none focus:border-primary disabled:opacity-60"
              title="File type"
            >
              {extensions.map((x) => (
                <option key={x} value={x}>
                  {x.toUpperCase()}
                </option>
              ))}
            </select>
          </div>

          {/* Breadcrumb */}
          <div className="flex flex-wrap items-center gap-0.5 text-[12px]">
            {crumbs.map((c, i) => (
              <span key={`${c.id ?? "root"}-${i}`} className="flex items-center">
                {i > 0 && <ChevronRight className="mx-0.5 h-3 w-3 text-muted-foreground" />}
                <button
                  type="button"
                  onClick={() => setFolderId(c.id)}
                  disabled={opening}
                  className={cn(
                    "rounded px-1.5 py-0.5 transition-colors hover:bg-muted",
                    i === crumbs.length - 1 ? "font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  {c.name}
                </button>
              </span>
            ))}
          </div>

          {/* Folders + files */}
          <div className="h-56 overflow-y-auto rounded-lg border border-border bg-background">
            {loading ? (
              <div className="flex h-full items-center justify-center">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground/50" />
              </div>
            ) : folders.length === 0 && visibleFiles.length === 0 ? (
              <div className="flex h-full items-center justify-center px-4 text-center text-[12px] text-muted-foreground">
                No {ext.toUpperCase()} files or subfolders here.
              </div>
            ) : (
              <ul className="divide-y divide-border/60">
                {folders.map((f) => (
                  <li key={`d-${f.id}`}>
                    <button
                      type="button"
                      onClick={() => setFolderId(f.id)}
                      disabled={opening}
                      className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-muted"
                    >
                      <Folder className="h-4 w-4 flex-shrink-0 text-primary/70" />
                      <span className="flex-1 truncate text-[13px] text-foreground">{f.name}</span>
                      <ChevronRight className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                    </button>
                  </li>
                ))}
                {visibleFiles.map((f) => (
                  <li key={`f-${f.id}`}>
                    <button
                      type="button"
                      onClick={() => setSelected(f)}
                      onDoubleClick={() => {
                        setSelected(f);
                        handleOpen();
                      }}
                      disabled={opening}
                      className={cn(
                        "flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-muted",
                        selected?.id === f.id && "bg-primary/10 hover:bg-primary/10",
                      )}
                    >
                      <FileText className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                      <span className="flex-1 truncate text-[13px] text-foreground">{f.name}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {error && <p className="text-[12px] text-destructive">{error}</p>}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={opening}
            className="h-8 rounded-lg border border-border px-3 text-[13px] text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleOpen}
            disabled={opening || !selected}
            className="flex h-8 items-center gap-1.5 rounded-lg bg-primary px-3.5 text-[13px] font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-60"
          >
            {opening && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Open
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
