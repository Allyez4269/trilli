import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Download,
  Edit3,
  Eye,
  Folder,
  Inbox,
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
  type FolderRecord,
  type RecentPage,
  type SearchResponse,
  type WorkspacesResponse,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { FileTypeChips, OTHER_EXT } from "@/components/FileTypeChips";
import FilePreview, { printUrlFor } from "@/components/FilePreview";
import ShareModal, { type ShareTarget } from "@/components/ShareModal";
import PortalModal, { type PortalTarget } from "@/components/PortalModal";
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
import { filesPath } from "@/lib/ids";
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
const JUMP_THRESHOLD = 20;

export default function Starred() {
  const { identity, refresh } = useAuth();
  const navigate = useNavigate();
  const [page, setPage] = useState<RecentPage | null>(null);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [stats, setStats] = useState<FileStats | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [activeQuery, setActiveQuery] = useState("");
  const [searchResults, setSearchResults] = useState<FileRecord[] | null>(null);
  const [offset, setOffset] = useState(0);
  // Default sort is "starred_at desc" on the server; we still let users
  // re-sort by name/modified/type/size from the column headers.
  const [sortBy, setSortBy] = useState<SortKey | "starred">("starred");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  const [error, setError] = useState<string | null>(null);
  // Bumps every time we star/unstar so the page + stats re-fetch.
  const [refreshTick, setRefreshTick] = useState(0);
  // Active file-type filter from the breakdown chips (extension, OTHER_EXT, or
  // null). typeExclude is the chip list to exclude when "Other" is active.
  const [typeFilter, setTypeFilter] = useState<string | null>(null);
  const [typeExclude, setTypeExclude] = useState<string[]>([]);
  // Index into the visible file rows for the preview overlay (null = closed).
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  // Share / drop-portal targets and the rename/trash dialog — mirroring My Files.
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [portalTarget, setPortalTarget] = useState<PortalTarget | null>(null);
  // Single file targeted by the "Move" action (opens the destination picker).
  const [moveFile, setMoveFile] = useState<FileRecord | null>(null);
  const [workspaces, setWorkspaces] = useState<{ id: number; name: string }[]>([]);
  const [dialog, setDialog] = useState<
    | { kind: "rename-file"; file: FileRecord }
    | { kind: "trash-file"; file: FileRecord }
    | { kind: "rename-folder"; folder: FolderRecord }
    | { kind: "trash-folder"; folder: FolderRecord }
    | null
  >(null);
  const reload = () => setRefreshTick((t) => t + 1);

  useEffect(() => {
    if (activeQuery.trim() !== "") return;
    const sortParam = sortBy === "starred" ? "" : `&sort=${sortBy}&dir=${sortDir}`;
    let url = `/api/files/starred/page?offset=${offset}&limit=${PAGE_SIZE}${sortParam}`;
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
  }, [offset, activeQuery, sortBy, sortDir, refreshTick, typeFilter, typeExclude]);

  useEffect(() => {
    void api.get<FileStats>("/api/files/starred/stats").then(setStats).catch(() => {});
  }, [refreshTick]);

  // Starred folders (not paginated — there are rarely many).
  useEffect(() => {
    void api
      .get<{ folders: FolderRecord[] }>("/api/folders/starred")
      .then((r) => setFolders(r.folders ?? []))
      .catch(() => {});
  }, [refreshTick]);

  // Workspaces for the Move picker (loaded once).
  useEffect(() => {
    void api
      .get<WorkspacesResponse>("/api/workspaces")
      .then((r) => setWorkspaces((r.workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))))
      .catch(() => {});
  }, []);

  // Search is workspace-wide via /api/search, then filtered down to starred
  // hits on the client so users can find a specific starred file by name.
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
        if (!cancelled) {
          setSearchResults((res.files ?? []).filter((f) => !!f.starred_at));
        }
      } catch (err) {
        if (!cancelled && err instanceof ApiError) setError(err.message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [activeQuery, refreshTick]);

  const onSearchSubmit = (q: string) => {
    setActiveQuery(q.trim());
    setOffset(0);
    setTypeFilter(null);
  };
  const clearSearch = () => {
    setSearchInput("");
    setActiveQuery("");
    setSearchResults(null);
  };

  // Chip click → filter the starred list to that type (or clear), exiting any
  // active search.
  const onTypeSelect = (selection: string | null, topExts: string[]) => {
    setOffset(0);
    setTypeFilter(selection);
    setTypeExclude(topExts);
    setSearchInput("");
    setActiveQuery("");
    setSearchResults(null);
  };

  const onSort = (key: SortKey) => {
    setOffset(0);
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(key);
      setSortDir(key === "name" || key === "type" ? "asc" : "desc");
    }
  };

  const handleToggleStar = async (f: FileRecord) => {
    try {
      await api.patch(`/api/files/${f.id}`, { starred: !f.starred_at });
      reload();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  // Rename / move / move-to-trash — same actions and behaviour as My Files.
  const handleRenameFile = (f: FileRecord) => setDialog({ kind: "rename-file", file: f });
  const handleDeleteFile = (f: FileRecord) => setDialog({ kind: "trash-file", file: f });
  const handleRenameFolder = (d: FolderRecord) => setDialog({ kind: "rename-folder", folder: d });
  const handleDeleteFolder = (d: FolderRecord) => setDialog({ kind: "trash-folder", folder: d });
  // Move opens the destination-picker modal (same one Files uses), not a prompt.
  const handleMoveFile = (f: FileRecord) => setMoveFile(f);

  const handleUnstarFolder = async (folderId: number) => {
    try {
      await api.patch(`/api/folders/${folderId}`, { starred: false });
      setRefreshTick((t) => t + 1);
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  const openFolder = (f: FolderRecord) => {
    navigate(filesPath(f.workspace_id ?? null, f.id));
  };

  const inSearch = activeQuery.trim() !== "";
  const rows: FileRecord[] = inSearch ? searchResults ?? [] : page?.files ?? [];
  const total = inSearch ? rows.length : page?.total ?? 0;
  const totalPages = inSearch ? 1 : Math.max(1, Math.ceil(total / PAGE_SIZE));
  const currentPage = inSearch ? 1 : Math.floor(offset / PAGE_SIZE) + 1;
  // Starred folders sit inline at the top of the same table as files (only on
  // the first page, never while searching, and never while a file-type filter
  // is active — folders aren't a file type).
  const showFolders =
    !inSearch && !typeFilter && currentPage === 1 && folders.length > 0;

  // Initial-load skeleton.
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
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        <header>
          <p className="mb-1 text-sm text-muted-foreground">
            {identity?.tenant?.name ?? "Workspace"}
          </p>
          <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-foreground">
            <Star className="h-6 w-6 fill-amber-300 text-amber-500" />
            Starred
          </h1>
        </header>

        {stats && stats.total > 0 && (
          <InfoBanner storageKey="starred">
            <span className="font-medium text-foreground">Tip: </span>
            Starred files are pinned here for one-click access. Unstar a row
            to remove it from this list — the file itself stays put.
          </InfoBanner>
        )}

        {/* Summary tile — workspace-wide starred breakdown. Hidden when
            there are no starred files (the empty state takes over). */}
        {stats && stats.total > 0 && (
          <div className="rounded-lg border border-border bg-card p-4 shadow-sm">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-6">
              <div className="flex items-center gap-3">
                <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-amber-400/15">
                  <Star className="h-5 w-5 fill-amber-300 text-amber-500" />
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Total starred</p>
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

        {/* Search row (hidden when there's nothing to search) */}
        {(stats?.total ?? 0) > 0 && (
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
                  placeholder="Search starred files…"
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
        )}

        {error && (
          <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        <section className="space-y-4">
          {rows.length === 0 && !showFolders ? (
            <div className="flex flex-col items-center justify-center rounded-xl border border-border bg-card py-16 text-center shadow-sm">
              <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-amber-400/15">
                <Star className="h-8 w-8 fill-amber-300 text-amber-500" />
              </div>
              <h3 className="mb-1 text-lg font-semibold text-foreground">
                {inSearch ? "No starred matches" : "Nothing starred yet"}
              </h3>
              <p className="max-w-md text-sm text-muted-foreground">
                {inSearch
                  ? "None of your starred files match this search."
                  : "Open the … menu on any file or folder in Files and choose Star. Pinned items will show up here for one-click access."}
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
                      sortBy={sortBy === "starred" ? null : sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                    />
                    <SortableHeader
                      label="Modified"
                      sortKey="modified"
                      sortBy={sortBy === "starred" ? null : sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                      hideAt="sm"
                    />
                    <SortableHeader
                      label="Type"
                      sortKey="type"
                      sortBy={sortBy === "starred" ? null : sortBy}
                      sortDir={sortDir}
                      onSort={onSort}
                      hideAt="md"
                    />
                    <SortableHeader
                      label="Size"
                      sortKey="size"
                      sortBy={sortBy === "starred" ? null : sortBy}
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
                  {showFolders &&
                    folders.map((d) => (
                      <ContextMenu key={`folder-${d.id}`}>
                        <ContextMenuTrigger asChild>
                          <tr className="group transition-colors hover:bg-muted/40">
                            <td className="px-4 py-1.5">
                              <button
                                type="button"
                                onClick={() => openFolder(d)}
                                className="flex items-center gap-3 text-left"
                                title={`Open ${d.name}`}
                              >
                                {/* Same 32px leading tile as file rows (FileThumb),
                                    so folder and file names align in one column. */}
                                <span className="flex h-8 w-8 flex-shrink-0 items-center justify-center">
                                  <Folder className="h-5 w-5 fill-amber-300 text-amber-500" />
                                </span>
                                <div className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                                  {truncateName(d.name)}
                                  <Star className="h-3 w-3 flex-shrink-0 fill-amber-300 text-amber-500" />
                                </div>
                              </button>
                            </td>
                            <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                              {d.starred_at ? formatDateTime(d.starred_at) : "—"}
                            </td>
                            <td className="hidden px-4 py-1.5 text-muted-foreground md:table-cell">
                              <span className="rounded-md bg-secondary px-2 py-1 text-xs">
                                Folder
                              </span>
                            </td>
                            <td className="px-4 py-1.5 text-[12.5px] text-muted-foreground">
                              {d.item_count} item{d.item_count === 1 ? "" : "s"}
                            </td>
                            <td className="px-4 py-1.5">
                              <div className="flex items-center justify-end gap-1">
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="h-8 w-8 text-muted-foreground hover:text-foreground"
                                  title="Open"
                                  onClick={() => openFolder(d)}
                                >
                                  <ChevronRight className="h-4 w-4" />
                                </Button>
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
                                    <DropdownMenuItem onSelect={() => openFolder(d)}>
                                      <Folder className="mr-2 h-4 w-4" />
                                      Open folder
                                    </DropdownMenuItem>
                                    <DropdownMenuItem
                                      onSelect={() => setShareTarget({ kind: "folder", id: d.id, name: d.name })}
                                    >
                                      <Share2 className="mr-2 h-4 w-4" />
                                      Share
                                    </DropdownMenuItem>
                                    <DropdownMenuItem
                                      onSelect={() => setPortalTarget({ folderId: d.id, folderName: d.name })}
                                    >
                                      <Inbox className="mr-2 h-4 w-4" />
                                      Request files
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem onSelect={() => handleUnstarFolder(d.id)}>
                                      <StarOff className="mr-2 h-4 w-4" />
                                      Unstar
                                    </DropdownMenuItem>
                                    <DropdownMenuItem onSelect={() => handleRenameFolder(d)}>
                                      <Pencil className="mr-2 h-4 w-4" />
                                      Rename
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                    <DropdownMenuItem
                                      variant="destructive"
                                      onSelect={() => handleDeleteFolder(d)}
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
                          <ContextMenuItem onSelect={() => openFolder(d)}>
                            <Folder className="mr-2 h-4 w-4" /> Open folder
                          </ContextMenuItem>
                          <ContextMenuItem
                            onSelect={() => setShareTarget({ kind: "folder", id: d.id, name: d.name })}
                          >
                            <Share2 className="mr-2 h-4 w-4" /> Share
                          </ContextMenuItem>
                          <ContextMenuItem
                            onSelect={() => setPortalTarget({ folderId: d.id, folderName: d.name })}
                          >
                            <Inbox className="mr-2 h-4 w-4" /> Request files
                          </ContextMenuItem>
                          <ContextMenuSeparator />
                          <ContextMenuItem onSelect={() => handleUnstarFolder(d.id)}>
                            <StarOff className="mr-2 h-4 w-4" /> Unstar
                          </ContextMenuItem>
                          <ContextMenuItem onSelect={() => handleRenameFolder(d)}>
                            <Pencil className="mr-2 h-4 w-4" /> Rename
                          </ContextMenuItem>
                          <ContextMenuSeparator />
                          <ContextMenuItem variant="destructive" onSelect={() => handleDeleteFolder(d)}>
                            <Trash2 className="mr-2 h-4 w-4" /> Move to trash
                          </ContextMenuItem>
                        </ContextMenuContent>
                      </ContextMenu>
                    ))}
                  {rows.map((f, fi) => {
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
                                  <div className="flex items-center gap-1.5 text-xs font-medium text-foreground hover:underline">
                                    {truncateName(f.name)}
                                    <Star className="h-3 w-3 flex-shrink-0 fill-amber-300 text-amber-500" />
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

          {!inSearch && totalPages > 1 && (
            <Paginator
              currentPage={currentPage}
              totalPages={totalPages}
              onJump={(p) => setOffset((p - 1) * PAGE_SIZE)}
            />
          )}
        </section>
      </div>

      {previewIndex !== null && rows[previewIndex] && (
        <FilePreview
          files={rows.map((f) => ({
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

      {portalTarget && (
        <PortalModal
          target={portalTarget}
          onClose={() => setPortalTarget(null)}
        />
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

      {dialog?.kind === "rename-folder" && (
        <NameDialog
          title="Rename folder"
          label="Folder name"
          initialValue={dialog.folder.name}
          confirmLabel="Rename"
          onSubmit={async (name) => {
            try {
              await api.patch(`/api/folders/${dialog.folder.id}`, { name });
            } catch (e) {
              if (e instanceof ApiError && e.status === 409)
                throw new Error(`A folder named “${name}” already exists here.`);
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

      {dialog?.kind === "trash-folder" && (
        <ConfirmDialog
          title="Move folder to trash?"
          danger
          confirmLabel="Move to trash"
          message={
            <>
              “{truncateName(dialog.folder.name)}” and everything inside it will be
              moved to the trash. You can restore the folder later.
            </>
          }
          onConfirm={async () => {
            await api.delete(`/api/folders/${dialog.folder.id}`);
            reload();
            emitTrashChanged();
          }}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}

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
            <label className="text-muted-foreground" htmlFor="page-jump-starred">
              Go to
            </label>
            <input
              id="page-jump-starred"
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
  sortBy: TKey | null;
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
