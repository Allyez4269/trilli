import { Fragment, useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowDownLeft,
  ArrowUpRight,
  Check,
  Copy,
  Download,
  Edit3,
  ExternalLink,
  Eye,
  File as FileIcon,
  Folder as FolderIcon,
  Globe,
  Loader2,
  MoreHorizontal,
  Move,
  Share2,
  Star,
  Users,
  type LucideIcon,
} from "lucide-react";

import {
  api,
  downloadUrl,
  type Portal,
  type Share,
  type WorkspacesResponse,
} from "@/lib/api";
import { Ban, Inbox, Lock, Pencil, Power, Trash2 } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { appKeyForFile, isEditable } from "@/lib/productivity/edit";
import PortalModal, { type PortalTarget } from "@/components/PortalModal";
import ShareModal, { type ShareTarget } from "@/components/ShareModal";
import MoveItemsModal from "@/components/SelectionActionsModal";
import FilePreview, { printUrlFor } from "@/components/FilePreview";
import { NameDialog, ConfirmDialog } from "@/components/Dialogs";
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
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { emitTrashChanged } from "@/lib/events";
import { setFileDownloadDrag } from "@/lib/drag";
import { cn } from "@/lib/utils";

function statusOf(s: Share): { label: string; tone: string } {
  if (s.revoked_at) return { label: "Off", tone: "bg-muted text-muted-foreground" };
  if (s.expires_at && new Date(s.expires_at) < new Date())
    return { label: "Expired", tone: "bg-amber-500/15 text-amber-700" };
  return { label: "Active", tone: "bg-emerald-500/15 text-emerald-700" };
}

