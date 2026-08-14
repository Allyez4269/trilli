import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { HardDrive, Layers, Loader2, Search, Users, X } from "lucide-react";

import { api, ApiError, type AccountQuota, type Workspace } from "@/lib/api";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { WorkspaceFolderPicker } from "@/components/WorkspaceFolderPicker";
import { cn } from "@/lib/utils";

const GB = 1024 * 1024 * 1024;
const TB = 1024 * GB;
const fmtGB = (bytes: number) => (bytes / GB).toFixed(bytes % GB === 0 ? 0 : 2);

// A plan is "unlimited" when its pool is the backend's MaxInt64 sentinel
// (≈9.22e18 bytes ≈ 8.6 billion GB). Any real plan cap is single/low-double-digit
// TB, so anything past an exabyte is the unlimited plan — show words, not a number.
const UNLIMITED_BYTES = 1e18;

// Human-friendly capacity: TB once we're into terabytes, else GB; trims a bare .0.
const fmtSpace = (bytes: number) => {
  const [val, unit] = bytes >= TB ? [bytes / TB, "TB"] : [bytes / GB, "GB"];
  return `${val.toFixed(Number.isInteger(val) ? 0 : 1)} ${unit}`;
};

// One member/viewer's access to this workspace (GET /api/workspaces/{id}/access).
interface MemberAccess {
  user_id: number;
  email: string;
  full_name?: string | null;
  role: string;
  whole: boolean;
  folder_ids: number[];
}

const displayName = (m: MemberAccess) => m.full_name?.trim() || m.email.split("@")[0];
const accessMode = (m: MemberAccess): "full" | "partial" | "none" =>
  m.whole ? "full" : m.folder_ids.length > 0 ? "partial" : "none";

