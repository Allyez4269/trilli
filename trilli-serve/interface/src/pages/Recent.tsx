import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Clock,
  Download,
  Edit3,
  Eye,
  File as FileIconGeneric,
  Files as FilesIcon,
  MoreHorizontal,
  Move,
  Pencil,
  Search,
  Share2,
  Star,
  StarOff,
  Trash2,
  X,
} from "lucide-react";

import {
  api,
  ApiError,
  downloadUrl,
  type FileRecord,
  type FileStats,
  type RecentPage,
  type SearchResponse,
  type WorkspacesResponse,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { FileTypeChips, OTHER_EXT } from "@/components/FileTypeChips";
import FilePreview, { printUrlFor } from "@/components/FilePreview";
import ShareModal, { type ShareTarget } from "@/components/ShareModal";
import MoveItemsModal from "@/components/SelectionActionsModal";
import { NameDialog, ConfirmDialog } from "@/components/Dialogs";
import { useAuth } from "@/contexts/AuthContext";
import { InfoBanner } from "@/components/InfoBanner";
import { Skeleton } from "@/components/ui/skeleton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { emitTrashChanged } from "@/lib/events";
import { setFileDownloadDrag } from "@/lib/drag";
import { Input } from "@/components/ui/input";
import FileThumb from "@/components/FileThumb";
import {
  fileExtension,
  formatBytes,
  formatDateTime,
  truncateName,
} from "@/lib/files-meta";
import { editFile, isEditable } from "@/lib/productivity/edit";
import { cn } from "@/lib/utils";

type SortKey = "name" | "modified" | "type" | "size";
type SortDir = "asc" | "desc";

const PAGE_SIZE = 10;

export default function Recent() {
  const { identity, refresh } = useAuth();
  const [page, setPage] = useState<RecentPage | null>(null);
  const [stats, setStats] = useState<FileStats | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [activeQuery, setActiveQuery] = useState("");
  const [searchResults, setSearchResults] = useState<FileRecord[] | null>(null);
  const [offset, setOffset] = useState(0);
  const [sortBy, setSortBy] = useState<SortKey>("modified");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [error, setError] = useState<string | null>(null);
  // Active file-type filter from the breakdown chips. typeFilter is an
  // extension, OTHER_EXT, or null; typeExclude is the chip list to exclude
  // when the catch-all "Other" chip is active.
  const [typeFilter, setTypeFilter] = useState<string | null>(null);
  const [typeExclude, setTypeExclude] = useState<string[]>([]);
  // Index into the visible rows for the preview overlay (null = closed).
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  const navigate = useNavigate();
  // Share target (file) and the rename/trash dialog, mirroring My Files.
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  // Single file targeted by the "Move" action (opens the destination picker).
  const [moveFile, setMoveFile] = useState<FileRecord | null>(null);
  // Workspace list for the move picker's workspace selector.
  const [workspaces, setWorkspaces] = useState<{ id: number; name: string }[]>([]);
  const [dialog, setDialog] = useState<
    | { kind: "rename-file"; file: FileRecord }
    | { kind: "trash-file"; file: FileRecord }
    | null
  >(null);
  // Bumped after a rename/move/trash so the list + stats re-fetch.
  const [refreshTick, setRefreshTick] = useState(0);
  const reload = () => setRefreshTick((t) => t + 1);

  // Recent page fetch (only when not searching). Refetched on offset OR
  // sort change, since sorting is now server-side across the full set.
  useEffect(() => {
    if (activeQuery.trim() !== "") return;
    let url = `/api/files/recent/page?offset=${offset}&limit=${PAGE_SIZE}&sort=${sortBy}&dir=${sortDir}`;
    if (typeFilter) {
      url += `&ext=${encodeURIComponent(typeFilter)}`;
      if (typeFilter === OTHER_EXT) url += `&exclude=${encodeURIComponent(typeExclude.join(","))}`;
    }
    void api
      .get<RecentPage>(url)
      .then(setPage)
      .catch((err) => {
        if (err instanceof ApiError) setError(err.message);
      });
  }, [offset, activeQuery, sortBy, sortDir, typeFilter, typeExclude, refreshTick]);

  // Stats fetched once on mount and refreshed when search clears (counts can
  // shift if you upload/delete from another tab).
  useEffect(() => {
    void api.get<FileStats>("/api/files/stats").then(setStats).catch(() => {});
  }, [refreshTick]);

  // Workspaces for the Move picker (loaded once).
  useEffect(() => {
    void api
      .get<WorkspacesResponse>("/api/workspaces")
      .then((r) => setWorkspaces((r.workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))))
      .catch(() => {});
  }, []);

  // Search fetch — replaces the recent list view while query is non-empty.
  useEffect(() => {
    if (activeQuery.trim() === "") {
      setSearchResults(null);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get<SearchResponse>(
          `/api/search?q=${encodeURIComponent(activeQuery)}`,
        );
        if (!cancelled) setSearchResults(res.files ?? []);
      } catch (err) {
        if (!cancelled && err instanceof ApiError) setError(err.message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [activeQuery, refreshTick]);

  const onSearchSubmit = (q: string) => {
    const trimmed = q.trim();
    setActiveQuery(trimmed);
    setOffset(0);
    // Searching and type-filtering are mutually exclusive.
    setTypeFilter(null);
  };

  // Chip click → filter the list to that type (or clear). Clears any active
  // search so the filtered recent list is what's shown.
  const onTypeSelect = (selection: string | null, topExts: string[]) => {
    setOffset(0);
    setTypeFilter(selection);
    setTypeExclude(topExts);
    setSearchInput("");
    setActiveQuery("");
    setSearchResults(null);
  };

  const clearSearch = () => {
    setSearchInput("");
    setActiveQuery("");
    setSearchResults(null);
  };

  const handleToggleStar = async (f: FileRecord) => {
    const next = !f.starred_at;
    try {
      const updated = await api.patch<FileRecord>(`/api/files/${f.id}`, { starred: next });
      // Patch returns the updated file; merge it back into whichever list is
      // currently being shown without refetching the whole page.
      const merge = (arr: FileRecord[]) =>
        arr.map((x) => (x.id === f.id ? { ...x, starred_at: updated.starred_at } : x));
      if (searchResults) setSearchResults(merge(searchResults));
      if (page) setPage({ ...page, files: merge(page.files) });
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  // Rename / move / move-to-trash — same actions and behaviour as My Files.
  const handleRenameFile = (f: FileRecord) => setDialog({ kind: "rename-file", file: f });
  const handleDeleteFile = (f: FileRecord) => setDialog({ kind: "trash-file", file: f });
  // Move opens the destination-picker modal (same one Files uses), not a prompt.
  const handleMoveFile = (f: FileRecord) => setMoveFile(f);

  const onSort = (key: SortKey) => {
    // Any sort change resets the pager back to page 1 — otherwise the
    // user could land on offset=20 of a list reordered to only have 7
    // entries above them.
    setOffset(0);
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(key);
      setSortDir(key === "name" || key === "type" ? "asc" : "desc");
    }
  };

  const inSearch = activeQuery.trim() !== "";
  const rows: FileRecord[] = inSearch ? searchResults ?? [] : page?.files ?? [];
  const total = inSearch ? rows.length : page?.total ?? 0;
  const totalPages = inSearch ? 1 : Math.max(1, Math.ceil(total / PAGE_SIZE));
  const currentPage = inSearch ? 1 : Math.floor(offset / PAGE_SIZE) + 1;

  // Server sorts across the whole active set; we render whatever order it
  // returned. (Search results stay in the server's created_at DESC order
  // since /api/search doesn't take sort params today.)
  const sortedRows = rows;

  // Initial-load skeleton — shown until the first recent-files page
  // arrives, so the layout doesn't pop in one element at a time.
  if (page === null && activeQuery.trim() === "") {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
          <div className="space-y-2">
            <Skeleton className="h-4 w-48" />
            <Skeleton className="h-8 w-32" />
          </div>
          <Skeleton className="h-14 w-full" />
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        <header className="flex items-center justify-between">
          <div>
            <p className="mb-1 text-sm text-muted-foreground">
              {identity?.tenant?.name ?? "Workspace"}
            </p>
            <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-foreground">
              <Clock className="h-6 w-6 text-primary" />
              Recent
            </h1>
          </div>
        </header>

        <InfoBanner storageKey="recent">
          <span className="font-medium text-foreground">Tip: </span>
          The latest uploads across this workspace, whatever the folder. The
          path under each name shows where a file lives — star one to pin it
          under Starred.
        </InfoBanner>

        {/* Summary tile — total + per-extension chips. Stays workspace-wide
            so users get the global picture even while searching. */}
        {stats && (
          <div className="rounded-lg border border-border bg-card p-4 shadow-sm">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-6">
              <div className="flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10">
                  <FilesIcon className="h-5 w-5 text-primary" />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Total files</p>
                  <p className="text-xl font-semibold tabular-nums text-foreground">
                    {stats.total}
                  </p>
                </div>
              </div>
              <FileTypeChips
                byExtension={stats.by_extension}
                active={typeFilter}
                onSelect={onTypeSelect}
              />
            </div>
          </div>
        )}

        {/* Search row — single h-7 input, uniform with My Files. */}
        <div className="flex items-center">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              onSearchSubmit(searchInput);
            }}
            className="w-full sm:w-72"
          >
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search files…"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                className="h-7 rounded-[11px] border border-border bg-card pl-10 pr-9 text-xs focus-visible:ring-1 focus-visible:ring-primary/40"
              />
              {(searchInput || inSearch) && (
                <button
                  type="button"
                  onClick={clearSearch}
                  className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"
                  title="Clear search"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          </form>
        </div>

        {error && (
          <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        <section className="space-y-4">
          {sortedRows.length === 0 ? (
            <div className="flex flex-col items-center justify-center rounded-xl border border-border bg-card py-16 text-center shadow-sm">
              <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-secondary">
                <FileIconGeneric className="h-8 w-8 text-muted-foreground" />
              </div>
              <h3 className="mb-1 text-lg font-semibold text-foreground">
                {inSearch ? "No matches" : "Nothing uploaded yet"}
              </h3>
              <p className="text-sm text-muted-foreground">
                {inSearch
                  ? "Nothing in this workspace matches your search."
                  : "Once you upload files anywhere in this workspace, the most recent ones will show up here."}
              </p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
              <table className="w-full text-sm">
                <thead className="sticky top-0 z-10 bg-card">
                  <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                    <SortableHeader
                      label="Name"
                      sortKey="name"
                      sortBy={sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                    />
                    <SortableHeader
                      label="Modified"
                      sortKey="modified"
                      sortBy={sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                      hideAt="sm"
                    />
                    <SortableHeader
                      label="Type"
                      sortKey="type"
                      sortBy={sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                      hideAt="md"
                    />
                    <SortableHeader
                      label="Size"
                      sortKey="size"
                      sortBy={sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                    />
                    <th className="w-[120px] px-4 py-2.5 text-right">Action</th>
                  </tr>
                </thead>
                <tbody>
                  <tr aria-hidden="true">
                    <td colSpan={5} className="h-3 p-0" />
                  </tr>
                  {sortedRows.map((f, fi) => {
                    return (
                      <ContextMenu key={f.id}>
                        <ContextMenuTrigger asChild>
                          <tr
                            draggable
                            onDragStart={(e) => setFileDownloadDrag(e, f)}
                            className="group cursor-grab transition-colors hover:bg-muted/40 active:cursor-grabbing"
                          >
                            <td className="px-4 py-1.5">
                              <button
                                type="button"
                                onClick={() => setPreviewIndex(fi)}
                                className="flex items-center gap-3 text-left"
                                title={`Preview ${f.name}`}
                              >
                                <FileThumb
                                  id={f.id}
                                  name={f.name}
                                  contentType={f.content_type}
                                  version={f.updated_at}
                                />
                                <div className="min-w-0">
                                  {f.path && (
                                    <div
                                      className="truncate text-[10px] text-muted-foreground"
                                      title={f.path}
                                    >
                                      {f.path}
                                    </div>
                                  )}
                                  <div className="inline-flex items-center gap-1.5 text-xs font-medium text-foreground hover:underline">
                                    {truncateName(f.name)}
                                    {f.starred_at && (
                                      <Star
                                        className="h-3 w-3 flex-shrink-0 fill-amber-300 text-amber-500"
                                        aria-label="Starred"
                                      />
                                    )}
                                  </div>
                                </div>
                              </button>
                            </td>
                            <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                              {formatDateTime(f.updated_at ?? f.created_at)}
                            </td>
                            <td className="hidden px-4 py-1.5 text-muted-foreground md:table-cell">
                              <span className="rounded-md bg-secondary px-2 py-1 text-xs">
                                {fileExtension(f.name) || "—"}
                              </span>
                            </td>
                            <td className="px-4 py-1.5 text-[12.5px] text-muted-foreground">
                              {formatBytes(f.size_bytes)}
                            </td>
                            <td className="px-4 py-1.5">
                              <div className="flex items-center justify-end gap-1">
                                <a href={downloadUrl(f.id)}>
                                  <Button
                                    variant="ghost"
                                    size="icon"
                                    className="h-8 w-8 text-muted-foreground hover:text-foreground"
                                    title="Download"
                                  >
                                    <Download className="h-4 w-4" />
                                  </Button>
                                </a>
                                <DropdownMenu>
                                  <DropdownMenuTrigger asChild>
                                    <Button
                                      variant="ghost"
                                      size="icon"
                                      className="h-8 w-8 text-muted-foreground hover:text-foreground"
                                      title="More"
                                    >
                                      <MoreHorizontal className="h-4 w-4" />
                                    </Button>
                                  </DropdownMenuTrigger>
                                  <DropdownMenuContent align="end" className="w-48">
                                    <DropdownMenuItem onSelect={() => setPreviewIndex(fi)}>
                                      <Eye className="mr-2 h-4 w-4" />
                                      Preview
                                    </DropdownMenuItem>
                                    {isEditable(f.name) && (
                                      <DropdownMenuItem onSelect={() => editFile(navigate, f.id, f.name)}>
                                        <Edit3 className="mr-2 h-4 w-4" />
                                        Edit
                                      </DropdownMenuItem>
                                    )}
                                    <DropdownMenuItem asChild>
                                      <a href={downloadUrl(f.id)}>
                                        <Download className="mr-2 h-4 w-4" />
                                        Download
                                      </a>
                                    </DropdownMenuItem>
                                    <DropdownMenuItem
                                      onSelect={() => setShareTarget({ kind: "file", id: f.id, name: f.name })}
                                    >
                                      <Share2 className="mr-2 h-4 w-4" />
                                      Share
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem onSelect={() => handleToggleStar(f)}>
                                      {f.starred_at ? (
                                        <>
                                          <StarOff className="mr-2 h-4 w-4" />
                                          Unstar
                                        </>
                                      ) : (
                                        <>
                                          <Star className="mr-2 h-4 w-4" />
                                          Star
                                        </>
                                      )}
                                    </DropdownMenuItem>
                                    <DropdownMenuItem onSelect={() => handleRenameFile(f)}>
                                      <Pencil className="mr-2 h-4 w-4" />
                                      Rename
                                    </DropdownMenuItem>
                                    <DropdownMenuItem onSelect={() => handleMoveFile(f)}>
                                      <Move className="mr-2 h-4 w-4" />
                                      Move
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem
                                      variant="destructive"
                                      onSelect={() => handleDeleteFile(f)}
                                    >
                                      <Trash2 className="mr-2 h-4 w-4" />
                                      Move to trash
                                    </DropdownMenuItem>
                                  </DropdownMenuContent>
                                </DropdownMenu>
                              </div>
                            </td>
                          </tr>
                        </ContextMenuTrigger>
                        <ContextMenuContent className="w-48">
                          <ContextMenuItem
                            onSelect={() => {
                              const url = printUrlFor({ id: f.id, name: f.name, content_type: f.content_type });
                              if (url) window.open(url, "_blank", "noopener");
                            }}
                          >
                            <Eye className="mr-2 h-4 w-4" /> Preview
                          </ContextMenuItem>
                          {isEditable(f.name) && (
                            <ContextMenuItem onSelect={() => editFile(navigate, f.id, f.name)}>
                              <Edit3 className="mr-2 h-4 w-4" /> Edit
                            </ContextMenuItem>
                          )}
                          <ContextMenuItem asChild>
                            <a href={downloadUrl(f.id)}>
                              <Download className="mr-2 h-4 w-4" /> Download
                            </a>
                          </ContextMenuItem>
                          <ContextMenuItem
                            onSelect={() => setShareTarget({ kind: "file", id: f.id, name: f.name })}
                          >
                            <Share2 className="mr-2 h-4 w-4" /> Share
                          </ContextMenuItem>
                          <ContextMenuSeparator />
                          <ContextMenuItem onSelect={() => handleToggleStar(f)}>
                            {f.starred_at ? (
                              <>
                                <StarOff className="mr-2 h-4 w-4" /> Unstar
                              </>
                            ) : (
                              <>
                                <Star className="mr-2 h-4 w-4" /> Star
                              </>
                            )}
                          </ContextMenuItem>
                          <ContextMenuItem onSelect={() => handleRenameFile(f)}>
                            <Pencil className="mr-2 h-4 w-4" /> Rename
                          </ContextMenuItem>
                          <ContextMenuItem onSelect={() => handleMoveFile(f)}>
                            <Move className="mr-2 h-4 w-4" /> Move
                          </ContextMenuItem>
                          <ContextMenuSeparator />
                          <ContextMenuItem variant="destructive" onSelect={() => handleDeleteFile(f)}>
                            <Trash2 className="mr-2 h-4 w-4" /> Move to trash
                          </ContextMenuItem>
                        </ContextMenuContent>
                      </ContextMenu>
                    );
                  })}
                  <tr aria-hidden="true">
                    <td colSpan={5} className="h-3 p-0" />
                  </tr>
                </tbody>
              </table>
            </div>
          )}

          {/* Pager — only when not searching and there are multiple pages. */}
          {!inSearch && totalPages > 1 && (
            <Paginator
              currentPage={currentPage}
              totalPages={totalPages}
              onJump={(p) => setOffset((p - 1) * PAGE_SIZE)}
            />
          )}
        </section>
      </div>

      {previewIndex !== null && sortedRows[previewIndex] && (
        <FilePreview
          files={sortedRows.map((f) => ({
            id: f.id,
            name: f.name,
            content_type: f.content_type,
            size_bytes: f.size_bytes,
          }))}
          index={previewIndex}
          onIndexChange={setPreviewIndex}
          onClose={() => setPreviewIndex(null)}
        />
      )}

      {shareTarget && (
        <ShareModal target={shareTarget} onClose={() => setShareTarget(null)} />
      )}

      {moveFile && (
        <MoveItemsModal
          fileCount={1}
          folderCount={0}
          workspaces={workspaces}
          currentWorkspaceId={null}
          currentFolderId={moveFile.parent_folder_id ?? null}
          selectedFolderIds={[]}
          onMove={async (targetFolderId, targetWorkspaceId) => {
            try {
              await api.patch(`/api/files/${moveFile.id}`, {
                folder_id: targetFolderId,
                workspace_id: targetWorkspaceId,
              });
              reload();
            } catch (err) {
              if (err instanceof ApiError) setError(err.message);
            }
          }}
          onClose={() => setMoveFile(null)}
        />
      )}

      {dialog?.kind === "rename-file" && (
        <NameDialog
          title="Rename file"
          label="File name"
          initialValue={dialog.file.name}
          confirmLabel="Rename"
          onSubmit={async (name) => {
            try {
              await api.patch(`/api/files/${dialog.file.id}`, { name });
            } catch (e) {
              if (e instanceof ApiError && e.status === 409)
                throw new Error(`A file named “${name}” already exists in this folder.`);
              throw e;
            }
            reload();
          }}
          onClose={() => setDialog(null)}
        />
      )}

      {dialog?.kind === "trash-file" && (
        <ConfirmDialog
          title="Move to trash?"
          danger
          confirmLabel="Move to trash"
          message={
            <>
              “{truncateName(dialog.file.name)}” will be moved to the trash. You can
              restore it later from the Trash bin.
            </>
          }
          onConfirm={async () => {
            await api.delete(`/api/files/${dialog.file.id}`);
            reload();
            await refresh({ silent: true });
            emitTrashChanged();
          }}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}

// Paginator renders a chevron + numbered pager that scales with the total
// page count. The numbered slots compress with "…" once there are too
// many pages to list inline. Once we cross JUMP_THRESHOLD, a small "Go to
// page" input also appears so users can jump deep without click-spamming.
const JUMP_THRESHOLD = 20;

function Paginator({
  currentPage,
  totalPages,
  onJump,
}: {
  currentPage: number;
  totalPages: number;
  onJump: (p: number) => void;
}) {
  const [jumpValue, setJumpValue] = useState("");

  // Page numbers to render. Rules:
  //  - ≤ 7 pages: show them all
  //  - otherwise: show first, last, current, current±1, with "…" gaps
  const pages = useMemo<(number | "…")[]>(() => {
    if (totalPages <= 7) {
      return Array.from({ length: totalPages }, (_, i) => i + 1);
    }
    const wanted = new Set<number>();
    wanted.add(1);
    wanted.add(totalPages);
    for (let p = currentPage - 1; p <= currentPage + 1; p++) {
      if (p >= 1 && p <= totalPages) wanted.add(p);
    }
    const sorted = [...wanted].sort((a, b) => a - b);
    const out: (number | "…")[] = [];
    sorted.forEach((p, i) => {
      if (i > 0 && p - sorted[i - 1] > 1) out.push("…");
      out.push(p);
    });
    return out;
  }, [currentPage, totalPages]);

  const submitJump = () => {
    const n = Number(jumpValue);
    if (Number.isFinite(n) && n >= 1 && n <= totalPages) {
      onJump(Math.floor(n));
      setJumpValue("");
    }
  };

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
      <span className="tabular-nums">
        <span className="font-medium text-foreground">{currentPage}</span> of {totalPages}
      </span>
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={currentPage === 1}
          onClick={() => onJump(currentPage - 1)}
          title="Previous page"
        >
          <ChevronLeft className="h-4 w-4" />
        </Button>
        {pages.map((p, i) =>
          p === "…" ? (
            <span
              key={`gap-${i}`}
              className="px-1 text-muted-foreground/70 select-none"
              aria-hidden="true"
            >
              …
            </span>
          ) : (
            <button
              key={p}
              type="button"
              onClick={() => onJump(p)}
              className={cn(
                "min-w-[28px] rounded px-2 py-1 text-xs tabular-nums transition-colors",
                p === currentPage
                  ? "bg-primary text-primary-foreground font-medium"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
              aria-current={p === currentPage ? "page" : undefined}
            >
              {p}
            </button>
          ),
        )}
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          disabled={currentPage >= totalPages}
          onClick={() => onJump(currentPage + 1)}
          title="Next page"
        >
          <ChevronRight className="h-4 w-4" />
        </Button>
        {totalPages > JUMP_THRESHOLD && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              submitJump();
            }}
            className="ml-2 flex items-center gap-1.5"
          >
            <label className="text-muted-foreground" htmlFor="page-jump">
              Go to
            </label>
            <input
              id="page-jump"
              type="number"
              inputMode="numeric"
              min={1}
              max={totalPages}
              value={jumpValue}
              onChange={(e) => setJumpValue(e.target.value)}
              placeholder="#"
              className="h-7 w-16 rounded-[5px] border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40"
            />
          </form>
        )}
      </div>
    </div>
  );
}

function SortableHeader<TKey extends string>({
  label,
  sortKey,
  sortBy,
  sortDir,
  onSort,
  hideAt,
}: {
  label: string;
  sortKey: TKey;
  sortBy: TKey;
  sortDir: "asc" | "desc";
  onSort: (k: TKey) => void;
  hideAt?: "sm" | "md";
}) {
  const isActive = sortBy === sortKey;
  const Arrow = isActive ? (sortDir === "asc" ? ChevronUp : ChevronDown) : null;
  return (
    <th
      onClick={() => onSort(sortKey)}
      className={cn(
        "cursor-pointer select-none px-4 py-2.5 text-left",
        hideAt === "sm" && "hidden sm:table-cell",
        hideAt === "md" && "hidden md:table-cell",
      )}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {Arrow && <Arrow className="h-3.5 w-3.5" />}
      </span>
    </th>
  );
}
