import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  ClipboardPaste,
  Copy,
  CopyPlus,
  Download,
  Edit3,
  Eye,
  File as FileIconGeneric,
  FileText,
  FileUp,
  Folder,
  FolderPlus,
  FolderUp,
  Loader2,
  FolderRoot,
  Layers,
  Move,
  Inbox,
  MoreHorizontal,
  Pencil,
  Plus,
  Printer,
  Search,
  Share2,
  Star,
  StarOff,
  Trash2,
  Upload,
  X,
  Lock,
} from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import {
  api,
  ApiError,
  downloadUrl,
  type AccountQuota,
  type BrowseResponse,
  type FileRecord,
  type FolderRecord,
  type Quota,
  type SearchResponse,
  type UploadProgress,
  type Workspace,
  type WorkspacesResponse,
} from "@/lib/api";
import { Paginator } from "@/components/Paginator";
import { WorkspaceCreateModal } from "@/components/WorkspaceCreateModal";
import ShareModal, { type ShareTarget } from "@/components/ShareModal";
import PortalModal, { type PortalTarget } from "@/components/PortalModal";
import MoveItemsModal from "@/components/SelectionActionsModal";
import CopyToAccountModal, { type CopyAccount } from "@/components/CopyToAccountModal";
import MoveConflictModal from "@/components/MoveConflictModal";
import { NameDialog, ConfirmDialog } from "@/components/Dialogs";
import FilePreview, { canPreview, canPrint, printUrlFor } from "@/components/FilePreview";
import { printDocument } from "@/lib/print";
import { Button } from "@/components/ui/button";
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
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { UploadTips } from "@/components/UploadTips";
import { PageHeader } from "@/components/PageHeader";
import { FileDropOverlay } from "@/components/FileDropOverlay";
import { CloudImportButton } from "@/components/cloudimport/CloudImportButton";
import { CloudImportModal } from "@/components/cloudimport/CloudImportModal";
import FileThumb from "@/components/FileThumb";
import {
  fileExtension,
  formatBytes,
  formatDateTime,
  truncateName,
} from "@/lib/files-meta";
import { editFile, isEditable } from "@/lib/productivity/edit";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/Checkbox";
import { TRILLI_DRAG_MIME, emitTrashChanged, emitStorageChanged, onItemsTrashed } from "@/lib/events";
import { setFileDownloadDrag, setFolderDownloadDrag } from "@/lib/drag";
import { decodeId, filesPath } from "@/lib/ids";

// Mirrors the selected workspace so it survives navigation away from /files
// (the sidebar link carries no query string).
const WS_STORAGE_KEY = "trilli.lastWorkspaceId";

// The list container shows ~8 rows then scrolls; once a folder holds more than
// one page worth of items, a paginator splits them into pages.
const FILES_PAGE_SIZE = 50;