// WorkspaceCreateModal: owner/admin create a new workspace — or, when `editing`
// is set, rename / re-allocate an existing one. Disk is carved from the
// account's remaining storage pool.
export function WorkspaceCreateModal({
  open,
  account,
  editing,
  onClose,
  onSaved,
}: {
  open: boolean;
  account: AccountQuota | null;
  editing?: Workspace | null;
  onClose: () => void;
  onSaved: (ws: Workspace) => void;
}) {
  const isEdit = !!editing;
  const [name, setName] = useState("");
  const [allocGB, setAllocGB] = useState("1");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Inline access control (edit mode): member/viewer roster + each one's access
  // to THIS workspace. Left pane picks a member; right pane edits their access
  // (whole / folder-scoped) scoped to this workspace only. Changes apply live.
  const [roster, setRoster] = useState<MemberAccess[] | null>(null);
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null);
  const [memberQuery, setMemberQuery] = useState("");

  useEffect(() => {
    if (open) {
      setName(editing?.name ?? "");
      setAllocGB(editing ? fmtGB(editing.disk_allocation_bytes) : "1");
      setBusy(false);
      setError(null);
    }
  }, [open, editing]);

  // Load the access roster (member/viewer access to this workspace) on open.
  useEffect(() => {
    if (!open || !editing) {
      setRoster(null);
      setSelectedUserId(null);
      setMemberQuery("");
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const res = await api.get<{ members: MemberAccess[] }>(`/api/workspaces/${editing.id}/access`);
        if (cancelled) return;
        setRoster(res.members ?? []);
        setSelectedUserId((res.members ?? [])[0]?.user_id ?? null);
      } catch {
        if (!cancelled) setRoster([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, editing]);

  // Persist a member's access to this workspace (whole or folder-scoped), scoped
  // to this workspace only. Optimistic; reloads the roster on failure to resync.
  const applyAccess = async (userId: number, whole: boolean, folderIds: number[]) => {
    if (!editing) return;
    setRoster((prev) =>
      prev?.map((m) =>
        m.user_id === userId ? { ...m, whole, folder_ids: folderIds } : m,
      ) ?? prev,
    );
    try {
      await api.patch(`/api/workspaces/${editing.id}/members/${userId}/access`, {
        whole,
        folder_ids: folderIds,
      });
    } catch {
      try {
        const res = await api.get<{ members: MemberAccess[] }>(`/api/workspaces/${editing.id}/access`);
        setRoster(res.members ?? []);
      } catch {
        /* leave optimistic state */
      }
    }
  };

  const selectedMember = roster?.find((m) => m.user_id === selectedUserId) ?? null;

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  // When editing, this workspace's current reservation is freed back into the
  // pool, and it can't be shrunk below what it already uses.
  const remaining = (account?.remaining_bytes ?? 0) + (editing?.disk_allocation_bytes ?? 0);
  const unlimited = (account?.total_bytes ?? 0) >= UNLIMITED_BYTES;
  const minBytes = editing?.storage_bytes_used ?? 0;
  const allocBytes = Math.round((parseFloat(allocGB) || 0) * GB);
  const overPool = allocBytes > remaining;
  const belowUsage = allocBytes < minBytes;
  const valid = name.trim() !== "" && allocBytes > 0 && !overPool && !belowUsage;

  async function save() {
    if (!valid || busy) return;
    setBusy(true);
    setError(null);
    try {
      const ws = isEdit
        ? await api.patch<Workspace>(`/api/workspaces/${editing!.id}`, {
            name: name.trim(),
            disk_allocation_bytes: allocBytes,
          })
        : await api.post<Workspace>("/api/workspaces", {
            name: name.trim(),
            disk_allocation_bytes: allocBytes,
          });
      onSaved(ws);
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't save — try again.");
    } finally {
      setBusy(false);
    }
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        className={cn(
          "w-full overflow-hidden rounded-xl border border-border bg-card shadow-xl",
          isEdit ? "max-w-2xl" : "max-w-sm",
        )}
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-2">
          <div className="flex items-center gap-2">
            <span className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10">
              <Layers className="h-4 w-4 text-primary" />
            </span>
            <h2 className="text-sm font-semibold text-foreground">
              {isEdit ? "Edit workspace" : "New workspace"}
            </h2>
          </div>
          <button
            type="button"
            onClick={() => !busy && onClose()}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-2.5 p-3">
          {/* Name + disk allocation on one compact row. */}
          <div className="flex items-start gap-2.5">
            <div className="flex-1 space-y-1">
              <Label htmlFor="ws-create-name" className="text-[11px] font-medium text-foreground">
                Workspace name
              </Label>
              <Input
                id="ws-create-name"
                autoFocus
                value={name}
                maxLength={120}
                onChange={(e) => setName(e.target.value)}
                placeholder="Marketing"
                className="h-7 border-foreground/25 bg-background text-sm"
              />
            </div>
            <div className="w-24 space-y-1">
              <Label htmlFor="ws-create-alloc" className="text-[11px] font-medium text-foreground">
                Disk size
              </Label>
              <div className="relative">
                <Input
                  id="ws-create-alloc"
                  type="number"
                  min={0}
                  step="0.5"
                  value={allocGB}
                  onChange={(e) => setAllocGB(e.target.value)}
                  className={cn(
                    "h-7 border-foreground/25 bg-background pr-7 text-sm",
                    (overPool || belowUsage) && "border-destructive",
                  )}
                />
                <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-[11px] text-muted-foreground">
                  GB
                </span>
              </div>
            </div>
          </div>
          <p
            className={cn(
              "flex items-center gap-1 text-[11px]",
              overPool || belowUsage ? "text-destructive" : "text-muted-foreground",
            )}
          >
            <HardDrive className="h-3 w-3 flex-shrink-0" />
            {overPool
              ? `Only ${fmtSpace(remaining)} available in this account.`
              : belowUsage
                ? `${fmtSpace(minBytes)} is already in use — can't allocate less.`
                : unlimited
                  ? "Ample storage available — allocate as needed."
                  : `${fmtSpace(remaining)} available to allocate.`}
          </p>

          {/* Inline access control — edit mode only. Dual pane: members on the
              left, this workspace's hierarchy on the right. */}
          {isEdit && editing && (
            <div className="space-y-1.5 border-t border-border pt-2.5">
              <Label className="flex items-center gap-1.5 text-[11px] font-medium text-foreground">
                <Users className="h-3.5 w-3.5" />
                Member access
              </Label>
              <p className="text-[11px] leading-relaxed text-muted-foreground">
                Pick a member, then grant the whole workspace or specific folders.
                Owners and admins always have full access.
              </p>
              {roster === null ? (
                <div className="flex items-center justify-center gap-2 rounded-md border border-input bg-background px-3 py-6 text-[11px] text-muted-foreground">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  Loading members…
                </div>
              ) : roster.length === 0 ? (
                <div className="rounded-md border border-input bg-background px-3 py-6 text-center text-[11px] text-muted-foreground">
                  No members or viewers to assign yet.
                </div>
              ) : (
                <div className="grid grid-cols-[minmax(0,12rem)_1fr] gap-2">
                  {/* Left: members — searchable + scrollable so it scales. */}
                  {(() => {
                    const q = memberQuery.trim().toLowerCase();
                    const filtered = q
                      ? roster.filter(
                          (m) =>
                            displayName(m).toLowerCase().includes(q) ||
                            m.email.toLowerCase().includes(q),
                        )
                      : roster;
                    return (
                      <div className="flex max-h-72 flex-col overflow-hidden rounded-md border border-input bg-background">
                        <div className="border-b border-border p-1.5">
                          <div className="relative">
                            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                            <input
                              value={memberQuery}
                              onChange={(e) => setMemberQuery(e.target.value)}
                              placeholder="Search members…"
                              className="h-7 w-full rounded-md border border-foreground/20 bg-background pl-7 pr-2 text-xs outline-none focus:border-primary/50"
                            />
                          </div>
                        </div>
                        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto p-1">
                          {filtered.length === 0 ? (
                            <p className="px-2 py-3 text-center text-[11px] text-muted-foreground">
                              No members match “{memberQuery.trim()}”.
                            </p>
                          ) : (
                            filtered.map((m) => {
                              const mode = accessMode(m);
                              const isSel = m.user_id === selectedUserId;
                              return (
                                <button
                                  key={m.user_id}
                                  type="button"
                                  onClick={() => setSelectedUserId(m.user_id)}
                                  className={cn(
                                    "flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors",
                                    isSel ? "bg-primary/10" : "hover:bg-muted",
                                  )}
                                >
                                  <span
                                    className={cn(
                                      "h-2 w-2 flex-shrink-0 rounded-full",
                                      mode === "full"
                                        ? "bg-primary"
                                        : mode === "partial"
                                          ? "bg-amber-500"
                                          : "bg-muted-foreground/30",
                                    )}
                                    title={
                                      mode === "full"
                                        ? "Full access"
                                        : mode === "partial"
                                          ? "Folder-scoped access"
                                          : "No access"
                                    }
                                  />
                                  <span className="min-w-0 flex-1">
                                    <span className="block truncate text-sm text-foreground">{displayName(m)}</span>
                                    <span className="block truncate text-[11px] text-muted-foreground">
                                      {m.role.charAt(0).toUpperCase() + m.role.slice(1)}
                                    </span>
                                  </span>
                                </button>
                              );
                            })
                          )}
                        </div>
                      </div>
                    );
                  })()}

                  {/* Right: this workspace's hierarchy for the selected member */}
                  <div className="min-w-0">
                    {selectedMember ? (
                      <WorkspaceFolderPicker
                        key={selectedMember.user_id}
                        workspaces={[editing]}
                        value={{
                          workspaceIds: selectedMember.whole ? [editing.id] : [],
                          folderIds: selectedMember.folder_ids,
                        }}
                        initialFolderWorkspaces={Object.fromEntries(
                          selectedMember.folder_ids.map((fid) => [fid, editing.id]),
                        )}
                        onChange={(v) =>
                          void applyAccess(
                            selectedMember.user_id,
                            v.workspaceIds.includes(editing.id),
                            v.folderIds,
                          )
                        }
                      />
                    ) : (
                      <div className="flex h-full items-center justify-center rounded-md border border-input bg-background px-3 py-6 text-center text-[11px] text-muted-foreground">
                        Select a member to edit their access.
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {error && (
            <p className="text-[11px] text-destructive" role="alert">
              {error}
            </p>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-2">
          <button
            type="button"
            onClick={() => !busy && onClose()}
            disabled={busy}
            className="inline-flex h-7 min-w-[5rem] items-center justify-center rounded-md border border-foreground/30 bg-background px-3 text-[11px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={save}
            disabled={!valid || busy}
            className="inline-flex h-7 items-center justify-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {busy ? (isEdit ? "Saving…" : "Creating…") : isEdit ? "Save changes" : "Create workspace"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