export default function Shared() {
  const { refresh } = useAuth();
  const [shares, setShares] = useState<Share[] | null>(null);
  const [portals, setPortals] = useState<Portal[]>([]);
  const [copied, setCopied] = useState<number | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [pCopied, setPCopied] = useState<number | null>(null);
  const [pBusy, setPBusy] = useState<number | null>(null);
  const [editingPortal, setEditingPortal] = useState<Portal | null>(null);
  // Item-action state — mirrors My Files so a shared item gets the full menu.
  const [shareTarget, setShareTarget] = useState<ShareTarget | null>(null);
  const [portalTarget, setPortalTarget] = useState<PortalTarget | null>(null);
  const [moveFile, setMoveFile] = useState<{ id: number; name: string } | null>(null);
  const [previewFile, setPreviewFile] = useState<{ id: number; name: string } | null>(null);
  const [workspaces, setWorkspaces] = useState<{ id: number; name: string }[]>([]);
  const [dialog, setDialog] = useState<
    | { kind: "rename-file"; id: number; name: string }
    | { kind: "rename-folder"; id: number; name: string }
    | { kind: "trash-file"; id: number; name: string }
    | { kind: "trash-folder"; id: number; name: string }
    | null
  >(null);

  const load = useCallback(async () => {
    const [s, p] = await Promise.all([
      api.get<{ shares: Share[] }>("/api/shares"),
      api.get<{ portals: Portal[] }>("/api/portals"),
    ]);
    setShares(s.shares ?? []);
    setPortals(p.portals ?? []);
  }, []);

  const copyPortal = async (p: Portal) => {
    try {
      await navigator.clipboard.writeText(`${window.location.origin}${p.url}`);
      setPCopied(p.id);
      setTimeout(() => setPCopied((c) => (c === p.id ? null : c)), 1800);
    } catch {
      /* clipboard blocked */
    }
  };

  const disablePortal = async (p: Portal) => {
    setPBusy(p.id);
    try {
      await api.patch(`/api/portals/${p.id}`, { revoked: true });
      await load();
    } finally {
      setPBusy(null);
    }
  };

  const enablePortal = async (p: Portal) => {
    setPBusy(p.id);
    try {
      await api.patch(`/api/portals/${p.id}`, { revoked: false });
      await load();
    } finally {
      setPBusy(null);
    }
  };

  const deletePortal = async (p: Portal) => {
    setPBusy(p.id);
    try {
      await api.delete(`/api/portals/${p.id}`);
      await load();
    } finally {
      setPBusy(null);
    }
  };

  useEffect(() => {
    void load();
  }, [load]);

  // Workspaces for the Move picker (loaded once).
  useEffect(() => {
    void api
      .get<WorkspacesResponse>("/api/workspaces")
      .then((r) => setWorkspaces((r.workspaces ?? []).map((w) => ({ id: w.id, name: w.name }))))
      .catch(() => {});
  }, []);

  // ----- item actions (same set as My Files; gated by ownership/type) -------
  const itemPreview = (s: Share) => {
    if (s.file_id) setPreviewFile({ id: s.file_id, name: s.resource_name });
  };
  const itemShare = (s: Share) =>
    setShareTarget({
      kind: s.resource_type,
      id: (s.resource_type === "folder" ? s.folder_id : s.file_id) ?? 0,
      name: s.resource_name,
    });
  const itemRequestFiles = (s: Share) => {
    if (s.folder_id) setPortalTarget({ folderId: s.folder_id, folderName: s.resource_name });
  };
  const itemStar = async (s: Share) => {
    try {
      if (s.resource_type === "folder" && s.folder_id)
        await api.patch(`/api/folders/${s.folder_id}`, { starred: true });
      else if (s.file_id) await api.patch(`/api/files/${s.file_id}`, { starred: true });
    } catch {
      /* best-effort */
    }
  };
  const itemRename = (s: Share) => {
    if (s.resource_type === "folder" && s.folder_id)
      setDialog({ kind: "rename-folder", id: s.folder_id, name: s.resource_name });
    else if (s.file_id) setDialog({ kind: "rename-file", id: s.file_id, name: s.resource_name });
  };
  const itemMove = (s: Share) => {
    if (s.file_id) setMoveFile({ id: s.file_id, name: s.resource_name });
  };
  const itemTrash = (s: Share) => {
    if (s.resource_type === "folder" && s.folder_id)
      setDialog({ kind: "trash-folder", id: s.folder_id, name: s.resource_name });
    else if (s.file_id) setDialog({ kind: "trash-file", id: s.file_id, name: s.resource_name });
  };

  const copyLink = async (s: Share) => {
    try {
      await navigator.clipboard.writeText(`${window.location.origin}${s.url}`);
      setCopied(s.id);
      setTimeout(() => setCopied((c) => (c === s.id ? null : c)), 1800);
    } catch {
      /* clipboard blocked */
    }
  };

  const revoke = async (s: Share) => {
    setBusy(s.id);
    try {
      await api.delete(`/api/shares/${s.id}`);
      await load();
    } finally {
      setBusy(null);
    }
  };

  const deleteShare = async (s: Share) => {
    setBusy(s.id);
    try {
      await api.delete(`/api/shares/${s.id}?hard=1`);
      await load();
    } finally {
      setBusy(null);
    }
  };

  const allShares = shares ?? [];
  const links = allShares.filter((s) => s.grantee_type === "public");
  const people = groupByPerson(allShares);
  const itemCount = new Set(
    allShares.map((s) => `${s.resource_type}:${s.resource_name}`),
  ).size;

  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-5xl space-y-6">
        <header>
          <h1 className="flex items-center gap-2 text-3xl font-semibold">
            <Share2 className="h-7 w-7 text-primary" />
            Shared
          </h1>
          <p className="text-muted-foreground">
            The whole picture in one place — what you're sharing, how it's
            shared, and who has access.
          </p>
        </header>

        {shares === null ? (
          <SharedSkeleton />
        ) : (
          <>
            {/* At-a-glance summary — the "who / what / how" totals. */}
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat icon={Users} label="People" value={people.length} />
              <Stat icon={FileIcon} label="Items" value={itemCount} />
              <Stat icon={Globe} label="Public links" value={links.length} />
              <Stat icon={Inbox} label="Drop portals" value={portals.length} />
            </div>

            {/* Every share — name (what), shared-with (who), access (how). */}
            <section>
              <h2 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Shares — what, who &amp; how
              </h2>
              <ShareTable
                rows={allShares}
                copied={copied}
                busy={busy}
                onCopy={copyLink}
                onRevoke={revoke}
                onDelete={deleteShare}
                onPreview={itemPreview}
                onShareItem={itemShare}
                onRequestFiles={itemRequestFiles}
                onStar={itemStar}
                onRename={itemRename}
                onMove={itemMove}
                onTrash={itemTrash}
                showDirection
                emptyText="You haven't shared anything yet, and nothing's been shared with you. Open a file or folder's ⋯ menu and choose Share."
              />
            </section>

            {/* Drop portals — inbound uploads (another way you share access).
                Extra top margin to separate it more clearly from the shares table. */}
            <section className="pt-4">
              <h2 className="mb-2 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                <Inbox className="h-3.5 w-3.5 text-primary" /> Drop portals — request uploads
              </h2>
              <PortalTable
                rows={portals}
                copied={pCopied}
                busy={pBusy}
                onCopy={copyPortal}
                onEdit={setEditingPortal}
                onDisable={disablePortal}
                onEnable={enablePortal}
                onDelete={deletePortal}
              />
            </section>
          </>
        )}
      </div>

      {editingPortal && (
        <PortalModal
          editing={editingPortal}
          onClose={() => setEditingPortal(null)}
          onSaved={() => {
            setEditingPortal(null);
            void load();
          }}
        />
      )}

      {previewFile && (
        <FilePreview
          files={[{ id: previewFile.id, name: previewFile.name }]}
          index={0}
          onIndexChange={() => {}}
          onClose={() => setPreviewFile(null)}
        />
      )}

      {shareTarget && (
        <ShareModal target={shareTarget} onClose={() => setShareTarget(null)} />
      )}

      {portalTarget && (
        <PortalModal target={portalTarget} onClose={() => setPortalTarget(null)} />
      )}

      {moveFile && (
        <MoveItemsModal
          fileCount={1}
          folderCount={0}
          workspaces={workspaces}
          currentWorkspaceId={null}
          currentFolderId={null}
          selectedFolderIds={[]}
          onMove={async (targetFolderId, targetWorkspaceId) => {
            try {
              await api.patch(`/api/files/${moveFile.id}`, {
                folder_id: targetFolderId,
                workspace_id: targetWorkspaceId,
              });
              await load();
            } catch {
              /* surfaced inline elsewhere */
            }
          }}
          onClose={() => setMoveFile(null)}
        />
      )}

      {dialog?.kind === "rename-file" && (
        <NameDialog
          title="Rename file"
          label="File name"
          initialValue={dialog.name}
          confirmLabel="Rename"
          onSubmit={async (name) => {
            await api.patch(`/api/files/${dialog.id}`, { name });
            await load();
          }}
          onClose={() => setDialog(null)}
        />
      )}

      {dialog?.kind === "rename-folder" && (
        <NameDialog
          title="Rename folder"
          label="Folder name"
          initialValue={dialog.name}
          confirmLabel="Rename"
          onSubmit={async (name) => {
            await api.patch(`/api/folders/${dialog.id}`, { name });
            await load();
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
              “{dialog.name}” will be moved to the trash. You can restore it later
              from the Trash bin.
            </>
          }
          onConfirm={async () => {
            await api.delete(`/api/files/${dialog.id}`);
            await load();
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
              “{dialog.name}” and everything inside it will be moved to the trash.
              You can restore the folder later.
            </>
          }
          onConfirm={async () => {
            await api.delete(`/api/folders/${dialog.id}`);
            await load();
            emitTrashChanged();
          }}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}

// SharedSkeleton mirrors the loaded layout — the four summary tiles plus the
// shares and drop-portal tables — so the page settles in place instead of
// popping in. Matches the skeleton style used across the other sections.
function SharedSkeleton() {
  return (
    <>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <div
            key={i}
            className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3"
          >
            <Skeleton className="h-9 w-9 flex-shrink-0 rounded-lg" />
            <div className="space-y-2">
              <Skeleton className="h-3 w-14" />
              <Skeleton className="h-5 w-8" />
            </div>
          </div>
        ))}
      </div>
      {[0, 1].map((section) => (
        <section key={section} className={section === 1 ? "pt-4" : undefined}>
          <Skeleton className="mb-2 h-3 w-52" />
          <div className="overflow-hidden rounded-xl border border-border bg-card">
            {Array.from({ length: 3 }).map((_, i) => (
              <div
                key={i}
                className="flex items-center gap-3 border-b border-border px-4 py-3.5 last:border-0"
              >
                <Skeleton className="h-5 w-5 flex-shrink-0 rounded" />
                <Skeleton className="h-4 w-40" />
                <Skeleton className="ml-auto hidden h-4 w-24 sm:block" />
                <Skeleton className="h-4 w-16" />
              </div>
            ))}
          </div>
        </section>
      ))}
    </>
  );
}

// Stat is a compact metric tile used in the Shared summary row.
function Stat({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Users;
  label: string;
  value: number;
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3">
      <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
        <Icon className="h-4 w-4" />
      </span>
      <div className="min-w-0">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="text-lg font-semibold tabular-nums text-foreground">{value}</p>
      </div>
    </div>
  );
}

function IconBtn({
  title,
  onClick,
  children,
  danger,
  disabled,
}: {
  title: string;
  onClick: () => void;
  children: React.ReactNode;
  danger?: boolean;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "rounded p-1.5 text-muted-foreground disabled:opacity-50",
        danger ? "hover:bg-destructive/10 hover:text-destructive" : "hover:bg-muted hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

// RowAct describes one entry in a share row's item menu (mirrors My Files).
type RowAct = {
  key: string;
  label: string;
  Icon: LucideIcon;
  onSelect?: () => void;
  href?: string;
  destructive?: boolean;
  sep?: boolean; // render a separator before this item
};

function ShareTable({
  rows,
  copied,
  busy,
  onCopy,
  onRevoke,
  onDelete,
  onPreview,
  onShareItem,
  onRequestFiles,
  onStar,
  onRename,
  onMove,
  onTrash,
  showDirection,
  emptyText,
}: {
  rows: Share[];
  copied: number | null;
  busy: number | null;
  onCopy: (s: Share) => void;
  onRevoke: (s: Share) => void;
  onDelete: (s: Share) => void;
  onPreview: (s: Share) => void;
  onShareItem: (s: Share) => void;
  onRequestFiles: (s: Share) => void;
  onStar: (s: Share) => void;
  onRename: (s: Share) => void;
  onMove: (s: Share) => void;
  onTrash: (s: Share) => void;
  showDirection: boolean;
  emptyText?: string;
}) {
  const navigate = useNavigate();
  if (rows.length === 0) {
    return (
      <Placeholder
        icon={Share2}
        title="Nothing here yet"
        body={emptyText ?? "When you share a file or folder, it shows up here."}
      />
    );
  }
  return (
    <div className="overflow-hidden rounded-xl border border-border">
      {/* table-fixed + a shared colgroup so this table aligns column-for-column
          with the drop-portals table below it (Access↔Password, Expires↔Uploads,
          Opens↔Expires, Status↔Status, Action↔Action). */}
      <table className="w-full table-fixed text-[13px]">
        <colgroup>
          <col className="w-[22%]" />
          <col className="w-[20%]" />
          <col className="w-[12%]" />
          <col className="w-[12%]" />
          <col className="w-[10%]" />
          <col className="w-[12%]" />
          <col className="w-[12%]" />
        </colgroup>
        <thead className="bg-card">
          <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
            <th className="px-4 py-2.5 font-medium">Name</th>
            <th className="px-3 py-2.5 font-medium">Shared with</th>
            <th className="px-3 py-2.5 font-medium">Access</th>
            <th className="px-3 py-2.5 font-medium">Expires</th>
            <th className="px-3 py-2.5 font-medium">Downloads</th>
            <th className="px-3 py-2.5 font-medium">Status</th>
            <th className="px-3 py-2.5 text-right font-medium">Action</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((s) => {
            const st = statusOf(s);
            const live = !s.revoked_at && !(s.expires_at && new Date(s.expires_at) < new Date());
            const isOut = s.direction === "out";
            // Public + email shares are link-based, so they can be opened/copied.
            // Member shares grant in-app access and have no shareable link.
            const linkable =
              (s.grantee_type === "public" || s.grantee_type === "email") && !!s.url;
            // Item menu — full set on items you own (outbound); inbound shares
            // only get Preview/Download since you don't own the underlying file.
            const isFolder = s.resource_type === "folder";
            const acts: RowAct[] = [];
            if (!isFolder && s.file_id) {
              acts.push({
                key: "preview",
                label: "Preview",
                Icon: Eye,
                onSelect: () => {
                  const url = printUrlFor({ id: s.file_id, name: s.resource_name });
                  if (url) window.open(url, "_blank", "noopener");
                },
              });
              if (isEditable(s.resource_name)) {
                acts.push({
                  key: "edit",
                  label: "Edit",
                  Icon: Edit3,
                  onSelect: () => navigate(`/apps/productivity/${appKeyForFile(s.resource_name)}`, { state: { fileId: s.file_id } }),
                });
              }
              acts.push({ key: "download", label: "Download", Icon: Download, href: downloadUrl(s.file_id) });
            }
            if (isOut) {
              acts.push({ key: "share", label: "Share", Icon: Share2, onSelect: () => onShareItem(s) });
              if (isFolder)
                acts.push({ key: "request", label: "Request files", Icon: Inbox, onSelect: () => onRequestFiles(s) });
              acts.push({ key: "star", label: "Star", Icon: Star, onSelect: () => onStar(s), sep: true });
              acts.push({ key: "rename", label: "Rename", Icon: Pencil, onSelect: () => onRename(s) });
              if (!isFolder)
                acts.push({ key: "move", label: "Move", Icon: Move, onSelect: () => onMove(s) });
              acts.push({
                key: "trash",
                label: "Move to trash",
                Icon: Trash2,
                onSelect: () => onTrash(s),
                destructive: true,
                sep: true,
              });
            }
            return (
              <ContextMenu key={s.id}>
                <ContextMenuTrigger asChild>
                  <tr
                    draggable={!isFolder && !!s.file_id}
                    onDragStart={(e) => {
                      if (!isFolder && s.file_id)
                        setFileDownloadDrag(e, { id: s.file_id, name: s.resource_name });
                    }}
                    className={cn(
                      "border-b border-border last:border-0 hover:bg-muted/30",
                      !isFolder && s.file_id && "cursor-grab active:cursor-grabbing",
                    )}
                  >
                <td className="px-4 py-2.5">
                  <span className="flex min-w-0 items-center gap-2">
                    {s.resource_type === "folder" ? (
                      <FolderIcon className="h-4 w-4 flex-shrink-0 text-primary/70" />
                    ) : (
                      <FileIcon className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                    )}
                    <span className="truncate">{s.resource_name}</span>
                    {showDirection &&
                      (s.direction === "out" ? (
                        <ArrowUpRight className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                      ) : (
                        <ArrowDownLeft className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                      ))}
                  </span>
                </td>
                <td className="truncate px-3 py-2.5 text-muted-foreground">
                  {s.grantee_type === "public" ? (
                    <span className="inline-flex items-center gap-1.5">
                      <Globe className="h-3.5 w-3.5 flex-shrink-0" /> Anyone with link
                    </span>
                  ) : (
                    s.grantee_name || s.grantee_email || "Member"
                  )}
                </td>
                <td className="px-3 py-2.5 capitalize text-muted-foreground">
                  {s.capability === "download" ? "Download" : "View"}
                  {s.has_password ? " · 🔒" : ""}
                </td>
                <td className="px-3 py-2.5 text-muted-foreground">
                  {s.expires_at ? new Date(s.expires_at).toLocaleDateString() : "—"}
                </td>
                <td className="px-3 py-2.5 tabular-nums text-muted-foreground">{s.access_count}</td>
                <td className="px-3 py-2.5">
                  <span className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", st.tone)}>{st.label}</span>
                </td>
                <td className="px-3 py-2.5">
                  <div className="flex items-center justify-end gap-0.5">
                    {live && linkable && (
                      <>
                        <IconBtn
                          title="Open link"
                          onClick={() =>
                            window.open(`${window.location.origin}${s.url}`, "_blank", "noopener")
                          }
                        >
                          <ExternalLink className="h-3.5 w-3.5" />
                        </IconBtn>
                        <IconBtn title="Copy link" onClick={() => onCopy(s)}>
                          {copied === s.id ? <Check className="h-3.5 w-3.5 text-emerald-600" /> : <Copy className="h-3.5 w-3.5" />}
                        </IconBtn>
                      </>
                    )}
                    {live && isOut && (
                      <IconBtn title="Turn off" danger onClick={() => onRevoke(s)} disabled={busy === s.id}>
                        {busy === s.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Ban className="h-3.5 w-3.5" />}
                      </IconBtn>
                    )}
                    {isOut && (
                      <IconBtn title="Delete permanently" danger onClick={() => onDelete(s)} disabled={busy === s.id}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </IconBtn>
                    )}
                    {!isOut && !linkable && acts.length === 0 && (
                      <span className="text-muted-foreground/40">—</span>
                    )}
                    {acts.length > 0 && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-muted-foreground hover:text-foreground"
                            title="More"
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-48">
                          {acts.map((act) => (
                            <Fragment key={act.key}>
                              {act.sep && <DropdownMenuSeparator />}
                              {act.href ? (
                                <DropdownMenuItem asChild>
                                  <a href={act.href}>
                                    <act.Icon className="mr-2 h-4 w-4" />
                                    {act.label}
                                  </a>
                                </DropdownMenuItem>
                              ) : (
                                <DropdownMenuItem
                                  variant={act.destructive ? "destructive" : "default"}
                                  onSelect={act.onSelect}
                                >
                                  <act.Icon className="mr-2 h-4 w-4" />
                                  {act.label}
                                </DropdownMenuItem>
                              )}
                            </Fragment>
                          ))}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </div>
                </td>
                  </tr>
                </ContextMenuTrigger>
                {acts.length > 0 && (
                  <ContextMenuContent className="w-48">
                    {acts.map((act) => (
                      <Fragment key={act.key}>
                        {act.sep && <ContextMenuSeparator />}
                        {act.href ? (
                          <ContextMenuItem asChild>
                            <a href={act.href}>
                              <act.Icon className="mr-2 h-4 w-4" />
                              {act.label}
                            </a>
                          </ContextMenuItem>
                        ) : (
                          <ContextMenuItem
                            variant={act.destructive ? "destructive" : "default"}
                            onSelect={act.onSelect}
                          >
                            <act.Icon className="mr-2 h-4 w-4" />
                            {act.label}
                          </ContextMenuItem>
                        )}
                      </Fragment>
                    ))}
                  </ContextMenuContent>
                )}
              </ContextMenu>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function portalStatus(p: Portal): { label: string; tone: string } {
  if (p.revoked_at) return { label: "Off", tone: "bg-muted text-muted-foreground" };
  if (p.expires_at && new Date(p.expires_at) < new Date())
    return { label: "Expired", tone: "bg-amber-500/15 text-amber-700" };
  return { label: "Active", tone: "bg-emerald-500/15 text-emerald-700" };
}

function PortalTable({
  rows,
  copied,
  busy,
  onCopy,
  onEdit,
  onTerminate,
  onDelete,
}: {
  rows: Portal[];
  copied: number | null;
  busy: number | null;
  onCopy: (p: Portal) => void;
  onEdit: (p: Portal) => void;
  onDisable: (p: Portal) => void;
  onEnable: (p: Portal) => void;
  onDelete: (p: Portal) => void;
}) {
  if (rows.length === 0) {
    return (
      <Placeholder
        icon={Inbox}
        title="No drop portals yet"
        body="Open a folder’s ⋯ menu and choose “Request files” to let others upload into it."
      />
    );
  }
  return (
    <div className="overflow-hidden rounded-xl border border-border">
      {/* Same colgroup geometry as the shares table so the columns line up:
          Request spans Name+Shared-with, then Password↔Access, Uploads↔Expires,
          Expires↔Opens, Status↔Status, Action↔Action. */}
      <table className="w-full table-fixed text-[13px]">
        <colgroup>
          <col className="w-[42%]" />
          <col className="w-[12%]" />
          <col className="w-[12%]" />
          <col className="w-[10%]" />
          <col className="w-[12%]" />
          <col className="w-[12%]" />
        </colgroup>
        <thead className="bg-card">
          <tr className="border-b border-border text-left text-[11px] uppercase tracking-wide text-muted-foreground">
            <th className="px-4 py-2.5 font-medium">Request</th>
            <th className="px-3 py-2.5 font-medium">Password</th>
            <th className="px-3 py-2.5 font-medium">Uploads</th>
            <th className="px-3 py-2.5 font-medium">Expires</th>
            <th className="px-3 py-2.5 font-medium">Status</th>
            <th className="px-3 py-2.5 text-right font-medium">Action</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => {
            const st = portalStatus(p);
            const live = !p.revoked_at && !(p.expires_at && new Date(p.expires_at) < new Date());
            return (
              <tr key={p.id} className="border-b border-border last:border-0 hover:bg-muted/30">
                <td className="px-4 py-2.5">
                  <span className="flex min-w-0 items-center gap-2">
                    <Inbox className="h-4 w-4 flex-shrink-0 text-primary/70" />
                    <span className="truncate">{p.title || "File request"}</span>
                  </span>
                </td>
                <td className="px-3 py-2.5 text-muted-foreground">
                  {p.has_password ? (
                    <span className="inline-flex items-center gap-1">
                      <Lock className="h-3.5 w-3.5" /> Yes
                    </span>
                  ) : (
                    "No"
                  )}
                </td>
                <td className="px-3 py-2.5 tabular-nums text-muted-foreground">{p.upload_count}</td>
                <td className="px-3 py-2.5 text-muted-foreground">
                  {p.expires_at ? new Date(p.expires_at).toLocaleDateString() : "—"}
                </td>
                <td className="px-3 py-2.5">
                  <span className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", st.tone)}>{st.label}</span>
                </td>
                <td className="px-3 py-2.5">
                  <div className="flex items-center justify-end gap-0.5">
                    {live ? (
                      <>
                        <IconBtn title="Copy link" onClick={() => onCopy(p)}>
                          {copied === p.id ? <Check className="h-3.5 w-3.5 text-emerald-600" /> : <Copy className="h-3.5 w-3.5" />}
                        </IconBtn>
                        <IconBtn title="Edit" onClick={() => onEdit(p)}>
                          <Pencil className="h-3.5 w-3.5" />
                        </IconBtn>
                        <IconBtn title="Disable" danger onClick={() => onDisable(p)} disabled={busy === p.id}>
                          {busy === p.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Ban className="h-3.5 w-3.5" />}
                        </IconBtn>
                      </>
                    ) : (
                      !p.revoked_at ? null : (
                        <IconBtn title="Enable" onClick={() => onEnable(p)} disabled={busy === p.id}>
                          {busy === p.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Power className="h-3.5 w-3.5" />}
                        </IconBtn>
                      )
                    )}
                    <IconBtn title="Delete permanently" danger onClick={() => onDelete(p)} disabled={busy === p.id}>
                      <Trash2 className="h-3.5 w-3.5" />
                    </IconBtn>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

type PersonGroup = {
  key: string;
  name: string;
  kind: "member" | "email" | "public";
  out: Share[];
  in: Share[];
};

// groupByPerson collapses shares into one row per collaborator. Member shares
// merge both directions by user id (key "u:<id>"), so "you ⇄ them" is one row.
function groupByPerson(shares: Share[]): PersonGroup[] {
  const map = new Map<string, PersonGroup>();
  const ensure = (key: string, name: string, kind: PersonGroup["kind"]) => {
    let g = map.get(key);
    if (!g) {
      g = { key, name, kind, out: [], in: [] };
      map.set(key, g);
    } else if (name && (!g.name || g.name === "A teammate")) {
      g.name = name;
    }
    return g;
  };
  for (const s of shares) {
    if (s.direction === "in") {
      ensure(`u:${s.created_by_id}`, s.created_by_name || "A teammate", "member").in.push(s);
    } else if (s.grantee_type === "public") {
      ensure("public", "Anyone with the link", "public").out.push(s);
    } else if (s.grantee_type === "email") {
      ensure(`e:${s.grantee_email}`, s.grantee_email || "Guest", "email").out.push(s);
    } else {
      ensure(`u:${s.grantee_user_id}`, s.grantee_name || "Member", "member").out.push(s);
    }
  }
  // People with shares first, "Anyone with the link" last.
  return [...map.values()].sort((a, b) => (a.kind === "public" ? 1 : 0) - (b.kind === "public" ? 1 : 0));
}

function Placeholder({ icon: Icon, title, body }: { icon: typeof Users; title: string; body: string }) {
  return (
    <div className="rounded-xl border border-dashed border-border py-16 text-center">
      <Icon className="mx-auto h-8 w-8 text-muted-foreground/50" />
      <p className="mt-3 text-sm font-medium text-foreground">{title}</p>
      <p className="mx-auto mt-1 max-w-md text-[13px] text-muted-foreground">{body}</p>
    </div>
  );
}