export default function Files() {
  const { identity, refresh } = useAuth();
  const navigate = useNavigate();
  // Workspace + folder live in opaque URL path tokens (/files/w/<ws>/f/<folder>);
  // only the search term ?q stays in the query string. The API still speaks
  // integer ids, so we decode the tokens here at the boundary and everything
  // downstream is unchanged.
  const { wsToken, folderToken } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const folderId = decodeId(folderToken);
  const initialQuery = searchParams.get("q") ?? "";

  const [browse, setBrowse] = useState<BrowseResponse | null>(null);
  const [searchResults, setSearchResults] = useState<FileRecord[] | null>(null);
  const [searchInput, setSearchInput] = useState(initialQuery);
  const [activeQuery, setActiveQuery] = useState(initialQuery);
  const [quota, setQuota] = useState<Quota | null>(null);
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [portalTarget, setPortalTarget] = useState<PortalTarget | null>(null);
  // Single file targeted by the "Move" row action (opens the picker modal).
  const [moveFile, setMoveFile] = useState<FileRecord | null>(null);
  const [moveFolder, setMoveFolder] = useState<FolderRecord | null>(null);
  // Items dropped onto the workspace selector — opens the move picker seeded
  // with them (so the user can move into another workspace/folder).
  const [dragMove, setDragMove] = useState<{ fileIds: number[]; folderIds: number[] } | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [cloudImportOpen, setCloudImportOpen] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null);
  // Pre-upload phase for folder drops: the modal appears the instant the drop
  // lands, narrating the scan + folder creation before the first byte moves.
  const [treeStats, setTreeStats] = useState<{ folders: number; files: number } | null>(null);
  const [uploadPrep, setUploadPrep] = useState<
    { phase: "scanning" | "folders"; files: number; folders: number; foldersDone: number } | null
  >(null);
  // Move progress is local: per-item PATCH calls don't have byte progress
  // like uploads do, so we just track item index + name as we iterate.
  const [moveProgress, setMoveProgress] = useState<{
    itemIndex: number;
    totalItems: number;
    itemName: string;
  } | null>(null);
  // Queue of unresolved name collisions from a move — drives the conflict modal.
  const [moveConflicts, setMoveConflicts] = useState<{
    queue: { file: FileRecord; suggested: string }[];
    idx: number;
    targetFolderId: number | null;
    wsPayload: Record<string, number>;
    destLabel?: string;
  } | null>(null);
  // Same, for uploads (drag-in / pick) that clash with a file in the folder.
  const [uploadConflicts, setUploadConflicts] = useState<{
    queue: { file: File; suggested: string }[];
    idx: number;
    destLabel?: string;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedFiles, setSelectedFiles] = useState<number[]>([]);
  const [selectedFolders, setSelectedFolders] = useState<number[]>([]);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const dirInputRef = useRef<HTMLInputElement | null>(null);

  // ----- Rubber-band (marquee) drag-select -------------------------------
  // Press on blank space and drag to draw a selection box; rows it touches
  // are selected. Ctrl/Cmd-drag ADDS to the current selection; a plain drag
  // replaces it; a plain click on blank space clears it. Anchors live in the
  // scroller's CONTENT coordinates so the box stays glued to rows while the
  // list auto-scrolls at the edges; the box itself renders position:fixed in
  // a portal (clamped to the scroller) so no transformed ancestor offsets it.
  const scrollerRef = useRef<HTMLDivElement | null>(null);
  const marqueeRef = useRef<{
    anchorX: number;
    anchorY: number;
    additive: boolean;
    baseFiles: number[];
    baseFolders: number[];
    lastX: number;
    lastY: number;
    moved: boolean;
  } | null>(null);
  const [marqueeBox, setMarqueeBox] = useState<{
    left: number;
    top: number;
    width: number;
    height: number;
  } | null>(null);

  const onMarqueeMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    // Only blank space starts a marquee — rows keep their move-drag, and
    // controls/menus keep their clicks.
    const t = e.target as HTMLElement;
    if (t.closest("tr, button, input, a, select, textarea, label, [role='dialog'], [data-no-marquee]")) return;
    const sc = scrollerRef.current;
    if (!sc) return;

    const rect = sc.getBoundingClientRect();
    const additive = e.metaKey || e.ctrlKey;
    marqueeRef.current = {
      anchorX: e.clientX - rect.left + sc.scrollLeft,
      anchorY: e.clientY - rect.top + sc.scrollTop,
      additive,
      baseFiles: additive ? selectedFiles : [],
      baseFolders: additive ? selectedFolders : [],
      lastX: e.clientX,
      lastY: e.clientY,
      moved: false,
    };

    const update = (cx: number, cy: number) => {
      const st = marqueeRef.current;
      if (!st || !sc) return;
      st.lastX = cx;
      st.lastY = cy;
      const r = sc.getBoundingClientRect();

      // Edge auto-scroll keeps the band usable on long listings.
      if (cy > r.bottom - 36) sc.scrollTop += Math.min(24, cy - (r.bottom - 36));
      else if (cy < r.top + 36) sc.scrollTop -= Math.min(24, r.top + 36 - cy);

      const curX = cx - r.left + sc.scrollLeft;
      const curY = cy - r.top + sc.scrollTop;
      const x1 = Math.min(st.anchorX, curX);
      const x2 = Math.max(st.anchorX, curX);
      const y1 = Math.min(st.anchorY, curY);
      const y2 = Math.max(st.anchorY, curY);
      if (!st.moved && x2 - x1 + (y2 - y1) > 6) st.moved = true;
      if (!st.moved) return;

      // Render rect (viewport coords), clamped to the scroller's box.
      const vLeft = Math.max(x1 - sc.scrollLeft + r.left, r.left);
      const vTop = Math.max(y1 - sc.scrollTop + r.top, r.top);
      const vRight = Math.min(x2 - sc.scrollLeft + r.left, r.right);
      const vBottom = Math.min(y2 - sc.scrollTop + r.top, r.bottom);
      setMarqueeBox({
        left: vLeft,
        top: vTop,
        width: Math.max(0, vRight - vLeft),
        height: Math.max(0, vBottom - vTop),
      });

      // Hit-test every selectable row against the band (content coords).
      const hitFiles: number[] = [];
      const hitFolders: number[] = [];
      sc.querySelectorAll<HTMLElement>("[data-selid]").forEach((el) => {
        const b = el.getBoundingClientRect();
        const top = b.top - r.top + sc.scrollTop;
        const left = b.left - r.left + sc.scrollLeft;
        if (top + b.height >= y1 && top <= y2 && left + b.width >= x1 && left <= x2) {
          const id = el.dataset.selid!;
          if (id.startsWith("f")) hitFiles.push(Number(id.slice(1)));
          else hitFolders.push(Number(id.slice(1)));
        }
      });
      setSelectedFiles(Array.from(new Set([...st.baseFiles, ...hitFiles])));
      setSelectedFolders(Array.from(new Set([...st.baseFolders, ...hitFolders])));
    };

    const onScroll = () => {
      const st = marqueeRef.current;
      if (st) update(st.lastX, st.lastY);
    };
    const onMove = (ev: MouseEvent) => update(ev.clientX, ev.clientY);
    const onUp = () => {
      window.removeEventListener("mousemove", onMove);
      sc.removeEventListener("scroll", onScroll);
      const st = marqueeRef.current;
      marqueeRef.current = null;
      setMarqueeBox(null);
      // A motionless press on blank space is a plain background click —
      // clear the selection (unless they were holding the add modifier).
      if (st && !st.moved && !st.additive) {
        setSelectedFiles([]);
        setSelectedFolders([]);
      }
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp, { once: true });
    sc.addEventListener("scroll", onScroll);
    e.preventDefault(); // stop text selection while banding
  };

  // Sorting state — folders always cluster above files; sort is applied within
  // each group.
  type SortKey = "name" | "modified" | "type" | "size";
  type SortDir = "asc" | "desc";
  const [sortBy, setSortBy] = useState<SortKey>("modified");
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  // Zero-based page index into the combined (folders-then-files) list.
  const [page, setPage] = useState(0);

  const onSort = (key: SortKey) => {
    if (sortBy === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(key);
      // Names feel right ascending; dates and sizes feel right descending.
      setSortDir(key === "name" || key === "type" ? "asc" : "desc");
    }
  };

  // Internal drag-and-drop state (file/folder rows → folder rows / breadcrumb).
  // A custom MIME type distinguishes our drags from OS-file uploads.
  const [draggingFileIds, setDraggingFileIds] = useState<number[] | null>(null);
  const [draggingFolderIds, setDraggingFolderIds] = useState<number[] | null>(null);
  const [dragOverFolderId, setDragOverFolderId] = useState<number | null>(null);
  // Which breadcrumb crumb is the current drop target ("root" = workspace root).
  const [dragOverCrumb, setDragOverCrumb] = useState<number | "root" | null>(null);
  // Highlight when an internal drag is hovering the workspace selector.
  const [dragOverWs, setDragOverWs] = useState(false);

  // Role flags drive the workspace picker. Owner/admin manage every workspace
  // (create/edit); members/viewers ("scoped") only see the workspaces they're
  // assigned to and can't create/edit them. Viewers are read-only.
  const role = (identity?.membership?.role ?? "").toLowerCase();
  const isAdminOrOwner = role === "owner" || role === "admin";
  const isViewer = role === "viewer";
  const scoped = !isAdminOrOwner;
  // A lapsed account (subscription ended) is read-only for everyone until they
  // reactivate — mirror the server's RequireAuth write-guard so the upload /
  // new-folder controls don't offer actions that would just 403.
  const isLapsed = identity?.tenant?.lifecycle_state === "lapsed";
  const canWrite = !isViewer && !isLapsed; // owner/admin/member may create + upload

  // Workspaces (the Account → Workspace layer). Everyone picks an active
  // workspace; the selection lives in the URL path token (/files/w/<token>) so
  // browse scopes to it. For members/viewers the list is already limited server-side to the
  // workspaces they're assigned to.
  const [workspaces, setWorkspaces] = useState<Workspace[] | null>(null);
  const [accountQuota, setAccountQuota] = useState<AccountQuota | null>(null);
  const [wsModalOpen, setWsModalOpen] = useState(false);
  // Open state for the "Move to…" destination browser (launched from the
  // inline selection bar). All other bulk actions act inline.
  const [moveOpen, setMoveOpen] = useState(false);
  // Open state for the "Copy to another account" modal (cross-account copy).
  const [copyOpen, setCopyOpen] = useState(false);
  // In-app clipboard for same-account Copy/Paste. Survives folder navigation
  // (this page component stays mounted) so you can Copy here and Paste there.
  // It's in-memory only, so a logout / session end clears it automatically.
  const [clipboard, setClipboard] = useState<{ fileIds: number[]; folderIds: number[] } | null>(null);
  // Paste status modal (working / done / error) — mirrors the upload indicator.
  const [pasteStatus, setPasteStatus] = useState<
    { phase: "working" | "done" | "error"; files: number; folders: number; error?: string } | null
  >(null);
  // Sanitize the clipboard: auto-clear 15 minutes after a copy. Resets on each
  // new copy; also cleared the moment something is pasted.
  useEffect(() => {
    if (!clipboard) return;
    const t = window.setTimeout(() => setClipboard(null), 15 * 60 * 1000);
    return () => window.clearTimeout(t);
  }, [clipboard]);
  // Index into the current page's files for the preview overlay (null = closed).
  const [previewIndex, setPreviewIndex] = useState<number | null>(null);
  // Right-click → Edit on txt/json opens the preview modal straight in its editor.
  const [previewEdit, setPreviewEdit] = useState(false);
  const openPreview = (fi: number, edit = false) => {
    setPreviewEdit(edit);
    setPreviewIndex(fi);
  };
  // In-modal saves flow back here so the row's size/updated refresh live.
  const handlePreviewSaved = (rec: { id: number; size_bytes: number; updated_at?: string; content_type?: string }) =>
    setBrowse((prev) =>
      prev
        ? {
            ...prev,
            files: (prev.files ?? []).map((x) =>
              x.id === rec.id
                ? { ...x, size_bytes: rec.size_bytes, updated_at: rec.updated_at ?? x.updated_at, content_type: rec.content_type ?? x.content_type }
                : x,
            ),
          }
        : prev,
    );
  // Which styled dialog (new folder / rename / trash confirm) is open, if any.
  const [dialog, setDialog] = useState<
    | { kind: "new-folder" }
    | { kind: "rename-file"; file: FileRecord }
    | { kind: "rename-folder"; folder: FolderRecord }
    | { kind: "duplicate-file"; file: FileRecord }
    | { kind: "duplicate-folder"; folder: FolderRecord }
    | { kind: "trash-file"; file: FileRecord }
    | { kind: "trash-folder"; folder: FolderRecord }
    | { kind: "trash-bulk"; count: number }
    | null
  >(null);
  const [wsEditing, setWsEditing] = useState<Workspace | null>(null);
  // Workspace switcher menu filter query.
  const [wsMenuQuery, setWsMenuQuery] = useState("");
  // The selection lives in the URL path (/files/w/<token>) AND a localStorage
  // mirror. The sidebar "My Files" link is a bare /files (no token), so the URL
  // alone is wiped on navigation; the mirror lets us restore the last workspace
  // synchronously on mount — no flash of the wrong/default workspace.
  const urlWsId = decodeId(wsToken);
  const storedWs =
    typeof window !== "undefined" ? window.localStorage.getItem(WS_STORAGE_KEY) : null;
  // Workspace comes from the path token; falling back to the stored mirror when
  // the path carries none (bare /files, or a folder-only /files/f/<token> deep
  // link). The folder still loads via its own id regardless of which workspace
  // is ambient — browse keys off folder_id when present.
  // Ignore a stored mirror that the loaded workspace list doesn't contain —
  // e.g. a leftover from another account after a tenant switch.
  const storedWsId = storedWs ? Number(storedWs) : null;
  const storedWsValid =
    storedWsId != null && (workspaces == null || workspaces.some((w) => w.id === storedWsId));
  const currentWorkspaceId = urlWsId != null ? urlWsId : storedWsValid ? storedWsId : null;
  const currentWorkspace = workspaces?.find((w) => w.id === currentWorkspaceId) ?? null;

  // Monotonic request id so an earlier in-flight browse can't overwrite a later
  // one (e.g. switching workspaces quickly) — only the newest response wins.
  const reloadSeq = useRef(0);
  const reload = useCallback(async () => {
    const seq = ++reloadSeq.current;
    try {
      const qs = new URLSearchParams();
      if (folderId != null) qs.set("folder_id", String(folderId));
      if (currentWorkspaceId != null) qs.set("workspace_id", String(currentWorkspaceId));
      const suffix = qs.toString() ? `?${qs.toString()}` : "";
      const [b, q] = await Promise.all([
        api.get<BrowseResponse>(`/api/browse${suffix}`),
        api.get<Quota>("/api/files/quota"),
      ]);
      if (seq !== reloadSeq.current) return; // superseded by a newer reload
      setBrowse(b);
      setQuota(q);
      setError(null);
      // Tell the sidebar storage widget to refresh too — uploads/deletes/purges
      // all funnel through reload(), so its meter stays in sync immediately
      // instead of only updating on the next navigation.
      emitStorageChanged();
    } catch (err) {
      if (seq !== reloadSeq.current) return;
      console.error("reload failed", err);
      if (err instanceof ApiError) setError(err.message);
    }
  }, [folderId, currentWorkspaceId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // When items are trashed from outside this view (dragged onto the sidebar
  // Trash bin), reload so the now-trashed rows disappear and the storage figure
  // updates.
  useEffect(
    () =>
      onItemsTrashed(() => {
        void reload();
        void refresh({ silent: true });
      }),
    [reload, refresh],
  );

  // Switching workspaces: blank the old workspace's contents immediately so the
  // table shows the skeleton (not the previous workspace's folders) while the
  // new browse is in flight. Folder navigation within a workspace is left
  // untouched so it stays smooth.
  useEffect(() => {
    setBrowse(null);
  }, [currentWorkspaceId]);

  // Load the workspaces the caller may use (server-scoped: all for owner/admin,
  // the assigned set for members/viewers) plus the account storage pool.
  const loadWorkspaces = useCallback(async () => {
    try {
      const r = await api.get<WorkspacesResponse>("/api/workspaces");
      setWorkspaces(r.workspaces);
      setAccountQuota(r.account ?? null);
    } catch {
      setWorkspaces((prev) => prev ?? []);
    }
  }, []);

  useEffect(() => {
    void loadWorkspaces();
  }, [loadWorkspaces]);

  // Resolve the active workspace once the list loads: keep the current one if
  // it's still accessible (from URL or the localStorage mirror), otherwise fall
  // back to the first. Then make sure the URL + mirror both carry it so browse
  // is scoped from the very first render and the choice survives navigation.
  useEffect(() => {
    if (!workspaces || workspaces.length === 0) return;
    // Folder-only deep link (/files/f/<token>): never force a workspace token
    // into the path — that would redirect away from the folder. The folder
    // loads via its own id; the stored workspace stays ambient context.
    if (urlWsId == null && folderId != null) return;
    const accessible =
      currentWorkspaceId != null && workspaces.some((w) => w.id === currentWorkspaceId);
    const target = accessible ? currentWorkspaceId! : workspaces[0].id;
    if (urlWsId !== target) {
      // Put the workspace token in the path; keep any active search term.
      const search = searchParams.toString();
      navigate(filesPath(target) + (search ? `?${search}` : ""), { replace: true });
    }
    try {
      window.localStorage.setItem(WS_STORAGE_KEY, String(target));
    } catch {
      /* storage unavailable — URL still carries the selection */
    }
  }, [workspaces, currentWorkspaceId, urlWsId, folderId, searchParams, navigate]);

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
  }, [activeQuery]);

  // Reset to the first page whenever the underlying view changes (folder,
  // workspace, search, or sort), so we never strand the user on an empty page.
  useEffect(() => {
    setPage(0);
  }, [folderId, currentWorkspaceId, activeQuery, sortBy, sortDir]);

  // Recursive subtree counts for the status bar — refreshed whenever the
  // listing itself reloads (uploads, deletes, navigation).
  useEffect(() => {
    if (folderId == null && currentWorkspaceId == null) {
      setTreeStats(null);
      return;
    }
    const qs = new URLSearchParams();
    if (folderId != null) qs.set("folder_id", String(folderId));
    else if (currentWorkspaceId != null) qs.set("workspace_id", String(currentWorkspaceId));
    api
      .get<{ folders: number; files: number }>(`/api/files/tree-stats?${qs.toString()}`)
      .then(setTreeStats)
      .catch(() => setTreeStats(null));
  }, [folderId, currentWorkspaceId, browse]);

  if (!identity) return null;

  // No workspace yet — browse has nothing to scope to (it would error on the
  // account's missing default workspace), so rather than an endless skeleton we
  // show a canvas: owners/admins are prompted to create the first workspace
  // (which gates uploading), scoped members are told nothing's been shared yet.
  if (workspaces !== null && workspaces.length === 0) {
    return (
      <>
        <div className="flex flex-1 items-center justify-center overflow-y-auto p-6">
          <div className="flex max-w-md flex-col items-center text-center">
            <span className="mb-5 flex h-16 w-16 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <Layers className="h-8 w-8" />
            </span>
            {isAdminOrOwner ? (
              <>
                <h1 className="text-2xl font-semibold text-foreground">Create your first workspace</h1>
                <p className="mt-2.5 text-[15px] leading-relaxed text-muted-foreground">
                  Workspaces are where your files live — a separate, access-controlled home for
                  each team, client, or project. Create one to start uploading.
                </p>
                <Button onClick={() => setWsModalOpen(true)} className="mt-6 h-11 gap-2 px-5 text-[15px]">
                  <Plus className="h-4 w-4" />
                  Create workspace
                </Button>
              </>
            ) : (
              <>
                <h1 className="text-2xl font-semibold text-foreground">No workspaces yet</h1>
                <p className="mt-2.5 text-[15px] leading-relaxed text-muted-foreground">
                  You haven't been added to any workspaces yet. Ask your account owner or an admin
                  to share one with you.
                </p>
              </>
            )}
          </div>
        </div>
        <WorkspaceCreateModal
          open={wsModalOpen}
          account={accountQuota}
          editing={wsEditing}
          onClose={() => {
            setWsModalOpen(false);
            setWsEditing(null);
          }}
          onSaved={async () => {
            // First workspace created → reload; the workspace-resolver effect
            // then selects it and browse loads automatically.
            await loadWorkspaces();
          }}
        />
      </>
    );
  }

  // Initial-load skeleton. Only shown when browse hasn't returned yet
  // AND we aren't searching — once browse data is in we keep showing
  // it during subsequent folder navigation rather than re-skeleton.
  if (browse === null && activeQuery.trim() === "") {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl space-y-8 p-6 lg:p-8">
          <div className="space-y-2">
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-8 w-40" />
          </div>
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-44 w-full" />
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

  const enterFolder = (id: number | null) => {
    // Navigating into a folder (or back to the workspace root) drops any active
    // search — the path carries the new location, no ?q.
    setSearchInput("");
    setActiveQuery("");
    navigate(filesPath(currentWorkspaceId, id));
    setSelectedFiles([]);
    setSelectedFolders([]);
  };

  const onSearchSubmit = (q: string) => {
    const trimmed = q.trim();
    setActiveQuery(trimmed);
    const next = new URLSearchParams(searchParams);
    if (trimmed) next.set("q", trimmed);
    else next.delete("q");
    setSearchParams(next);
  };

  // Switch the active workspace: record it in the URL and drop folder/search so
  // we land at the chosen workspace's root.
  const selectWorkspace = (id: number) => {
    try {
      window.localStorage.setItem(WS_STORAGE_KEY, String(id));
    } catch {
      /* storage unavailable */
    }
    // Land at the chosen workspace's root, dropping folder + search.
    setSearchInput("");
    setActiveQuery("");
    navigate(filesPath(id));
    setSelectedFiles([]);
    setSelectedFolders([]);
  };

  const onCreateFolder = () => {
    if (folderId === null && currentWorkspaceId == null) {
      setError("Pick a workspace first to create a folder.");
      return;
    }
    setDialog({ kind: "new-folder" });
  };

  // uploadBatch streams a set of files into the current folder/workspace.
  const uploadBatch = async (files: File[]) => {
    if (files.length === 0) return;
    setUploading(true);
    // Seed progress so the modal can mount immediately — XHR's first
    // onprogress event might lag a moment.
    setUploadProgress({
      fileIndex: 1,
      totalFiles: files.length,
      fileName: files[0].name,
      bytesLoaded: 0,
      bytesTotal: files[0].size,
    });
    try {
      const res = await api.uploadFiles(
        files,
        folderId,
        (p) => setUploadProgress(p),
        folderId == null ? currentWorkspaceId : undefined,
      );
      if (res.error) setError(res.error + (res.failed ? ` (file: ${res.failed})` : ""));
      await reload();
      await refresh({ silent: true });
    } catch (err) {
      if (err instanceof ApiError) setError(err.message || "Upload failed");
      else setError("Network error");
    } finally {
      setUploading(false);
      setUploadProgress(null);
    }
  };

  const PROTECTED_MSG = "Uploads to the Trilli Sign directory are protected and managed by Trilli Sign.";
  const handleFilesPicked = async (picked: FileList | File[]) => {
    if (inProtectedTree) {
      setError(PROTECTED_MSG);
      return;
    }
    setError(null);
    const arr = Array.from(picked);
    if (arr.length === 0) return;
    if (folderId === null && currentWorkspaceId == null) {
      setError("Pick a workspace first to upload files.");
      return;
    }

    // Check each upload for a name collision in the destination folder. Clean
    // files upload right away; clashes go through the rename/keep-both prompt
    // (Windows-style) instead of being silently auto-renamed.
    const clean: File[] = [];
    const conflicts: { file: File; suggested: string }[] = [];
    for (const f of arr) {
      try {
        const qs = new URLSearchParams({ name: f.name });
        if (folderId != null) qs.set("folder_id", String(folderId));
        const r = await api.get<{ available: boolean; suggested: string }>(
          `/api/files/name-check?${qs.toString()}`,
        );
        if (r.available) clean.push(f);
        else conflicts.push({ file: f, suggested: r.suggested });
      } catch {
        clean.push(f); // check failed — upload normally (backend still dedupes)
      }
    }

    await uploadBatch(clean);

    if (conflicts.length > 0) {
      const destLabel = folderId != null ? browse?.folder?.name : currentWorkspace?.name;
      setUploadConflicts({ queue: conflicts, idx: 0, destLabel });
    }
  };

  // Resolve one queued upload conflict: re-wrap the File under the chosen name
  // and upload it, or skip; advance the queue.
  const resolveUploadConflict = async (chosenName: string | null) => {
    if (!uploadConflicts) return;
    const { queue, idx } = uploadConflicts;
    const cur = queue[idx];
    if (chosenName) {
      const renamed = new File([cur.file], chosenName, { type: cur.file.type });
      await uploadBatch([renamed]);
    }
    if (idx + 1 < queue.length) {
      setUploadConflicts({ ...uploadConflicts, idx: idx + 1 });
    } else {
      setUploadConflicts(null);
    }
  };

  // ---- directory drag-in: recreate a dropped folder tree ------------------
  // Browsers expose dropped directories via webkitGetAsEntry(); we walk the
  // tree client-side, mirror it with the folders API, then stream each file
  // into its folder — Drive-style.
  type TreeFile = { relDir: string; file: File };

  const traverseEntry = async (
    entry: any,
    prefix: string,
    out: TreeFile[],
    onTick?: (files: number, folders: number) => void,
  ): Promise<void> => {
    if (!entry) return;
    if (entry.isFile) {
      const file = await new Promise<File | null>((res) => entry.file(res, () => res(null)));
      if (file) {
        out.push({ relDir: prefix, file });
        onTick?.(1, 0);
      }
      return;
    }
    if (entry.isDirectory) {
      const dir = prefix ? `${prefix}/${entry.name}` : entry.name;
      onTick?.(0, 1);
      const reader = entry.createReader();
      // readEntries returns batches (≤100); loop until an empty batch
      for (;;) {
        const batch = await new Promise<any[]>((res) => reader.readEntries(res, () => res([])));
        if (batch.length === 0) break;
        for (const child of batch) await traverseEntry(child, dir, out, onTick);
      }
      // remember empty dirs too so the structure survives
      if (!out.some((t) => t.relDir === dir || t.relDir.startsWith(dir + "/"))) {
        out.push({ relDir: dir, file: null as unknown as File });
      }
    }
  };

  // uploadTree mirrors the relative dirs under the current location and
  // uploads every file into its folder, with one aggregate progress stream.
  const uploadTree = async (items: TreeFile[]) => {
    setError(null);
    // 1 — every directory path, parents first
    const dirs = new Set<string>();
    for (const it of items) {
      let p = it.relDir;
      while (p) {
        dirs.add(p);
        const cut = p.lastIndexOf("/");
        p = cut > 0 ? p.slice(0, cut) : "";
      }
    }
    const ordered = [...dirs].sort((a, b) => a.split("/").length - b.split("/").length);
    setUploadPrep((p) => ({
      phase: "folders",
      files: p?.files ?? items.filter((t) => t.file).length,
      folders: ordered.length,
      foldersDone: 0,
    }));
    const dirIds = new Map<string, number | null>([["", folderId]]);
    for (const d of ordered) {
      const name = d.includes("/") ? d.slice(d.lastIndexOf("/") + 1) : d;
      const parentKey = d.includes("/") ? d.slice(0, d.lastIndexOf("/")) : "";
      const parentId = dirIds.get(parentKey) ?? null;
      try {
        const created = await api.post<FolderRecord>("/api/folders", {
          name,
          parent_folder_id: parentId,
          ...(parentId == null ? { workspace_id: currentWorkspaceId } : {}),
        });
        dirIds.set(d, created.id);
        setUploadPrep((p) => (p ? { ...p, foldersDone: p.foldersDone + 1 } : p));
      } catch (err) {
        // Name taken at the drop root → merge into the existing folder.
        const existing =
          parentKey === "" ? browse?.folders.find((f) => f.name === name) : undefined;
        if (existing) {
          dirIds.set(d, existing.id);
        } else {
          setError(err instanceof ApiError ? err.message : `Couldn't create folder "${name}".`);
          setUploadPrep(null);
          return;
        }
      }
    }
    // 2 — upload per directory with aggregate progress
    const real = items.filter((t) => t.file);
    const groups = new Map<string, File[]>();
    for (const t of real) {
      const g = groups.get(t.relDir) ?? [];
      g.push(t.file);
      groups.set(t.relDir, g);
    }
    const total = real.length;
    const overallTotal = real.reduce((n, t) => n + t.file.size, 0);
    let done = 0;
    let doneBytes = 0;
    setUploadPrep(null); // hand off from "preparing" to the live progress view
    setUploading(true);
    try {
      for (const [dir, fs] of groups) {
        const target = dirIds.get(dir) ?? folderId;
        const sizes = fs.map((f) => f.size);
        const res = await api.uploadFiles(
          fs,
          target,
          (p) => {
            const withinDone = sizes.slice(0, p.fileIndex - 1).reduce((n, b) => n + b, 0);
            setUploadProgress({
              fileIndex: done + p.fileIndex,
              totalFiles: total,
              fileName: p.fileName,
              bytesLoaded: p.bytesLoaded,
              bytesTotal: p.bytesTotal,
              overallLoaded: doneBytes + withinDone + p.bytesLoaded,
              overallTotal,
            });
          },
          target == null ? currentWorkspaceId : undefined,
        );
        if (res.error) {
          setError(res.error + (res.failed ? ` (file: ${res.failed})` : ""));
          break;
        }
        done += fs.length;
        doneBytes += sizes.reduce((n, b) => n + b, 0);
      }
      await reload();
      await refresh({ silent: true });
    } finally {
      setUploading(false);
      setUploadProgress(null);
    }
  };

  // handleExternalEntries routes a drop: directory entries build a tree;
  // loose root files keep the normal conflict-checked path.
  const handleExternalEntries = async (entries: any[], flat: File[]) => {
    if (folderId === null && currentWorkspaceId == null) {
      setError("Pick a workspace first to upload files.");
      return;
    }
    if (!entries.some((en) => en?.isDirectory)) {
      if (flat.length > 0) await handleFilesPicked(flat);
      return;
    }
    // The modal mounts NOW — before any traversal or network work — and
    // narrates the scan as entries stream in.
    setUploadPrep({ phase: "scanning", files: 0, folders: 0, foldersDone: 0 });
    try {
      const tree: TreeFile[] = [];
      let nf = 0;
      let nd = 0;
      for (const en of entries) {
        await traverseEntry(en, "", tree, (df, dd) => {
          nf += df;
          nd += dd;
          setUploadPrep((p) => (p && p.phase === "scanning" ? { ...p, files: nf, folders: nd } : p));
        });
      }
      const rootFiles = tree.filter((t) => t.relDir === "" && t.file).map((t) => t.file);
      const nested = tree.filter((t) => t.relDir !== "");
      if (nested.length > 0) await uploadTree(nested);
      if (rootFiles.length > 0) await handleFilesPicked(rootFiles);
    } finally {
      setUploadPrep(null);
    }
  };

  // "Upload folder" picker: webkitdirectory delivers files with relative paths.
  const handleFolderPicked = async (picked: File[]) => {
    if (picked.length === 0) return;
    const tree: TreeFile[] = picked.map((f) => {
      const rel = ((f as any).webkitRelativePath as string) || f.name;
      const cut = rel.lastIndexOf("/");
      return { relDir: cut > 0 ? rel.slice(0, cut) : "", file: f };
    });
    const dirCount = new Set(tree.map((t) => t.relDir).filter(Boolean)).size;
    setUploadPrep({ phase: "scanning", files: tree.length, folders: dirCount, foldersDone: 0 });
    try {
      const rootFiles = tree.filter((t) => t.relDir === "").map((t) => t.file);
      const nested = tree.filter((t) => t.relDir !== "");
      if (nested.length > 0) await uploadTree(nested);
      if (rootFiles.length > 0) await handleFilesPicked(rootFiles);
    } finally {
      setUploadPrep(null);
    }
  };
  const openFolderPicker = () => dirInputRef.current?.click();

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    if (inProtectedTree) {
      setError(PROTECTED_MSG);
      return;
    }
    e.preventDefault();
    setDragOver(false);
    // Ignore our own internal file-drag — those land on folder cards / breadcrumb.
    // Let it bubble so the page handler can run its drag-end cleanup.
    if (isInternalDrag(e)) return;
    // External drop landed directly on the zone: handle it here and stop it
    // bubbling to the page-level onDrop, which would otherwise upload twice.
    e.stopPropagation();
    // Entries must be captured synchronously during the drop event.
    const entries = Array.from(e.dataTransfer.items ?? [])
      .map((it) => (it as any).webkitGetAsEntry?.())
      .filter(Boolean);
    const flat = Array.from(e.dataTransfer.files ?? []);
    if (entries.length > 0 || flat.length > 0) {
      void handleExternalEntries(entries, flat);
    }
  };

  // ----- internal drag-and-drop helpers ----------------------------------
  // Drag MIME + cross-component trash events live in @/lib/events.

  const isInternalDrag = (e: React.DragEvent) =>
    e.dataTransfer.types.includes(TRILLI_DRAG_MIME) ||
    draggingFileIds !== null ||
    draggingFolderIds !== null;

  type DragPayload = { fileIds: number[]; folderIds: number[] };

  const writeDragPayload = (e: React.DragEvent, payload: DragPayload) => {
    setDraggingFileIds(payload.fileIds);
    setDraggingFolderIds(payload.folderIds);
    try {
      e.dataTransfer.setData(TRILLI_DRAG_MIME, JSON.stringify(payload));
      e.dataTransfer.effectAllowed = "move";
    } catch {
      // Some browsers (rare) throw on setData during dragstart; the state
      // fallback in isInternalDrag still works in that case.
    }
  };

  const onFileDragStart = (e: React.DragEvent<HTMLTableRowElement>, f: FileRecord) => {
    // If the dragged row is part of an existing selection, drag the whole
    // selection (files + folders together). Otherwise drag just this file.
    const useSelection =
      selectedFiles.includes(f.id) && selectedFiles.length + selectedFolders.length > 1;
    writeDragPayload(e, {
      fileIds: useSelection ? selectedFiles : [f.id],
      folderIds: useSelection ? selectedFolders : [],
    });
    // Also expose the file as an OS download so dropping the row on the desktop
    // saves it (Chromium only; coexists with the internal-move payload above).
    // Only for a single-file drag — DownloadURL carries just one file.
    if (!useSelection) setFileDownloadDrag(e, f);
  };

  const onFolderDragStart = (e: React.DragEvent<HTMLTableRowElement>, d: FolderRecord) => {
    const useSelection =
      selectedFolders.includes(d.id) && selectedFiles.length + selectedFolders.length > 1;
    writeDragPayload(e, {
      fileIds: useSelection ? selectedFiles : [],
      folderIds: useSelection ? selectedFolders : [d.id],
    });
    // Dropping a single folder on the desktop saves it as <name>.zip
    // (server streams the subtree; Chromium-only, internal moves unaffected).
    if (!useSelection) setFolderDownloadDrag(e, d);
  };

  const onItemDragEnd = () => {
    setDraggingFileIds(null);
    setDraggingFolderIds(null);
    setDragOverFolderId(null);
    setDragOverCrumb(null);
    setDragOverWs(false);
  };

  const readDragPayload = (e: React.DragEvent): DragPayload => {
    try {
      const raw = e.dataTransfer.getData(TRILLI_DRAG_MIME);
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed.fileIds) && Array.isArray(parsed.folderIds)) {
          return parsed;
        }
      }
    } catch {
      // fall through to state
    }
    return {
      fileIds: draggingFileIds ?? [],
      folderIds: draggingFolderIds ?? [],
    };
  };

  // moveItemsTo moves the given files + folders into the target folder
  // (or root when target is null). Skips no-op moves; lets the backend
  // catch invalid cases like cycles and surfaces the error.
  const moveItemsTo = async (
    fileIds: number[],
    folderIds: number[],
    targetFolderId: number | null,
    targetWorkspaceId?: number,
  ) => {
    // A cross-workspace move is never a no-op even when the parent folder id
    // matches (root → root across workspaces), so only skip same-parent moves
    // when staying in the same workspace.
    const crossWs =
      targetWorkspaceId != null && targetWorkspaceId !== currentWorkspaceId;
    const filesToMove = (browse?.files ?? []).filter(
      (f) =>
        fileIds.includes(f.id) &&
        (crossWs || (f.parent_folder_id ?? null) !== targetFolderId),
    );
    const foldersToMove = (browse?.folders ?? []).filter(
      (d) =>
        folderIds.includes(d.id) &&
        d.id !== targetFolderId &&
        (crossWs || (d.parent_folder_id ?? null) !== targetFolderId),
    );
    if (filesToMove.length === 0 && foldersToMove.length === 0) return;
    // workspace_id is only needed for workspace-root destinations; when moving
    // into a folder the backend derives the workspace from the folder.
    const wsPayload =
      targetFolderId == null && targetWorkspaceId != null
        ? { workspace_id: targetWorkspaceId }
        : {};

    // Friendly destination label for the conflict prompt (folder or workspace).
    const destLabel =
      targetFolderId == null
        ? currentWorkspace?.name
        : (browse?.folders ?? []).find((f) => f.id === targetFolderId)?.name ??
          breadcrumb.find((b) => b.id === targetFolderId)?.name;

    // Detect file-name collisions in the destination up front, so we can prompt
    // (Windows-style) instead of silently auto-renaming. Folders aren't checked
    // here — the backend rejects folder-name clashes outright.
    const clean: FileRecord[] = [];
    const conflicts: { file: FileRecord; suggested: string }[] = [];
    for (const f of filesToMove) {
      try {
        const qs = new URLSearchParams({ name: f.name });
        if (targetFolderId != null) qs.set("folder_id", String(targetFolderId));
        const r = await api.get<{ available: boolean; suggested: string }>(
          `/api/files/name-check?${qs.toString()}`,
        );
        if (r.available) clean.push(f);
        else conflicts.push({ file: f, suggested: r.suggested });
      } catch {
        clean.push(f); // check failed — move normally (backend still auto-dedupes)
      }
    }

    const totalClean = clean.length + foldersToMove.length;
    // Only show the progress modal when there's enough to make the wait
    // noticeable. A single item is fast enough that flashing a modal
    // would be more annoying than helpful.
    const showProgress = totalClean > 1;
    try {
      // Serialize the moves. The backend's _copy_NN rename runs inside the
      // move's tx, but concurrent moves of equally-named items would each
      // see an "empty" destination and both pick _copy_01, since the files
      // table has no unique constraint to break the tie. Awaiting one move
      // before starting the next means the second move sees the first
      // move's renamed file and picks _copy_02 instead.
      let i = 0;
      for (const f of clean) {
        i++;
        if (showProgress) {
          setMoveProgress({ itemIndex: i, totalItems: totalClean, itemName: f.name });
        }
        await api.patch(`/api/files/${f.id}`, { folder_id: targetFolderId, ...wsPayload });
      }
      for (const d of foldersToMove) {
        i++;
        if (showProgress) {
          setMoveProgress({ itemIndex: i, totalItems: totalClean, itemName: d.name });
        }
        await api.patch(`/api/folders/${d.id}`, { parent_folder_id: targetFolderId, ...wsPayload });
      }
      if (totalClean > 0) await reload();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    } finally {
      setMoveProgress(null);
    }

    // Hand any name collisions to the conflict modal (resolved one at a time).
    if (conflicts.length > 0) {
      setMoveConflicts({ queue: conflicts, idx: 0, targetFolderId, wsPayload, destLabel });
    } else {
      setSelectedFiles([]);
      setSelectedFolders([]);
    }
  };

  // Resolve one queued move conflict: rename-and-move with the chosen name, or
  // skip it; advance the queue, reloading once the last one is handled.
  const resolveMoveConflict = async (chosenName: string | null) => {
    if (!moveConflicts) return;
    const { queue, idx, targetFolderId, wsPayload } = moveConflicts;
    const cur = queue[idx];
    if (chosenName) {
      try {
        await api.patch(`/api/files/${cur.file.id}`, {
          name: chosenName,
          folder_id: targetFolderId,
          ...wsPayload,
        });
      } catch (err) {
        if (err instanceof ApiError) setError(err.message);
      }
    }
    if (idx + 1 < queue.length) {
      setMoveConflicts({ ...moveConflicts, idx: idx + 1 });
    } else {
      setMoveConflicts(null);
      setSelectedFiles([]);
      setSelectedFolders([]);
      await reload();
    }
  };

  const onFolderDragOver = (e: React.DragEvent, folderRow: FolderRecord) => {
    if (!isInternalDrag(e)) return;
    // Don't accept a drop on a folder onto itself — show no-drop instead.
    if (draggingFolderIds?.includes(folderRow.id)) {
      e.dataTransfer.dropEffect = "none";
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = "move";
    setDragOverFolderId(folderRow.id);
  };

  const onFolderDrop = async (e: React.DragEvent, folderRow: FolderRecord) => {
    if (!isInternalDrag(e)) return;
    if (draggingFolderIds?.includes(folderRow.id)) return;
    e.preventDefault();
    e.stopPropagation();
    const payload = readDragPayload(e);
    onItemDragEnd();
    await moveItemsTo(payload.fileIds, payload.folderIds, folderRow.id);
  };

  // Breadcrumb crumbs are drop targets too: drop files/folders onto a crumb to
  // move them up to that folder (or the workspace root). targetFolderId is null
  // for the root crumb. crumbKey drives the hover highlight.
  const onCrumbDragOver = (
    e: React.DragEvent,
    targetFolderId: number | null,
    crumbKey: number | "root",
  ) => {
    if (!isInternalDrag(e)) return;
    // Can't move a folder into itself.
    if (targetFolderId != null && draggingFolderIds?.includes(targetFolderId)) {
      e.dataTransfer.dropEffect = "none";
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = "move";
    setDragOverCrumb(crumbKey);
  };

  const onCrumbDrop = async (e: React.DragEvent, targetFolderId: number | null) => {
    if (!isInternalDrag(e)) return;
    if (targetFolderId != null && draggingFolderIds?.includes(targetFolderId)) return;
    e.preventDefault();
    e.stopPropagation();
    const payload = readDragPayload(e);
    onItemDragEnd();
    // Root crumb needs the workspace id (folder is null); folder crumbs derive
    // their workspace from the target folder, so we still pass the current one
    // (same workspace) to keep the no-op guard accurate.
    await moveItemsTo(payload.fileIds, payload.folderIds, targetFolderId, currentWorkspaceId ?? undefined);
  };

  // Dragging an item onto the workspace selector opens the move picker seeded
  // with the dragged selection — a fast path to move into another workspace.
  const onWsSelectorDragOver = (e: React.DragEvent) => {
    if (!isInternalDrag(e)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = "move";
    setDragOverWs(true);
  };
  const onWsSelectorDrop = (e: React.DragEvent) => {
    if (!isInternalDrag(e)) return;
    e.preventDefault();
    e.stopPropagation();
    const payload = readDragPayload(e);
    onItemDragEnd();
    if (payload.fileIds.length + payload.folderIds.length > 0) {
      setDragMove({ fileIds: payload.fileIds, folderIds: payload.folderIds });
    }
  };

  const openFilePicker = () => inputRef.current?.click();

  const handleRenameFile = (f: FileRecord) => setDialog({ kind: "rename-file", file: f });

  // ----- same-account Copy / Duplicate / Paste ----------------------------
  // Default name for the Duplicate dialog: "<base>_copy_01<ext>" (folders have
  // no ext), stripping any existing "_copy_NN" so it doesn't stack. The backend
  // still auto-dedupes if that name is taken.
  const copyDefaultName = (name: string, isFolder: boolean): string => {
    const dot = isFolder ? -1 : name.lastIndexOf(".");
    const base = dot > 0 ? name.slice(0, dot) : name;
    const ext = dot > 0 ? name.slice(dot) : "";
    return `${base.replace(/_copy_\d+$/, "")}_copy_01${ext}`;
  };
  const runCopy = async (body: Record<string, unknown>) => {
    const res = await api.post<{ copied_files: number; copied_folders: number; errors?: string[] }>(
      "/api/files/copy",
      body,
    );
    await reload();
    emitStorageChanged();
    if (res.errors && res.errors.length) setError(res.errors[0]);
    return res;
  };
  const handleDuplicateFile = (f: FileRecord) => setDialog({ kind: "duplicate-file", file: f });
  const handleDuplicateFolder = (d: FolderRecord) => setDialog({ kind: "duplicate-folder", folder: d });
  const handleCopyFile = (f: FileRecord) => setClipboard({ fileIds: [f.id], folderIds: [] });
  const handlePrintFile = (f: FileRecord) => {
    const url = printUrlFor({ id: f.id, name: f.name, content_type: f.content_type });
    if (url) printDocument(url);
  };
  const handleCopyFolder = (d: FolderRecord) => setClipboard({ fileIds: [], folderIds: [d.id] });
  const handleCopySelection = () =>
    setClipboard({ fileIds: [...selectedFiles], folderIds: [...selectedFolders] });
  // Paste the clipboard into a specific folder (a folder row's "Paste into
  // folder"), or null for the current level (empty-area / file-row "Paste here").
  const pasteInto = async (destFolderId: number | null) => {
    if (!clipboard) return;
    const { fileIds, folderIds } = clipboard;
    setPasteStatus({ phase: "working", files: fileIds.length, folders: folderIds.length });
    try {
      const res = await runCopy({
        file_ids: fileIds,
        folder_ids: folderIds,
        dest_folder_id: destFolderId,
        dest_workspace_id: currentWorkspaceId,
      });
      setClipboard(null); // a paste consumes the clipboard
      setPasteStatus({
        phase: "done",
        files: res.copied_files,
        folders: res.copied_folders,
        error: res.errors && res.errors.length ? res.errors[0] : undefined,
      });
      window.setTimeout(() => setPasteStatus(null), 2500);
    } catch (e) {
      setPasteStatus({
        phase: "error",
        files: 0,
        folders: 0,
        error: e instanceof Error ? e.message : "Paste failed.",
      });
      window.setTimeout(() => setPasteStatus(null), 4000);
    }
  };
  const handlePaste = () => pasteInto(folderId);
  const hasClipboard = !!clipboard && clipboard.fileIds.length + clipboard.folderIds.length > 0;
  // Label for a current-level paste, reflecting what's on the clipboard.
  const pasteVerb = (): string => {
    if (!clipboard) return "Paste";
    const f = clipboard.fileIds.length;
    const d = clipboard.folderIds.length;
    if (f + d === 1) return d === 1 ? "Paste folder" : "Paste file";
    return `Paste ${f + d} items`;
  };

  // Single-file Move opens the same destination-picker modal as bulk move
  // (no more browser prompt) — see the moveFile modal render below.
  const handleMoveFile = (f: FileRecord) => setMoveFile(f);

  const handleDeleteFile = (f: FileRecord) => setDialog({ kind: "trash-file", file: f });

  const handleDeleteFolder = (folderRow: FolderRecord) =>
    setDialog({ kind: "trash-folder", folder: folderRow });

  const clearSelection = () => {
    setSelectedFiles([]);
    setSelectedFolders([]);
  };

  // Bulk download triggers a sequential anchor click per file. Folders aren't
  // downloadable as a single artifact yet, so they're skipped silently.
  const handleBulkDownload = () => {
    selectedFiles.forEach((id, i) => {
      setTimeout(() => {
        const a = document.createElement("a");
        a.href = downloadUrl(id);
        a.download = "";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      }, i * 150);
    });
  };

  const handleToggleStar = async (f: FileRecord) => {
    const next = !f.starred_at;
    try {
      await api.patch(`/api/files/${f.id}`, { starred: next });
      await reload();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  const handleToggleStarFolder = async (d: FolderRecord) => {
    const next = !d.starred_at;
    try {
      await api.patch(`/api/folders/${d.id}`, { starred: next });
      await reload();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  // Bulk-star every selected file + folder.
  const handleBulkStar = async () => {
    if (selectedFiles.length + selectedFolders.length === 0) return;
    try {
      await Promise.all([
        ...selectedFiles.map((id) => api.patch(`/api/files/${id}`, { starred: true })),
        ...selectedFolders.map((id) => api.patch(`/api/folders/${id}`, { starred: true })),
      ]);
      clearSelection();
      await reload();
    } catch (err) {
      if (err instanceof ApiError) setError(err.message);
    }
  };

  const handleBulkMove = async (targetFolderId: number | null, targetWorkspaceId?: number) => {
    if (selectedFiles.length + selectedFolders.length === 0) return;
    await moveItemsTo(selectedFiles, selectedFolders, targetFolderId, targetWorkspaceId);
  };

  const handleBulkDelete = () => {
    const total = selectedFiles.length + selectedFolders.length;
    if (total === 0) return;
    setDialog({ kind: "trash-bulk", count: total });
  };

  const handleRenameFolder = (folderRow: FolderRecord) =>
    setDialog({ kind: "rename-folder", folder: folderRow });

  // Single-folder Move opens the same destination-picker modal as files/bulk
  // move — see the moveFolder modal render below.
  const handleMoveFolder = (folderRow: FolderRecord) => setMoveFolder(folderRow);

  const inSearchMode = activeQuery.trim() !== "";
  const breadcrumb = browse?.breadcrumb ?? [];
  // Inside the Trilli Sign system directory (any depth): uploads are disabled
  // — its contents are staged and protected by Trilli Sign.
  const inProtectedTree = breadcrumb.some((b) => !!b.protected_source);
  const displayedFiles: FileRecord[] = inSearchMode ? searchResults ?? [] : browse?.files ?? [];
  const displayedFolders: FolderRecord[] = inSearchMode ? [] : browse?.folders ?? [];
  // Breadcrumb is the folder chain within the current workspace (workspace top
  // folders have no parent, so the chain naturally stops at the workspace root).
  // The "Workspace" home crumb is rendered separately and leads the trail.
  const showHome = true;
  const totalItems = displayedFolders.length + displayedFiles.length;
  const totalSelected = selectedFiles.length + selectedFolders.length;
  // Other accounts the user can copy INTO (everything except the one currently
  // open). Drives the "Copy to account" bulk action + modal; cross-account
  // transfers are always copies, never moves.
  const otherAccounts: CopyAccount[] = (identity?.tenants ?? [])
    .filter((t) => t.id !== identity?.tenant?.id)
    .map((t) => ({ id: t.id, name: t.name, is_owner: t.is_owner }));
  const currentAccountName = identity?.tenant?.name ?? "this account";
  const allSelected = totalItems > 0 && totalSelected === totalItems;
  const someSelected = totalSelected > 0 && !allSelected;
  const selectionBreakdown = [
    selectedFolders.length > 0
      ? `${selectedFolders.length} folder${selectedFolders.length === 1 ? "" : "s"}`
      : "",
    selectedFiles.length > 0
      ? `${selectedFiles.length} file${selectedFiles.length === 1 ? "" : "s"}`
      : "",
  ]
    .filter(Boolean)
    .join(" · ");

  // Sort folders and files separately so folders always cluster above files
  // regardless of the chosen column.
  const sortDirMultiplier = sortDir === "asc" ? 1 : -1;
  // Equal keys (same size, same type, …) fall through to name ASCENDING so a
  // sort always yields a visible, deterministic order — otherwise a column of
  // identical values looks like "sorting is broken" (Windows does the same).
  const nameTie = (a: { name: string }, b: { name: string }) =>
    a.name.toLowerCase().localeCompare(b.name.toLowerCase(), undefined, { numeric: true });
  const sortedFolders = [...displayedFolders].sort((a, b) => {
    let cmp = 0;
    switch (sortBy) {
      case "name":
        cmp = nameTie(a, b);
        break;
      case "modified":
        cmp = new Date(a.updated_at ?? a.created_at).getTime() -
              new Date(b.updated_at ?? b.created_at).getTime();
        break;
      case "type":
        cmp = 0; // every folder is the same "type"
        break;
      case "size":
        cmp = (a.size_bytes ?? 0) - (b.size_bytes ?? 0);
        break;
    }
    return cmp === 0 && sortBy !== "name" ? nameTie(a, b) : cmp * sortDirMultiplier;
  });
  const sortedFiles = [...displayedFiles].sort((a, b) => {
    let cmp = 0;
    switch (sortBy) {
      case "name":
        cmp = nameTie(a, b);
        break;
      case "modified":
        cmp = new Date(a.updated_at ?? a.created_at).getTime() -
              new Date(b.updated_at ?? b.created_at).getTime();
        break;
      case "type":
        cmp = fileExtension(a.name).localeCompare(fileExtension(b.name));
        break;
      case "size":
        cmp = a.size_bytes - b.size_bytes;
        break;
    }
    return cmp === 0 && sortBy !== "name" ? nameTie(a, b) : cmp * sortDirMultiplier;
  });

  // ONE combined ordering: files and folders interleave by the sorted column
  // (sort by Name and an "aardvark.pdf" outranks a "Zebra" folder; sort by
  // Size desc and a big file tops small folders). Folders no longer cluster.
  type PageItem =
    | { kind: "folder"; folder: FolderRecord }
    | { kind: "file"; file: FileRecord };
  const combined: PageItem[] = [
    ...sortedFolders.map((d) => ({ kind: "folder" as const, folder: d })),
    ...sortedFiles.map((f) => ({ kind: "file" as const, file: f })),
  ].sort((A, B) => {
    const a = A.kind === "folder" ? A.folder : A.file;
    const b = B.kind === "folder" ? B.folder : B.file;
    let cmp = 0;
    switch (sortBy) {
      case "name":
        cmp = nameTie(a, b);
        break;
      case "modified":
        cmp = new Date(a.updated_at ?? a.created_at).getTime() -
              new Date(b.updated_at ?? b.created_at).getTime();
        break;
      case "type": {
        // folders sort as the empty type — grouped ahead of files ascending
        const ta = A.kind === "folder" ? "" : fileExtension(A.file.name);
        const tb = B.kind === "folder" ? "" : fileExtension(B.file.name);
        cmp = ta.localeCompare(tb);
        break;
      }
      case "size":
        cmp = (a.size_bytes ?? 0) - (b.size_bytes ?? 0);
        break;
    }
    return cmp === 0 && sortBy !== "name" ? nameTie(a, b) : cmp * sortDirMultiplier;
  });

  const totalPages = Math.max(1, Math.ceil(totalItems / FILES_PAGE_SIZE));
  const safePage = Math.min(page, totalPages - 1);
  const pageStart = safePage * FILES_PAGE_SIZE;
  const pageEnd = pageStart + FILES_PAGE_SIZE;
  const pageItems = combined.slice(pageStart, pageEnd);
  // Files of this page in DISPLAY order — drives the preview modal's
  // next/prev sequence and each row's preview index.
  const pageFiles = pageItems.filter((it) => it.kind === "file").map((it) => it.file);

  // Whether an external OS-file drag should be treated as an upload anywhere
  // on the files page (the upload zone is only shown — and uploads only make
  // sense — while browsing a writable workspace, not in search results).
  const acceptsPageUpload = canWrite && !inSearchMode;

  // Page-level handlers do double duty:
  //   • Internal drags (file/folder moves) that aren't over a specific
  //     folder/crumb target get the move-cursor everywhere, and a drop in a
  //     non-target area is a silent no-op (cleanup only).
  //   • External (OS-file) drags anywhere on the page light up the upload
  //     zone and are accepted as an upload to the current folder — the zone
  //     becomes a cosmetic reference rather than the only valid target.
  const onPageDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    if (isInternalDrag(e)) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      return;
    }
    if (acceptsPageUpload && e.dataTransfer.types.includes("Files")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      if (!dragOver) setDragOver(true);
    }
  };
  // Clear the highlight only when the drag actually leaves the files area
  // (relatedTarget outside the container), not when crossing between child
  // rows — otherwise the zone would flicker as the cursor moves.
  const onPageDragLeave = (e: React.DragEvent<HTMLDivElement>) => {
    const next = e.relatedTarget as Node | null;
    if (next && e.currentTarget.contains(next)) return;
    setDragOver(false);
  };
  const onPageDrop = (e: React.DragEvent<HTMLDivElement>) => {
    if (isInternalDrag(e)) {
      e.preventDefault();
      onItemDragEnd();
      return;
    }
    if (acceptsPageUpload && e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      e.preventDefault();
      setDragOver(false);
      const entries = Array.from(e.dataTransfer.items ?? [])
        .map((it) => (it as any).webkitGetAsEntry?.())
        .filter(Boolean);
      void handleExternalEntries(entries, Array.from(e.dataTransfer.files));
    }
  };

  // The search field, defined once and rendered either inline with the
  // workspace breadcrumb (normal browsing) or on its own row (search mode /
  // no workspaces). ml-auto right-aligns it within whichever row holds it.
  const searchBar = (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSearchSubmit(searchInput);
      }}
      className="ml-auto w-full sm:w-80"
    >
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          placeholder="Search files and folders…"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          className="h-[34px] rounded-[11px] border border-border bg-card pl-10 pr-9 text-[13.5px] focus-visible:ring-1 focus-visible:ring-primary/40"
        />
        {(searchInput || inSearchMode) && (
          <button
            type="button"
            onClick={() => {
              setSearchInput("");
              onSearchSubmit("");
            }}
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"
            title="Clear search"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
    </form>
  );

  return (
    <>
    {/* hidden folder picker: webkitdirectory (files arrive with relative paths) */}
    <input
      ref={dirInputRef}
      type="file"
      multiple
      className="hidden"
      {...({ webkitdirectory: "" } as Record<string, string>)}
      onChange={(e) => {
        const fs = Array.from(e.target.files ?? []);
        e.target.value = "";
        if (fs.length > 0) void handleFolderPicked(fs);
      }}
    />
    {acceptsPageUpload && (
      <FileDropOverlay label="Drop files to upload" hint="Release to upload them here" />
    )}
    <div
      ref={scrollerRef}
      className="flex-1 overflow-y-auto"
      onDragOver={onPageDragOver}
      onDragLeave={onPageDragLeave}
      onDrop={onPageDrop}
      onMouseDown={onMarqueeMouseDown}
    >
      <div
        className={cn(
          // On short laptop panels (<=850px tall) the section gaps and vertical
          // padding are the main reason this page overflows its card and shows a
          // scrollbar; tighten them there (! to beat lg:p-8) so a near-empty
          // workspace fits without scrolling. Wider/taller screens unchanged.
          "mx-auto max-w-7xl space-y-8 p-6 lg:p-8 [@media(max-height:1100px)]:!space-y-3 [@media(max-height:1100px)]:!py-3",
          totalSelected > 0 && "pb-24",
        )}
      >
        <PageHeader
          title={
            inSearchMode
              ? `Search: "${activeQuery}"`
              : browse?.folder?.name ?? "Files"
          }
          icon={<Folder className="h-6 w-6 text-sidebar-foreground" />}
          subtitle={
            inSearchMode
              ? `${displayedFiles.length} match${displayedFiles.length === 1 ? "" : "es"} across this workspace`
              : `${identity.tenant?.name ?? "Workspace"}`
          }
        />

        {!inSearchMode && canWrite && (
          // !mt-5 tightens the gap to the heading above (overrides the outer
          // space-y-8) so the tips sit close to "My Files" as part of that
          // header block. UploadTips is a dismissible, restorable carousel.
          <UploadTips className="!mt-5 [@media(max-height:1100px)]:!mt-3" />
        )}

        {!inSearchMode && canWrite && inProtectedTree && (
          <div className="flex items-center gap-4 rounded-xl border border-border bg-muted/30 px-6 py-5">
            <span className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-[#0A2540] text-white">
              <Lock className="h-5 w-5" />
            </span>
            <div className="min-w-0">
              <p className="text-[14px] font-semibold text-foreground">Managed by Trilli Sign</p>
              <p className="mt-0.5 text-[12.5px] leading-snug text-muted-foreground">
                Uploads to the Trilli Sign directory are protected and managed by Trilli Sign.
                Envelope documents and signed agreements are filed here automatically.
              </p>
            </div>
          </div>
        )}
        {!inSearchMode && canWrite && !inProtectedTree && (
          <FileUploadZone
            onPick={handleFilesPicked}
            uploading={uploading}
            dragOver={dragOver}
            onDragOver={(e) => {
              // An internal file-move drag passing over the upload zone
              // shouldn't make it light up — the page-level handler is
              // already showing the move cursor.
              if (isInternalDrag(e)) return;
              e.preventDefault();
              setDragOver(true);
            }}
            // The page-level onDragLeave owns clearing the highlight (it only
            // fires when the drag leaves the whole files area), so the zone's
            // own leave is a no-op to avoid fighting it / flicker.
            onDragLeave={() => {}}
            onDrop={handleDrop}
            inputRef={inputRef}
            maxBytes={quota?.max_file_size_bytes ?? 0}
            folderName={browse?.folder?.name}
            compact={totalItems > 0}
          />
        )}

        {error && (
          <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {/* Top toolbar group (storage meter + workspace/search) is separated
            from the browser group (breadcrumb + file container) by a clear
            gap, so the listing reads as its own section. */}
        <div className="space-y-6 [@media(max-height:1100px)]:space-y-3">
        <div className="space-y-5 [@media(max-height:1100px)]:space-y-3">
          {!inSearchMode && workspaces && workspaces.length > 0 && (
            <div className="space-y-2.5">
          {/* Row 1 — storage meter for the current workspace. */}
          {!inSearchMode && currentWorkspace && (
            (() => {
              const used = currentWorkspace.storage_bytes_used;
              const alloc = currentWorkspace.disk_allocation_bytes;
              const pct = alloc > 0 ? Math.min(100, Math.round((used / alloc) * 100)) : 0;
              return (
                <div className="flex items-center gap-2">
                  {/* "Workspace storage" label — the sidebar card shows the
                      ACCOUNT-wide total; this meter is this workspace's pooled
                      allocation. Without the label the two read as duplicates. */}
                  <span className="whitespace-nowrap text-[11px] font-medium text-muted-foreground">
                    Workspace storage
                  </span>
                  <span className="whitespace-nowrap text-[11px] text-muted-foreground">
                    {formatBytes(used)} of {formatBytes(alloc)}
                  </span>
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                    <div
                      className={cn(
                        "h-full rounded-full",
                        pct >= 100 ? "bg-destructive" : pct >= 80 ? "bg-destructive/70" : "bg-primary",
                      )}
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <span className="whitespace-nowrap text-[11px] tabular-nums text-muted-foreground">
                    {pct}%
                  </span>
                </div>
              );
            })()
          )}

          {/* Row 2 — New button, then the breadcrumb workspace control on one
              line. Workspace is the first level (name → root, chevron →
              switcher); one chevron between each section. */}
          {!inSearchMode && workspaces && workspaces.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
            {/* Workspace selector — a single dropdown box. Clicking the name
                or the chevron opens the switcher. No folder breadcrumb. */}
            <DropdownMenu onOpenChange={(open) => open && setWsMenuQuery("")}>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  title={dragOverWs ? "Drop to move into another workspace" : "Switch workspace"}
                  onDragOver={onWsSelectorDragOver}
                  onDragLeave={() => setDragOverWs(false)}
                  onDrop={onWsSelectorDrop}
                  className={cn(
                    "inline-flex h-[34px] cursor-pointer items-center gap-1.5 rounded-md border border-primary/40 bg-primary/5 px-2.5 text-[13.5px] font-semibold text-foreground outline-none transition-colors hover:bg-primary/10 focus:outline-none focus-visible:outline-none focus-visible:ring-0",
                    dragOverWs && "bg-primary/20 ring-2 ring-primary/50",
                  )}
                >
                  <Layers className="h-4 w-4 text-primary" />
                  {currentWorkspace?.name ?? "Workspace"}
                  <ChevronDown className="-mr-0.5 h-4 w-4" strokeWidth={2.5} style={{ color: "#eb5a5c" }} />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-64 p-0">
                  {(() => {
                    // Show a filter box once the list is long enough to warrant it.
                    const SEARCH_THRESHOLD = 7;
                    const showSearch = workspaces.length > SEARCH_THRESHOLD;
                    const q = wsMenuQuery.trim().toLowerCase();
                    const filtered = q
                      ? workspaces.filter((w) => w.name.toLowerCase().includes(q))
                      : workspaces;
                    return (
                      <>
                        {/* Selection first — this is a switcher. The label makes
                            the intent explicit; the list is the primary content. */}
                        <div className="px-3 pt-2.5 pb-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                          {workspaces.length > 1 ? "Switch workspace" : "Workspace"}
                        </div>
                        {showSearch && (
                          <div className="border-b border-border p-2">
                            <div className="relative">
                              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                              <input
                                autoFocus
                                value={wsMenuQuery}
                                onChange={(e) => setWsMenuQuery(e.target.value)}
                                onKeyDown={(e) => e.stopPropagation()}
                                placeholder="Search workspaces…"
                                className="h-7 w-full rounded-md border border-foreground/20 bg-background pl-8 pr-2 text-sm outline-none focus:border-primary/50"
                              />
                            </div>
                          </div>
                        )}
                        <div className="max-h-64 overflow-y-auto p-1">
                          {filtered.length === 0 ? (
                            <p className="px-2 py-3 text-center text-xs text-muted-foreground">
                              No workspaces match “{wsMenuQuery.trim()}”.
                            </p>
                          ) : (
                            filtered.map((w) => (
                              <DropdownMenuItem
                                key={w.id}
                                onSelect={() => selectWorkspace(w.id)}
                                className="flex items-center justify-between gap-2 text-sm"
                              >
                                <span className="flex items-center gap-2 truncate">
                                  <Layers className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                                  <span className="truncate">{w.name}</span>
                                </span>
                                {w.id === currentWorkspaceId && (
                                  <Check className="h-3.5 w-3.5 flex-shrink-0 text-primary" />
                                )}
                              </DropdownMenuItem>
                            ))
                          )}
                        </div>
                        {/* Secondary actions below a divider — owner/admin only.
                            Create more workspaces (discoverable here, so the list
                            has something to switch between) and edit the current. */}
                        {isAdminOrOwner && (
                          <>
                            <DropdownMenuSeparator className="my-0" />
                            <div className="p-1">
                              <DropdownMenuItem
                                onSelect={() => {
                                  setWsEditing(null);
                                  setWsModalOpen(true);
                                }}
                                className="text-sm"
                              >
                                <Plus className="mr-2 h-3.5 w-3.5" />
                                New workspace
                              </DropdownMenuItem>
                              {currentWorkspace && (
                                <DropdownMenuItem
                                  onSelect={() => {
                                    setWsEditing(currentWorkspace);
                                    setWsModalOpen(true);
                                  }}
                                  className="text-sm"
                                >
                                  <Pencil className="mr-2 h-3.5 w-3.5" />
                                  Edit this workspace
                                </DropdownMenuItem>
                              )}
                            </div>
                          </>
                        )}
                      </>
                    );
                  })()}
                </DropdownMenuContent>
            </DropdownMenu>
            {canWrite && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  title="New"
                  className="inline-flex h-[34px] cursor-pointer items-center justify-center rounded-md border border-primary/40 bg-primary/5 px-2 outline-none focus:outline-none focus-visible:outline-none focus-visible:ring-0"
                >
                  <Plus className="h-4 w-4" style={{ color: "#eb5a5c" }} />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-48">
                {isAdminOrOwner && (
                  <>
                    <DropdownMenuItem
                      onSelect={() => {
                        setWsEditing(null);
                        setWsModalOpen(true);
                      }}
                    >
                      <Layers className="mr-2 h-4 w-4" />
                      New workspace
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                  </>
                )}
                <DropdownMenuItem onSelect={onCreateFolder}>
                  <FolderPlus className="mr-2 h-4 w-4" />
                  New folder
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={openFilePicker}>
                  <FileUp className="mr-2 h-4 w-4" />
                  Upload files
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={openFolderPicker}>
                  <FolderUp className="mr-2 h-4 w-4" />
                  Upload folder
                </DropdownMenuItem>
                {clipboard && (clipboard.fileIds.length > 0 || clipboard.folderIds.length > 0) && (
                  <DropdownMenuItem onSelect={() => void handlePaste()}>
                    <ClipboardPaste className="mr-2 h-4 w-4" />
                    {pasteVerb()}
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            )}
            {canWrite && (
              <CloudImportButton onClick={() => setCloudImportOpen(true)} />
            )}
            {searchBar}
            </div>
          )}
            </div>
          )}

        </div>

        {/* Unified contents table — folders first, then files, in one card.
            Folder rows are click-to-navigate + drop targets for file moves.
            File rows are draggable + carry the full action menu. */}
        <section className="space-y-3">
          {/* Breadcrumb bar — its own line directly above the file container.
              Workspace name as the root crumb, then the folder chain with ›
              separators (each crumb is also a drop target for moves). */}
          {!inSearchMode && workspaces && workspaces.length > 0 && (
            <div className="flex flex-wrap items-center gap-0.5 rounded-md border border-border bg-muted/30 px-2.5 py-1.5 text-[13.5px]">
              {/* Root link — directory icon + workspace name, one clickable
                  enclosure that sends you to the workspace root. */}
              <button
                type="button"
                onClick={() => enterFolder(null)}
                disabled={folderId == null}
                title="Workspace root"
                onDragOver={(e) => onCrumbDragOver(e, null, "root")}
                onDragLeave={() => setDragOverCrumb(null)}
                onDrop={(e) => void onCrumbDrop(e, null)}
                className={cn(
                  "flex items-center gap-1.5 rounded px-2 py-1 transition-colors",
                  folderId == null
                    ? "font-semibold text-foreground"
                    : "cursor-pointer text-muted-foreground hover:bg-primary/10 hover:text-primary",
                  dragOverCrumb === "root" && "bg-primary/15 text-primary ring-1 ring-primary/40",
                )}
              >
                <FolderRoot className="h-4 w-4 flex-shrink-0" />
                {currentWorkspace?.name ?? "All files"}
              </button>
              {breadcrumb.map((b, i) => {
                const isLast = i === breadcrumb.length - 1;
                return (
                  <span key={b.id} className="flex items-center">
                    <ChevronRight className="h-4 w-4 text-muted-foreground/50" />
                    {isLast ? (
                      <span className="rounded px-1.5 py-1 font-medium text-foreground">{b.name}</span>
                    ) : (
                      <button
                        type="button"
                        onClick={() => enterFolder(b.id)}
                        onDragOver={(e) => onCrumbDragOver(e, b.id, b.id)}
                        onDragLeave={() => setDragOverCrumb(null)}
                        onDrop={(e) => void onCrumbDrop(e, b.id)}
                        className={cn(
                          "cursor-pointer rounded px-1.5 py-1 text-muted-foreground transition-colors hover:bg-primary/10 hover:text-primary",
                          dragOverCrumb === b.id && "bg-primary/15 text-primary ring-1 ring-primary/40",
                        )}
                      >
                        {b.name}
                      </button>
                    )}
                  </span>
                );
              })}
            </div>
          )}

          {/* Search row — only when the breadcrumb toolbar above isn't shown
              (search mode / no workspaces). In normal browsing the search sits
              inline with the workspace breadcrumb instead. */}
          {(inSearchMode || !workspaces || workspaces.length === 0) && (
            <div className="flex flex-wrap items-center gap-3">
              {searchBar}

              {/* New button fallback — keep New reachable in search mode and
                  when there are no workspaces. Viewers (read-only) never get it. */}
              {canWrite && (
                <div className="ml-auto">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button className="h-7 gap-1.5 rounded px-3 text-xs">
                        <Plus className="h-3.5 w-3.5" />
                        <span className="hidden sm:inline">New</span>
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-48">
                      <DropdownMenuItem onSelect={onCreateFolder}>
                        <FolderPlus className="mr-2 h-4 w-4" />
                        New folder
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={openFilePicker}>
                        <FileUp className="mr-2 h-4 w-4" />
                        Upload files
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={openFolderPicker}>
                        <FolderUp className="mr-2 h-4 w-4" />
                        Upload folder
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              )}
            </div>
          )}
          <ContextMenu>
            <ContextMenuTrigger asChild>
              <div className="min-h-[55vh]">
          {inSearchMode && searchResults === null ? (
            // Search still in flight (e.g. jumping to a file from recent
            // activity) — show a loading skeleton, NOT the "No matches" empty
            // state, so the result doesn't flash blank before the file appears.
            <div className="space-y-2.5 rounded-xl border border-border bg-card p-4 shadow-sm">
              <Skeleton className="h-11 w-full" />
              <Skeleton className="h-11 w-full" />
              <Skeleton className="h-11 w-full" />
              <Skeleton className="h-11 w-full" />
            </div>
          ) : displayedFolders.length + displayedFiles.length === 0 ? (
            (() => {
              // At a workspace root (no folder open) an empty state is the
              // whole workspace being empty — not a folder. Differentiate the
              // icon + copy so the two read distinctly.
              const atWorkspaceRoot = !inSearchMode && folderId == null;
              const EmptyIcon = inSearchMode ? FileIconGeneric : atWorkspaceRoot ? Layers : FileIconGeneric;
              const title = inSearchMode
                ? "No matches"
                : atWorkspaceRoot
                  ? `${currentWorkspace?.name ?? "This workspace"} is empty`
                  : inProtectedTree
                    ? "Nothing here yet — Trilli Sign files agreements here automatically."
                    : "This folder is empty";
              const body = inSearchMode
                ? "Nothing in this workspace matches your search."
                : inProtectedTree
                  ? "Uploads to the Trilli Sign directory are protected and managed by Trilli Sign."
                  : atWorkspaceRoot
                    ? "This workspace has no files or folders yet. Drop files on the zone above, or click New to upload or create a folder."
                    : "Drop files on the zone above, or click New to upload or create a folder.";
              return (
                <div className="flex flex-col items-center justify-center rounded-xl border border-border bg-card py-16 text-center shadow-sm">
                  <div
                    className={cn(
                      "mb-4 flex h-16 w-16 items-center justify-center rounded-2xl",
                      atWorkspaceRoot ? "bg-primary/10" : "bg-secondary",
                    )}
                  >
                    <EmptyIcon
                      className={cn(
                        "h-8 w-8",
                        atWorkspaceRoot ? "text-primary" : "text-muted-foreground",
                      )}
                    />
                  </div>
                  <h3 className="mb-1 text-lg font-semibold text-foreground">{title}</h3>
                  <p className="max-w-md text-sm text-muted-foreground">{body}</p>
                </div>
              );
            })()
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
              {/* Bulk-actions bar — stays mounted and eases into view (grid-rows
                  + opacity) whenever anything is selected, via the header
                  "select all" or any individual checkbox. Mirrors Trash. */}
              <div
                className={cn(
                  "grid overflow-hidden transition-all duration-300 ease-out",
                  totalSelected > 0 ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0",
                )}
              >
                <div className="min-h-0">
                  <div className="flex items-center gap-2 border-b border-border bg-primary/5 px-4 py-1.5">
                    <span className="text-[12.5px] font-medium text-foreground">
                      {totalSelected} selected
                      {selectionBreakdown && (
                        <span className="text-muted-foreground"> · {selectionBreakdown}</span>
                      )}
                    </span>
                    <div className="ml-auto flex items-center gap-1.5">
                      <button
                        type="button"
                        onClick={() => void handleBulkStar()}
                        className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted"
                      >
                        <Star className="h-3.5 w-3.5" /> Star
                      </button>
                      <button
                        type="button"
                        onClick={handleBulkDownload}
                        disabled={selectedFiles.length === 0}
                        title={selectedFiles.length === 0 ? "Folders can't be downloaded yet" : undefined}
                        className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        <Download className="h-3.5 w-3.5" /> Download
                      </button>
                      <button
                        type="button"
                        onClick={() => setMoveOpen(true)}
                        className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted"
                      >
                        <Move className="h-3.5 w-3.5" /> Move to…
                      </button>
                      <button
                        type="button"
                        onClick={handleCopySelection}
                        title="Copy the selection — then Paste it into another folder"
                        className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted"
                      >
                        <Copy className="h-3.5 w-3.5" /> Copy
                      </button>
                      {otherAccounts.length > 0 && (
                        <button
                          type="button"
                          onClick={() => setCopyOpen(true)}
                          title="Copy the selection into another account you belong to"
                          className="inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-card px-2.5 text-[12px] font-medium text-foreground hover:bg-muted"
                        >
                          <Copy className="h-3.5 w-3.5" /> Copy to account…
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={handleBulkDelete}
                        className="inline-flex h-7 items-center gap-1.5 rounded-md bg-destructive px-2.5 text-[12px] font-semibold text-white hover:bg-destructive/90"
                      >
                        <Trash2 className="h-3.5 w-3.5" /> Delete
                      </button>
                      <button
                        type="button"
                        onClick={clearSelection}
                        className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                        title="Clear selection"
                      >
                        <X className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
              <div className="max-h-[448px] overflow-y-auto">
              <table className="w-full text-sm">
                <thead className="sticky top-0 z-10 bg-card">
                  <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                    <th className="w-12 px-4 py-2.5 text-left">
                      <Checkbox
                        size="lg"
                        checked={allSelected}
                        ref={(el) => {
                          if (el) el.indeterminate = someSelected;
                        }}
                        onChange={() => {
                          if (allSelected) {
                            setSelectedFiles([]);
                            setSelectedFolders([]);
                          } else {
                            setSelectedFiles(displayedFiles.map((f) => f.id));
                            setSelectedFolders(displayedFolders.map((d) => d.id));
                          }
                        }}
                        disabled={totalItems === 0}
                        aria-label="Select all"
                      />
                    </th>
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
                  {/* Top spacer — visual breathing room between the header
                      and the first data row. Matched by a sibling row at the
                      bottom so the gap above and below the data is equal. */}
                  <tr aria-hidden="true">
                    <td colSpan={6} className="h-3 p-0" />
                  </tr>
                  {/* Folder rows */}
                  {pageItems.map((item) => {
                    if (item.kind === "folder") {
                    const d = item.folder;
                    const isFolderSelected = selectedFolders.includes(d.id);
                    const isBeingDragged = draggingFolderIds?.includes(d.id) ?? false;
                    return (
                    <ContextMenu key={`folder-${d.id}`}>
                      <ContextMenuTrigger asChild>
                    <tr
                      data-selid={`d${d.id}`}
                      draggable
                      onDragStart={(e) => onFolderDragStart(e, d)}
                      onDragEnd={onItemDragEnd}
                      onClick={() => enterFolder(d.id)}
                      onDragOver={(e) => onFolderDragOver(e, d)}
                      onDragLeave={() =>
                        setDragOverFolderId((curr) => (curr === d.id ? null : curr))
                      }
                      onDrop={(e) => onFolderDrop(e, d)}
                      className={cn(
                        "group cursor-pointer transition-colors hover:bg-muted/40 active:cursor-grabbing",
                        isFolderSelected && "bg-primary/5",
                        isBeingDragged && "opacity-40",
                        dragOverFolderId === d.id &&
                          "bg-primary/5 ring-2 ring-inset ring-primary/30",
                      )}
                    >
                      <td className="px-4 py-1.5" onClick={(e) => e.stopPropagation()}>
                        <Checkbox
                          size="lg"
                          checked={isFolderSelected}
                          onChange={() => {
                            const adding = !selectedFolders.includes(d.id);
                            setSelectedFolders((curr) =>
                              adding ? [...curr, d.id] : curr.filter((x) => x !== d.id),
                            );
                            // Individual checks only toggle selection — the
                            // actions modal opens via the header "select all".
                          }}
                          aria-label={`Select ${d.name}`}
                        />
                      </td>
                      <td className="px-4 py-1.5">
                        <div className="flex items-center gap-3">
                          {/* Same 32px leading tile as file rows (FileThumb),
                              so folder and file names align in one column. */}
                          <span className="flex h-8 w-8 flex-shrink-0 items-center justify-center">
                            <span className="relative inline-flex h-5 w-5">
                              <Folder
                                className="h-5 w-5 fill-amber-300 text-amber-500"
                              />
                              {d.file_count > 0 && (
                                <span
                                  className="absolute -right-1 -top-1 flex h-[13px] min-w-[13px] items-center justify-center rounded-full bg-primary/55 px-1 text-[8px] font-semibold leading-none text-primary-foreground ring-1 ring-card"
                                  title={`${d.file_count} file${d.file_count === 1 ? "" : "s"}`}
                                >
                                  {d.file_count > 99 ? "99+" : d.file_count}
                                </span>
                              )}
                            </span>
                          </span>
                          <span className="inline-flex items-center gap-1.5 font-medium text-foreground">
                            {truncateName(d.name)}
                            {d.starred_at && (
                              <Star
                                className="h-3 w-3 flex-shrink-0 fill-amber-300 text-amber-500"
                                aria-label="Starred"
                              />
                            )}
                          </span>
                        </div>
                      </td>
                      <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                        {formatDateTime(d.updated_at ?? d.created_at)}
                      </td>
                      <td className="hidden px-4 py-1.5 text-muted-foreground md:table-cell">
                        <span className="rounded-md bg-primary/10 px-2 py-1 text-xs font-medium text-primary">
                          Folder
                        </span>
                      </td>
                      <td className="px-4 py-1.5 text-[12.5px] text-muted-foreground">
                        {d.size_bytes > 0 ? formatBytes(d.size_bytes) : "—"}
                      </td>
                      <td className="px-4 py-1.5" onClick={(e) => e.stopPropagation()}>
                        <div className="flex items-center justify-end gap-1">
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8 text-muted-foreground hover:text-foreground data-[state=open]:text-foreground"
                                title="More"
                              >
                                <MoreHorizontal className="h-4 w-4" />
                              </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="w-48">
                              {hasClipboard && (
                                <>
                                  <DropdownMenuItem onSelect={() => void pasteInto(d.id)}>
                                    <ClipboardPaste className="mr-2 h-4 w-4" />
                                    Paste into “{d.name}”
                                  </DropdownMenuItem>
                                  <DropdownMenuSeparator />
                                </>
                              )}
                              <DropdownMenuItem onSelect={() => setShareTarget({ kind: "folder", id: d.id, name: d.name })}>
                                <Share2 className="mr-2 h-4 w-4" />
                                Share
                              </DropdownMenuItem>
                              <DropdownMenuItem onSelect={() => setPortalTarget({ folderId: d.id, folderName: d.name })}>
                                <Inbox className="mr-2 h-4 w-4" />
                                Request files
                              </DropdownMenuItem>
                              <DropdownMenuItem onSelect={() => handleToggleStarFolder(d)}>
                                {d.starred_at ? (
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
                              <DropdownMenuItem onSelect={() => handleRenameFolder(d)}>
                                <Pencil className="mr-2 h-4 w-4" />
                                Rename
                              </DropdownMenuItem>
                              <DropdownMenuItem onSelect={() => handleDuplicateFolder(d)}>
                                <CopyPlus className="mr-2 h-4 w-4" />
                                Duplicate
                              </DropdownMenuItem>
                              <DropdownMenuItem onSelect={() => handleCopyFolder(d)}>
                                <Copy className="mr-2 h-4 w-4" />
                                Copy
                              </DropdownMenuItem>
                              <DropdownMenuItem onSelect={() => handleMoveFolder(d)}>
                                <Move className="mr-2 h-4 w-4" />
                                Move
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
                        {hasClipboard && (
                          <>
                            <ContextMenuItem onSelect={() => void pasteInto(d.id)}>
                              <ClipboardPaste className="mr-2 h-4 w-4" /> Paste into “{d.name}”
                            </ContextMenuItem>
                            <ContextMenuSeparator />
                          </>
                        )}
                        <ContextMenuItem onSelect={() => setShareTarget({ kind: "folder", id: d.id, name: d.name })}>
                          <Share2 className="mr-2 h-4 w-4" /> Share
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => setPortalTarget({ folderId: d.id, folderName: d.name })}>
                          <Inbox className="mr-2 h-4 w-4" /> Request files
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleToggleStarFolder(d)}>
                          {d.starred_at ? (
                            <><StarOff className="mr-2 h-4 w-4" /> Unstar</>
                          ) : (
                            <><Star className="mr-2 h-4 w-4" /> Star</>
                          )}
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleRenameFolder(d)}>
                          <Pencil className="mr-2 h-4 w-4" /> Rename
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleDuplicateFolder(d)}>
                          <CopyPlus className="mr-2 h-4 w-4" /> Duplicate
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleCopyFolder(d)}>
                          <Copy className="mr-2 h-4 w-4" /> Copy
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleMoveFolder(d)}>
                          <Move className="mr-2 h-4 w-4" /> Move
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem variant="destructive" onSelect={() => handleDeleteFolder(d)}>
                          <Trash2 className="mr-2 h-4 w-4" /> Move to trash
                        </ContextMenuItem>
                      </ContextMenuContent>
                    </ContextMenu>
                    );
                    }
                    const f = item.file;
                    const fi = pageFiles.indexOf(f);
                    const isSelected = selectedFiles.includes(f.id);
                    const isBeingDragged = draggingFileIds?.includes(f.id) ?? false;
                    const previewable = canPreview(f.name, f.content_type);
                    // txt/json edit in the preview modal, not in Trilli Docs
                    const modalEditable = ["txt", "json"].includes(fileExtension(f.name));
                    return (
                    <ContextMenu key={`file-${f.id}`}>
                      <ContextMenuTrigger asChild>
                      <tr
                        data-selid={`f${f.id}`}
                        draggable
                        onDragStart={(e) => onFileDragStart(e, f)}
                        onDragEnd={onItemDragEnd}
                        className={cn(
                          "group cursor-grab transition-colors hover:bg-muted/40 active:cursor-grabbing",
                          isSelected && "bg-primary/5",
                          isBeingDragged && "opacity-40",
                        )}
                      >
                        <td className="px-4 py-1.5">
                          <Checkbox
                            size="lg"
                            checked={isSelected}
                            onChange={() => {
                              const adding = !selectedFiles.includes(f.id);
                              setSelectedFiles((curr) =>
                                adding ? [...curr, f.id] : curr.filter((x) => x !== f.id),
                              );
                              // Individual checks only toggle selection — the
                              // actions modal opens via the header "select all".
                            }}
                            aria-label={`Select ${f.name}`}
                          />
                        </td>
                        <td className="px-4 py-1.5">
                          <button
                            type="button"
                            onClick={() => previewable && openPreview(fi)}
                            className={cn("flex items-center gap-3 text-left", !previewable && "cursor-default")}
                            title={previewable ? `Preview ${f.name}` : f.name}
                          >
                            <FileThumb
                              id={f.id}
                              name={f.name}
                              contentType={f.content_type}
                              version={f.updated_at}
                            />
                            <span className={cn("inline-flex items-center gap-1.5 text-xs font-medium text-foreground", previewable && "hover:underline")}>
                              {truncateName(f.name)}
                              {f.starred_at && (
                                <Star
                                  className="h-3 w-3 flex-shrink-0 fill-amber-300 text-amber-500"
                                  aria-label="Starred"
                                />
                              )}
                            </span>
                          </button>
                        </td>
                        <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                          {formatDateTime(f.updated_at ?? f.created_at)}
                        </td>
                        <td className="hidden px-4 py-1.5 text-muted-foreground md:table-cell">
                          <span className="inline-flex items-center gap-1 rounded-md bg-secondary px-2 py-1 text-xs">
                            {fileExtension(f.name) || "—"}
                            {f.protected_source && (
                              <span className="inline-flex items-center gap-0.5 text-muted-foreground" title="Managed by Trilli Sign — delete its envelope in Trilli Sign to remove it">
                                · <Lock className="h-3 w-3" /> protected
                              </span>
                            )}
                          </span>
                        </td>
                        <td className="px-4 py-1.5 text-[12.5px] text-muted-foreground">
                          {formatBytes(f.size_bytes)}
                        </td>
                        <td className="px-4 py-1.5">
                          <div className="flex items-center justify-end gap-1">
                            <a href={downloadUrl(f.id, f.updated_at)}>
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
                                  className="h-8 w-8 text-muted-foreground hover:text-foreground data-[state=open]:text-foreground"
                                  title="More"
                                >
                                  <MoreHorizontal className="h-4 w-4" />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align="end" className="w-48">
                                {hasClipboard && (
                                  <>
                                    <DropdownMenuItem onSelect={() => void handlePaste()}>
                                      <ClipboardPaste className="mr-2 h-4 w-4" />
                                      {pasteVerb()}
                                    </DropdownMenuItem>
                                    <DropdownMenuSeparator />
                                  </>
                                )}
                                {previewable && (
                                  <DropdownMenuItem onSelect={() => openPreview(fi)}>
                                    <Eye className="mr-2 h-4 w-4" />
                                    Preview
                                  </DropdownMenuItem>
                                )}
                                {(isEditable(f.name) || modalEditable) && (
                                  <DropdownMenuItem
                                    onSelect={() =>
                                      modalEditable ? openPreview(fi, true) : editFile(navigate, f.id, f.name)
                                    }
                                  >
                                    <Edit3 className="mr-2 h-4 w-4" />
                                    Edit
                                  </DropdownMenuItem>
                                )}
                                {canPrint(f.name, f.content_type) && (
                                  <DropdownMenuItem onSelect={() => handlePrintFile(f)}>
                                    <Printer className="mr-2 h-4 w-4" />
                                    Print
                                  </DropdownMenuItem>
                                )}
                                <DropdownMenuItem asChild>
                                  <a href={downloadUrl(f.id, f.updated_at)}>
                                    <Download className="mr-2 h-4 w-4" />
                                    Download
                                  </a>
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={() => setShareTarget({ kind: "file", id: f.id, name: f.name })}>
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
                                <DropdownMenuItem onSelect={() => handleDuplicateFile(f)}>
                                  <CopyPlus className="mr-2 h-4 w-4" />
                                  Duplicate
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={() => handleCopyFile(f)}>
                                  <Copy className="mr-2 h-4 w-4" />
                                  Copy
                                </DropdownMenuItem>
                                <DropdownMenuItem onSelect={() => handleMoveFile(f)}>
                                  <Move className="mr-2 h-4 w-4" />
                                  Move
                                </DropdownMenuItem>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  variant="destructive"
                                  disabled={!!f.protected_source}
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
                        {hasClipboard && (
                          <>
                            <ContextMenuItem onSelect={() => void handlePaste()}>
                              <ClipboardPaste className="mr-2 h-4 w-4" /> {pasteVerb()}
                            </ContextMenuItem>
                            <ContextMenuSeparator />
                          </>
                        )}
                        {previewable && (
                          <ContextMenuItem onSelect={() => openPreview(fi)}>
                            <Eye className="mr-2 h-4 w-4" /> Preview
                          </ContextMenuItem>
                        )}
                        {(isEditable(f.name) || modalEditable) && (
                          <ContextMenuItem
                            onSelect={() =>
                              modalEditable ? openPreview(fi, true) : editFile(navigate, f.id, f.name)
                            }
                          >
                            <Edit3 className="mr-2 h-4 w-4" /> Edit
                          </ContextMenuItem>
                        )}
                        {canPrint(f.name, f.content_type) && (
                          <ContextMenuItem onSelect={() => handlePrintFile(f)}>
                            <Printer className="mr-2 h-4 w-4" /> Print
                          </ContextMenuItem>
                        )}
                        <ContextMenuItem asChild>
                          <a href={downloadUrl(f.id, f.updated_at)}>
                            <Download className="mr-2 h-4 w-4" /> Download
                          </a>
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => setShareTarget({ kind: "file", id: f.id, name: f.name })}>
                          <Share2 className="mr-2 h-4 w-4" /> Share
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem onSelect={() => handleToggleStar(f)}>
                          {f.starred_at ? (
                            <><StarOff className="mr-2 h-4 w-4" /> Unstar</>
                          ) : (
                            <><Star className="mr-2 h-4 w-4" /> Star</>
                          )}
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleRenameFile(f)}>
                          <Pencil className="mr-2 h-4 w-4" /> Rename
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleDuplicateFile(f)}>
                          <CopyPlus className="mr-2 h-4 w-4" /> Duplicate
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleCopyFile(f)}>
                          <Copy className="mr-2 h-4 w-4" /> Copy
                        </ContextMenuItem>
                        <ContextMenuItem onSelect={() => handleMoveFile(f)}>
                          <Move className="mr-2 h-4 w-4" /> Move
                        </ContextMenuItem>
                        <ContextMenuSeparator />
                        <ContextMenuItem variant="destructive" disabled={!!f.protected_source} onSelect={() => handleDeleteFile(f)}>
                          <Trash2 className="mr-2 h-4 w-4" /> Move to trash
                        </ContextMenuItem>
                      </ContextMenuContent>
                    </ContextMenu>
                    );
                  })}
                </tbody>
              </table>
              </div>
              {/* non-scrolling spacer: the gap above the status bar holds even
                  when the list scrolls (the in-table spacer scrolls away). */}
              <div aria-hidden="true" className="h-3" />
              {totalPages > 1 && (
                <Paginator
                  currentPage={safePage + 1}
                  totalPages={totalPages}
                  rangeStart={pageStart + 1}
                  rangeEnd={Math.min(pageEnd, totalItems)}
                  total={totalItems}
                  onJump={(p) => setPage(p - 1)}
                />
              )}
              {/* Explorer-style status bar: the location's RECURSIVE contents.
                  pl-16 = checkbox column (w-12) + name-cell padding (px-4), so
                  "Total:" sits exactly where the folder/file objects start. */}
              <div className="flex items-center justify-between gap-3 border-t border-border bg-muted/30 py-[5px] pl-16 pr-4 text-[11.5px] text-muted-foreground">
                {(() => {
                  const nFolders = treeStats?.folders ?? sortedFolders.length;
                  const nFiles = treeStats?.files ?? sortedFiles.length;
                  const nItems = nFolders + nFiles;
                  return (
                    <span className="tabular-nums">
                      Total:{" "}
                      <span className="font-medium text-foreground">
                        {nItems.toLocaleString()}
                      </span>{" "}
                      item{nItems === 1 ? "" : "s"}
                      {" \u2013 "}
                      {nFolders.toLocaleString()} folder{nFolders === 1 ? "" : "s"},{" "}
                      {nFiles.toLocaleString()} file{nFiles === 1 ? "" : "s"}
                    </span>
                  );
                })()}
                <span className="tabular-nums font-medium text-foreground">
                  {selectedFiles.length + selectedFolders.length > 0
                    ? `${(selectedFiles.length + selectedFolders.length).toLocaleString()} selected`
                    : ""}
                </span>
              </div>
            </div>
          )}
              </div>
            </ContextMenuTrigger>
            {/* Right-click the empty browser area (works in empty / folders-only
                folders too) — Paste here, plus New folder / Upload. */}
            <ContextMenuContent className="w-52">
              {clipboard && clipboard.fileIds.length + clipboard.folderIds.length > 0 ? (
                <ContextMenuItem onSelect={() => void handlePaste()}>
                  <ClipboardPaste className="mr-2 h-4 w-4" /> {pasteVerb()}
                </ContextMenuItem>
              ) : (
                <ContextMenuItem disabled>
                  <ClipboardPaste className="mr-2 h-4 w-4" /> Paste (nothing copied)
                </ContextMenuItem>
              )}
              <ContextMenuSeparator />
              <ContextMenuItem onSelect={onCreateFolder}>
                <FolderPlus className="mr-2 h-4 w-4" /> New folder
              </ContextMenuItem>
              <ContextMenuItem onSelect={openFilePicker}>
                <FileUp className="mr-2 h-4 w-4" /> Upload files
              </ContextMenuItem>
              <ContextMenuItem onSelect={openFolderPicker}>
                <FolderUp className="mr-2 h-4 w-4" /> Upload folder
              </ContextMenuItem>
            </ContextMenuContent>
          </ContextMenu>
        </section>
        </div>
      </div>
    </div>

    {moveProgress && (
      <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150">
        <div className="w-[420px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in duration-200">
          <div className="mb-4 flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10">
              <Move className="h-5 w-5 text-primary" />
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">Moving items</h3>
              <p className="text-xs text-muted-foreground">
                {moveProgress.itemIndex} of {moveProgress.totalItems} items
              </p>
            </div>
          </div>
          <p
            className="mb-2 truncate text-xs font-medium text-foreground"
            title={moveProgress.itemName}
          >
            {moveProgress.itemName}
          </p>
          <div className="h-2 overflow-hidden rounded-full bg-secondary">
            <div
              className="h-full rounded-full bg-primary transition-[width] duration-200 ease-out"
              style={{
                width: `${Math.round(
                  (moveProgress.itemIndex / Math.max(1, moveProgress.totalItems)) * 100,
                )}%`,
              }}
            />
          </div>
          <div className="mt-2 flex items-center justify-end text-xs">
            <span className="font-medium tabular-nums text-foreground">
              {Math.round(
                (moveProgress.itemIndex / Math.max(1, moveProgress.totalItems)) * 100,
              )}
              %
            </span>
          </div>
        </div>
      </div>
    )}

    {pasteStatus && (
      <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150">
        <div className="w-[380px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in duration-200">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10">
              {pasteStatus.phase === "working" ? (
                <ClipboardPaste className="h-5 w-5 animate-pulse text-primary" />
              ) : pasteStatus.phase === "done" ? (
                <Check className="h-5 w-5 text-emerald-600" />
              ) : (
                <X className="h-5 w-5 text-destructive" />
              )}
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">
                {pasteStatus.phase === "working"
                  ? "Pasting…"
                  : pasteStatus.phase === "done"
                    ? "Paste complete"
                    : "Paste failed"}
              </h3>
              <p className="text-xs text-muted-foreground">
                {pasteStatus.phase === "working"
                  ? `Copying ${pasteStatus.files + pasteStatus.folders} item${
                      pasteStatus.files + pasteStatus.folders === 1 ? "" : "s"
                    }…`
                  : pasteStatus.phase === "done"
                    ? `${pasteStatus.files} file${pasteStatus.files === 1 ? "" : "s"} · ${
                        pasteStatus.folders
                      } folder${pasteStatus.folders === 1 ? "" : "s"} copied`
                    : pasteStatus.error ?? "Something went wrong."}
              </p>
            </div>
          </div>
          {pasteStatus.phase === "working" && (
            <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-secondary">
              <div className="h-full w-1/3 animate-pulse rounded-full bg-primary" />
            </div>
          )}
          {pasteStatus.phase === "done" && pasteStatus.error && (
            <p className="mt-3 text-xs text-amber-600">Some items were skipped: {pasteStatus.error}</p>
          )}
        </div>
      </div>
    )}

    {uploadPrep && !uploadProgress && (
      <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150">
        <div className="w-[420px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in duration-200">
          <div className="mb-4 flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10">
              <Loader2 className="h-5 w-5 animate-spin text-primary" />
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">Preparing upload</h3>
              <p className="text-xs text-muted-foreground">
                {uploadPrep.phase === "scanning"
                  ? "Reading the folder's contents…"
                  : `Creating folders — ${uploadPrep.foldersDone} of ${uploadPrep.folders}`}
              </p>
            </div>
          </div>
          <div className="flex items-center gap-4 rounded-lg border border-border bg-muted/40 px-4 py-3 text-[13px] text-foreground">
            <span className="tabular-nums">
              <span className="font-semibold">{uploadPrep.files.toLocaleString()}</span>{" "}
              file{uploadPrep.files === 1 ? "" : "s"}
            </span>
            <span className="h-4 w-px bg-border" />
            <span className="tabular-nums">
              <span className="font-semibold">{uploadPrep.folders.toLocaleString()}</span>{" "}
              folder{uploadPrep.folders === 1 ? "" : "s"}
            </span>
          </div>
          {uploadPrep.phase === "folders" && (
            <div className="mt-3 h-2 overflow-hidden rounded-full bg-secondary">
              <div
                className="h-full rounded-full bg-primary transition-[width] duration-150 ease-out"
                style={{
                  width: `${Math.round((uploadPrep.foldersDone / Math.max(1, uploadPrep.folders)) * 100)}%`,
                }}
              />
            </div>
          )}
        </div>
      </div>
    )}

    {uploadProgress && (
      <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150">
        <div className="w-[420px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in duration-200">
          <div className="mb-4 flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10">
              <Upload className="h-5 w-5 text-primary" />
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-sm font-semibold text-foreground">
                Uploading files
              </h3>
              <p className="text-xs text-muted-foreground">
                {uploadProgress.fileIndex} of {uploadProgress.totalFiles}
                {uploadProgress.totalFiles > 1 ? " files" : " file"}
              </p>
            </div>
          </div>
          <p
            className="mb-2 truncate text-xs font-medium text-foreground"
            title={uploadProgress.fileName}
          >
            {uploadProgress.fileName}
          </p>
          <div className="h-2 overflow-hidden rounded-full bg-secondary">
            <div
              className="h-full rounded-full bg-primary transition-[width] duration-150 ease-out"
              style={{
                width: `${Math.min(
                  100,
                  Math.round(
                    ((uploadProgress.overallTotal
                      ? uploadProgress.overallLoaded! / Math.max(1, uploadProgress.overallTotal)
                      : uploadProgress.bytesLoaded / Math.max(1, uploadProgress.bytesTotal)) as number) * 100,
                  ),
                )}%`,
              }}
            />
          </div>
          <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
            <span>
              {uploadProgress.overallTotal
                ? `${formatBytes(uploadProgress.overallLoaded ?? 0)} / ${formatBytes(uploadProgress.overallTotal)}`
                : `${formatBytes(uploadProgress.bytesLoaded)} / ${formatBytes(uploadProgress.bytesTotal)}`}
            </span>
            <span className="font-medium tabular-nums text-foreground">
              {Math.min(
                100,
                Math.round(
                  ((uploadProgress.overallTotal
                    ? uploadProgress.overallLoaded! / Math.max(1, uploadProgress.overallTotal)
                    : uploadProgress.bytesLoaded / Math.max(1, uploadProgress.bytesTotal)) as number) * 100,
                ),
              )}
              %
            </span>
          </div>
        </div>
      </div>
    )}

    {moveOpen && totalSelected > 0 && (
      <MoveItemsModal
        fileCount={selectedFiles.length}
        folderCount={selectedFolders.length}
        workspaces={(workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))}
        currentWorkspaceId={currentWorkspaceId}
        currentFolderId={folderId}
        selectedFolderIds={selectedFolders}
        onMove={handleBulkMove}
        onClose={() => setMoveOpen(false)}
      />
    )}

    {copyOpen && totalSelected > 0 && otherAccounts.length > 0 && (
      <CopyToAccountModal
        fileCount={selectedFiles.length}
        folderCount={selectedFolders.length}
        fileIds={selectedFiles}
        folderIds={selectedFolders}
        accounts={otherAccounts}
        currentAccountName={currentAccountName}
        onCopied={clearSelection}
        onClose={() => setCopyOpen(false)}
      />
    )}

    {moveFile && (
      <MoveItemsModal
        fileCount={1}
        folderCount={0}
        workspaces={(workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))}
        currentWorkspaceId={currentWorkspaceId}
        currentFolderId={moveFile.parent_folder_id ?? folderId}
        selectedFolderIds={[]}
        onMove={async (targetFolderId, targetWorkspaceId) => {
          try {
            await api.patch(`/api/files/${moveFile.id}`, {
              folder_id: targetFolderId,
              workspace_id: targetWorkspaceId,
            });
            await reload();
          } catch (err) {
            if (err instanceof ApiError) setError(err.message);
          }
        }}
        onClose={() => setMoveFile(null)}
      />
    )}

    {moveFolder && (
      <MoveItemsModal
        fileCount={0}
        folderCount={1}
        workspaces={(workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))}
        currentWorkspaceId={currentWorkspaceId}
        currentFolderId={moveFolder.parent_folder_id ?? folderId}
        selectedFolderIds={[moveFolder.id]}
        onMove={async (targetFolderId, targetWorkspaceId) => {
          try {
            await api.patch(`/api/folders/${moveFolder.id}`, {
              parent_folder_id: targetFolderId,
              workspace_id: targetWorkspaceId,
            });
            await reload();
          } catch (err) {
            if (err instanceof ApiError) setError(err.message);
          }
        }}
        onClose={() => setMoveFolder(null)}
      />
    )}

    {dragMove && (
      <MoveItemsModal
        fileCount={dragMove.fileIds.length}
        folderCount={dragMove.folderIds.length}
        workspaces={(workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))}
        currentWorkspaceId={currentWorkspaceId}
        currentFolderId={folderId}
        selectedFolderIds={dragMove.folderIds}
        onMove={async (targetFolderId, targetWorkspaceId) => {
          await moveItemsTo(dragMove.fileIds, dragMove.folderIds, targetFolderId, targetWorkspaceId);
        }}
        onClose={() => setDragMove(null)}
      />
    )}

    {moveConflicts && (
      <MoveConflictModal
        key={moveConflicts.queue[moveConflicts.idx].file.id}
        fileName={moveConflicts.queue[moveConflicts.idx].file.name}
        suggested={moveConflicts.queue[moveConflicts.idx].suggested}
        destLabel={moveConflicts.destLabel}
        position={moveConflicts.idx + 1}
        total={moveConflicts.queue.length}
        onKeepBoth={(name) => void resolveMoveConflict(name)}
        onSkip={() => void resolveMoveConflict(null)}
      />
    )}

    {uploadConflicts && (
      <MoveConflictModal
        key={`${uploadConflicts.idx}-${uploadConflicts.queue[uploadConflicts.idx].file.name}`}
        fileName={uploadConflicts.queue[uploadConflicts.idx].file.name}
        suggested={uploadConflicts.queue[uploadConflicts.idx].suggested}
        destLabel={uploadConflicts.destLabel}
        position={uploadConflicts.idx + 1}
        total={uploadConflicts.queue.length}
        actionLabel="uploading"
        onKeepBoth={(name) => void resolveUploadConflict(name)}
        onSkip={() => void resolveUploadConflict(null)}
      />
    )}

    <WorkspaceCreateModal
      open={wsModalOpen}
      account={accountQuota}
      editing={wsEditing}
      onClose={() => {
        setWsModalOpen(false);
        setWsEditing(null);
      }}
      onSaved={async (ws) => {
        const wasNew = !wsEditing;
        await loadWorkspaces();
        if (wasNew) selectWorkspace(ws.id);
        else void reload(); // name/allocation may have changed
      }}
    />

    {/* Rubber-band selection box — portaled + position:fixed so no
        transformed/zoomed ancestor can offset it. */}
    {marqueeBox && createPortal(
      <div
        className="pointer-events-none fixed z-[90] rounded-sm border border-primary/60 bg-primary/10"
        style={marqueeBox}
      />,
      document.body,
    )}

    {shareTarget && (
      <ShareModal target={shareTarget} onClose={() => setShareTarget(null)} />
    )}
    {portalTarget && (
      <PortalModal target={portalTarget} onClose={() => setPortalTarget(null)} />
    )}

    <CloudImportModal
      open={cloudImportOpen}
      onClose={() => setCloudImportOpen(false)}
      destinationLabel={`Trilli · ${
        folderId != null ? (browse?.folder?.name ?? "Current folder") : (currentWorkspace?.name ?? "Files")
      }`}
      destinationFolderId={folderId}
      destinationWorkspaceId={currentWorkspaceId}
      onImported={() => void reload()}
    />

    {previewIndex !== null && pageFiles[previewIndex] && (
      <FilePreview
        files={pageFiles.map((f) => ({
          id: f.id,
          name: f.name,
          content_type: f.content_type,
          size_bytes: f.size_bytes,
          updated_at: f.updated_at,
        }))}
        index={previewIndex}
        startInEdit={previewEdit}
        onFileSaved={handlePreviewSaved}
        onIndexChange={(i) => {
          setPreviewEdit(false);
          setPreviewIndex(i);
        }}
        onClose={() => {
          setPreviewEdit(false);
          setPreviewIndex(null);
        }}
      />
    )}

    {dialog?.kind === "new-folder" && (
      <NameDialog
        title="New folder"
        label="Folder name"
        confirmLabel="Create"
        placeholder="Untitled folder"
        onSubmit={async (name) => {
          try {
            await api.post<FolderRecord>("/api/folders", {
              name,
              parent_folder_id: folderId,
              workspace_id: folderId == null ? currentWorkspaceId : undefined,
            });
          } catch (e) {
            if (e instanceof ApiError && e.status === 409)
              throw new Error(`A folder named “${name}” already exists here.`);
            throw e;
          }
          await reload();
        }}
        onClose={() => setDialog(null)}
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
          await reload();
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
          await reload();
        }}
        onClose={() => setDialog(null)}
      />
    )}

    {dialog?.kind === "duplicate-file" && (
      <NameDialog
        title="Duplicate in place"
        label="Name for the copy"
        initialValue={copyDefaultName(dialog.file.name, false)}
        confirmLabel="Duplicate"
        allowUnchanged
        onSubmit={async (name) => {
          await runCopy({
            file_ids: [dialog.file.id],
            dest_folder_id: folderId,
            dest_workspace_id: currentWorkspaceId,
            name,
          });
        }}
        onClose={() => setDialog(null)}
      />
    )}

    {dialog?.kind === "duplicate-folder" && (
      <NameDialog
        title="Duplicate in place"
        label="Name for the copy"
        initialValue={copyDefaultName(dialog.folder.name, true)}
        confirmLabel="Duplicate"
        allowUnchanged
        onSubmit={async (name) => {
          await runCopy({
            folder_ids: [dialog.folder.id],
            dest_folder_id: folderId,
            dest_workspace_id: currentWorkspaceId,
            name,
          });
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
          await reload();
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
          await reload();
          emitTrashChanged();
        }}
        onClose={() => setDialog(null)}
      />
    )}

    {dialog?.kind === "trash-bulk" && (
      <ConfirmDialog
        title={`Move ${dialog.count} item${dialog.count === 1 ? "" : "s"} to trash?`}
        danger
        confirmLabel="Move to trash"
        message="The selected items will be moved to the trash. You can restore them later from the Trash bin."
        onConfirm={async () => {
          await Promise.all([
            ...selectedFiles.map((id) => api.delete(`/api/files/${id}`)),
            ...selectedFolders.map((id) => api.delete(`/api/folders/${id}`)),
          ]);
          clearSelection();
          await reload();
          await refresh({ silent: true });
          emitTrashChanged();
        }}
        onClose={() => setDialog(null)}
      />
    )}
    </>
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

function FileUploadZone({
  onPick,
  uploading,
  dragOver,
  onDragOver,
  onDragLeave,
  onDrop,
  inputRef,
  maxBytes,
  folderName,
  compact = false,
}: {
  onPick: (files: FileList | File[]) => void;
  uploading: boolean;
  dragOver: boolean;
  onDragOver: (e: React.DragEvent<HTMLDivElement>) => void;
  onDragLeave: () => void;
  onDrop: (e: React.DragEvent<HTMLDivElement>) => void;
  inputRef: React.MutableRefObject<HTMLInputElement | null>;
  maxBytes: number;
  folderName?: string;
  /** Slim one-line strip once the folder has content — the full-height
      billboard is only earned by an empty folder. Dropping/clicking works
      identically in both shapes (and page-wide drop still works). */
  compact?: boolean;
}) {
  const maxLabel =
    maxBytes > 0
      ? maxBytes >= Number.MAX_SAFE_INTEGER
        ? "No individual file size restrictions. Fair use applies."
        : `Max ${formatBytes(maxBytes)} per file`
      : "";

  const picker = (
    <input
      ref={inputRef}
      type="file"
      multiple
      className="hidden"
      disabled={uploading}
      onChange={(e) => {
        if (e.target.files) {
          onPick(e.target.files);
          e.target.value = "";
        }
      }}
    />
  );

  if (compact) {
    return (
      <div
        onDragOver={onDragOver}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        className={cn(
          "relative flex cursor-pointer items-center justify-center gap-2.5 rounded-xl border-2 border-dashed px-4 py-2.5 transition-all duration-300",
          dragOver
            ? "border-primary bg-primary/10"
            : "border-border hover:border-primary/50 hover:bg-card/50",
        )}
      >
        {picker}
        <span
          className={cn(
            "flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-lg transition-colors",
            dragOver ? "bg-primary text-primary-foreground" : "bg-secondary text-muted-foreground",
          )}
        >
          <Upload className="h-4 w-4" />
        </span>
        <span className="text-sm font-medium text-foreground">
          {uploading
            ? "Uploading…"
            : dragOver
              ? "Drop files here"
              : `Drop files anywhere or click to browse${folderName ? ` (into ${folderName})` : ""}`}
        </span>
        {maxLabel && !uploading && !dragOver && (
          <span className="hidden text-xs text-muted-foreground sm:inline">· {maxLabel}</span>
        )}
      </div>
    );
  }

  return (
    <div
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
      onClick={() => inputRef.current?.click()}
      className={cn(
        "relative flex cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed p-8 transition-all duration-300",
        dragOver
          ? "scale-[1.02] border-primary bg-primary/10"
          : "border-border hover:border-primary/50 hover:bg-card/50",
      )}
    >
      {picker}
      <div
        className={cn(
          "mb-4 flex h-16 w-16 items-center justify-center rounded-2xl transition-all duration-300",
          dragOver
            ? "scale-110 bg-primary text-primary-foreground"
            : "bg-secondary text-muted-foreground group-hover:bg-primary/20 group-hover:text-primary",
        )}
      >
        <Upload className="h-8 w-8" />
      </div>
      <h3 className="mb-1 text-lg font-semibold text-foreground">
        {uploading
          ? "Uploading…"
          : dragOver
            ? "Drop files here"
            : `Drop files here or click to browse${folderName ? ` (into ${folderName})` : ""}`}
      </h3>
      {maxLabel && <p className="text-sm text-muted-foreground">{maxLabel}</p>}
    </div>
  );
}

// Compact pager beneath the file list. Shown only when the current folder holds
// more than one page of items.
