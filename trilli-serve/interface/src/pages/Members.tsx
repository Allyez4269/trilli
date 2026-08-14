import { useEffect, useMemo, useState } from "react";
import {
  Ban,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Copy,
  Layers,
  Loader2,
  Mail,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  UserPlus,
  Users as UsersIcon,
  X,
} from "lucide-react";

import {
  api,
  ApiError,
  type InviteCreateResponse,
  type InvitePayload,
  type PendingInvitesResponse,
  type SeatSummary,
  type Workspace,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import SeatMeter from "@/components/SeatMeter";
import AddSeatsModal from "@/components/AddSeatsModal";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { WorkspaceFolderPicker, type WSAccessValue } from "@/components/WorkspaceFolderPicker";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/Checkbox";

// Owner is intentionally NOT in INVITE_ROLES — ownership of a tenant is
// transferred from the current Owner, not handed out via an invite.
//
// Three tiers users can be invited at:
//   Admin  — full access incl. workspace management (invites, billing read)
//   Member — full access except workspace management
//   Viewer — read only
const INVITE_ROLES = [
  { value: "admin", label: "Admin", description: "Full access + manage users" },
  { value: "member", label: "Member", description: "Full access" },
  { value: "viewer", label: "Viewer", description: "Read only" },
] as const;
type InviteRole = (typeof INVITE_ROLES)[number]["value"];

const PAGE_SIZE = 10;
const JUMP_THRESHOLD = 20;

// Native checkbox rendering (browser-styled) with a hard size lock.
// We tried appearance-none for pixel-identical box dimensions but it
// regressed the visible disabled Owner state — the native renderer
// is the simpler / better fit here.
// Section-count pill used on both the Users and Pending Invites
// headers. Darker text + font-semibold so the count reads clearly
// against the light secondary bg.
const sectionCountPillCls =
  "inline-flex items-center rounded-full bg-secondary px-2 py-0.5 text-[10.5px] font-semibold tabular-nums text-foreground/80";


// Static demo data so the layout has something to render. Wiring comes in
// the next phase — backend tables already exist (tenant_members).
type MemberRow = {
  id: number;
  name: string;
  status: "active" | "pending" | "suspended";
  // Free-form so it can hold both today's DB values ("Admin", "Member")
  // and future role tiers without a code churn.
  role: string;
  email: string;
  dateAdded: string; // pre-formatted MM/DD/YYYY for now
};

// API-side payload (matches auth.TenantMember on the server). Joined to
// users for the email; mapped into MemberRow shape for rendering.
interface APIMember {
  id: number;
  email: string;
  full_name?: string; // null/absent on the wire when the user hasn't set one
  role: string;       // "admin" | "member"
  status: string;     // "active" | "invited" | "suspended"
  created_at: string;
}

// titleizeEmail derives a display name from the local part of an email.
// "mike+e2e@trilli.test" -> "Mike E2e". When the user later adds names to
// the users table, swap this for the real field.
function titleizeEmail(email: string): string {
  const local = email.split("@")[0] ?? email;
  return local
    .split(/[._+\-]+/)
    .filter(Boolean)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join(" ");
}

// formatExpiresAt renders an ISO timestamp as "May 31, 2026" — the
// "Expires" column in Pending Invites.
function formatExpiresAt(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

// formatRelativeFromExpiry treats the invite's expires_at as TTL-end
// and reports how recently it was sent (server picks expires_at =
// created_at + 7d, so created = expires - 7d). The "Sent" column on
// Pending Invites uses this to give the Owner a sense of staleness.
function formatRelativeFromExpiry(iso: string): string {
  const expires = new Date(iso);
  if (Number.isNaN(expires.getTime())) return "—";
  const created = expires.getTime() - 7 * 24 * 60 * 60 * 1000;
  const diffMs = Date.now() - created;
  const mins = Math.floor(diffMs / 60_000);
  if (mins < 1) return "Just now";
  if (mins < 60) return `${mins} min${mins === 1 ? "" : "s"} ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs} hour${hrs === 1 ? "" : "s"} ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days} day${days === 1 ? "" : "s"} ago`;
  return new Date(created).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function mapAPIMember(m: APIMember): MemberRow {
  const apiStatus = m.status === "invited" ? "pending" : (m.status as MemberRow["status"]);
  const role = (m.role.charAt(0).toUpperCase() + m.role.slice(1)) as MemberRow["role"];
  const d = new Date(m.created_at);
  const dateAdded = `${String(d.getMonth() + 1).padStart(2, "0")}/${String(d.getDate()).padStart(2, "0")}/${d.getFullYear()}`;
  // Prefer the user's stored full_name; fall back to titleizing the
  // email's local part when no name has been set.
  const name =
    m.full_name && m.full_name.trim() !== "" ? m.full_name : titleizeEmail(m.email);
  return {
    id: m.id,
    name,
    status: apiStatus,
    role,
    email: m.email,
    dateAdded,
  };
}

export default function Members() {
  const [members, setMembers] = useState<MemberRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number[]>([]);
  const [searchInput, setSearchInput] = useState("");
  const [activeQuery, setActiveQuery] = useState("");
  const [offset, setOffset] = useState(0);
  // Invite modal state. Submitting POSTs to /api/invites and swaps to a
  // "copy link" view; no email is sent yet (SendGrid is Phase B).
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteEmails, setInviteEmails] = useState("");
  const [inviteRole, setInviteRole] = useState<InviteRole | "">("");
  const [inviteSubmitting, setInviteSubmitting] = useState(false);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [inviteResults, setInviteResults] = useState<InvitePayload[] | null>(null);
  const [copiedToken, setCopiedToken] = useState<string | null>(null);
  // Access for Member/Viewer invites — whole workspaces and/or folder-scoped
  // grants. Required (at least one) for those roles; ignored for Admin.
  const [inviteWsIds, setInviteWsIds] = useState<number[]>([]);
  const [inviteFolderIds, setInviteFolderIds] = useState<number[]>([]);
  const wsRequired = inviteRole === "member" || inviteRole === "viewer";
  const inviteValid =
    inviteEmails.trim() !== "" &&
    inviteRole !== "" &&
    (!wsRequired || inviteWsIds.length + inviteFolderIds.length > 0);

  // Pending invites — listed in a separate section below the Users
  // table. Hidden when empty so the panel doesn't add visual noise to
  // workspaces that aren't actively onboarding anyone.
  const [pendingInvites, setPendingInvites] = useState<InvitePayload[]>([]);
  // Per-row spinner state for resend (keyed on invite id). We don't
  // track "in flight" for revoke separately — the row just disappears
  // optimistically when revoke succeeds.
  const [resendingId, setResendingId] = useState<number | null>(null);
  const [pendingCopiedToken, setPendingCopiedToken] = useState<string | null>(null);
  // Multi-select for the Pending Invites table. Separate from the
  // Users-table `selected` state so the two tables can be operated on
  // independently. Bulk actions fan out N parallel requests and clear
  // the selection on completion.
  const [selectedInvites, setSelectedInvites] = useState<number[]>([]);
  const [bulkResending, setBulkResending] = useState(false);

  const loadPending = () => {
    return api
      .get<PendingInvitesResponse>("/api/invites")
      .then((res) => setPendingInvites(res.invites ?? []))
      .catch(() => {
        // Silent: this section is auxiliary; failing to load shouldn't
        // block the user-management view.
      });
  };

  // Seat usage powers the meter + add-seats upsell at the top of the page.
  const [seats, setSeats] = useState<SeatSummary | null>(null);
  const [addSeatsOpen, setAddSeatsOpen] = useState(false);
  const loadSeats = () => {
    return api
      .get<SeatSummary>("/api/billing/seats")
      .then((res) => setSeats(res))
      .catch(() => {
        /* Silent: the meter is auxiliary. */
      });
  };
  useEffect(() => {
    void loadSeats();
  }, []);

  const resendInvite = async (id: number) => {
    if (resendingId !== null) return;
    setResendingId(id);
    try {
      const updated = await api.post<InvitePayload>(`/api/invites/${id}/resend`);
      setPendingInvites((prev) =>
        prev.map((inv) => (inv.id === id ? updated : inv)),
      );
    } catch {
      // Keep silent UI for now; future iteration could surface a toast.
    } finally {
      setResendingId(null);
    }
  };

  const revokeInvite = async (id: number) => {
    // Optimistically drop the row; if the server rejects we'll refetch.
    const prev = pendingInvites;
    setPendingInvites((cur) => cur.filter((inv) => inv.id !== id));
    setSelectedInvites((cur) => cur.filter((x) => x !== id));
    try {
      await api.delete<void>(`/api/invites/${id}`);
      void loadSeats(); // a freed invite returns a seat
    } catch {
      setPendingInvites(prev);
    }
  };

  const allInvitesSelected =
    pendingInvites.length > 0 &&
    pendingInvites.every((inv) => selectedInvites.includes(inv.id));
  const someInvitesSelected =
    selectedInvites.length > 0 && !allInvitesSelected;

  const toggleAllInvites = () => {
    if (allInvitesSelected) {
      setSelectedInvites([]);
    } else {
      setSelectedInvites(pendingInvites.map((inv) => inv.id));
    }
  };
  const toggleOneInvite = (id: number) => {
    setSelectedInvites((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );
  };

  const bulkResendInvites = async () => {
    if (bulkResending || selectedInvites.length === 0) return;
    setBulkResending(true);
    try {
      const updates = await Promise.allSettled(
        selectedInvites.map((id) =>
          api.post<InvitePayload>(`/api/invites/${id}/resend`),
        ),
      );
      // Patch the rows we successfully resent; leave the rest as-is.
      const updatedById = new Map<number, InvitePayload>();
      updates.forEach((res, i) => {
        if (res.status === "fulfilled") updatedById.set(selectedInvites[i], res.value);
      });
      setPendingInvites((prev) =>
        prev.map((inv) => updatedById.get(inv.id) ?? inv),
      );
      setSelectedInvites([]);
    } finally {
      setBulkResending(false);
    }
  };

  const bulkRevokeInvites = async () => {
    if (selectedInvites.length === 0) return;
    const toRevoke = [...selectedInvites];
    // Optimistic: drop them all immediately; restore on failure.
    const prev = pendingInvites;
    setPendingInvites((cur) => cur.filter((inv) => !toRevoke.includes(inv.id)));
    setSelectedInvites([]);
    const results = await Promise.allSettled(
      toRevoke.map((id) => api.delete<void>(`/api/invites/${id}`)),
    );
    const anyFailed = results.some((r) => r.status === "rejected");
    if (anyFailed) {
      // Server rejected at least one — refetch to get the authoritative
      // state rather than guessing which IDs are still pending.
      setPendingInvites(prev);
      void loadPending();
    }
  };

  const copyPendingLink = async (token: string, link: string) => {
    try {
      await navigator.clipboard.writeText(link);
      setPendingCopiedToken(token);
      setTimeout(() => {
        setPendingCopiedToken((cur) => (cur === token ? null : cur));
      }, 1500);
    } catch {
      window.prompt("Copy invite link:", link);
    }
  };

  // Bulk-status spinner (Active / Disabled toolbar action). One flag is
  // enough since both options use the same button area.
  const [bulkStatusBusy, setBulkStatusBusy] = useState(false);

  // Bulk Workspace Access modal — assigns the same set of workspaces to every
  // selected member at once. A member gets whole-workspace access: every folder
  // and file inside an assigned workspace. Owner rows are filtered out at submit
  // time (they already reach every workspace by role).
  const [accountWorkspaces, setAccountWorkspaces] = useState<Workspace[]>([]);
  const [wsAccessOpen, setWsAccessOpen] = useState(false);
  const [wsAccessVal, setWsAccessVal] = useState<WSAccessValue>({ workspaceIds: [], folderIds: [] });
  const [wsAccessFolderWs, setWsAccessFolderWs] = useState<Record<number, number>>({});
  const [wsAccessBusy, setWsAccessBusy] = useState(false);
  const [wsAccessLoading, setWsAccessLoading] = useState(false);
  const [wsAccessError, setWsAccessError] = useState<string | null>(null);

  // Load the account's workspaces once (for the access checklist).
  useEffect(() => {
    let cancelled = false;
    api
      .get<{ workspaces: Workspace[] }>("/api/workspaces")
      .then((r) => {
        if (!cancelled) setAccountWorkspaces(r.workspaces ?? []);
      })
      .catch(() => {
        if (!cancelled) setAccountWorkspaces([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const openWorkspaceAccess = async () => {
    setWsAccessError(null);
    setWsAccessVal({ workspaceIds: [], folderIds: [] });
    setWsAccessFolderWs({});
    setWsAccessOpen(true);

    // Pre-fill with what the selected members already have so their current
    // access shows up checked. Owners are skipped (full access by role). For a
    // multi-select we seed the union of everyone's grants — the admin edits
    // from there, and Apply replaces each member's set.
    const targets = members
      .filter((m) => selected.includes(m.id) && m.role.toLowerCase() !== "owner")
      .map((m) => m.id);
    if (targets.length === 0) return;

    setWsAccessLoading(true);
    try {
      const [wsLists, folderLists] = await Promise.all([
        Promise.all(
          targets.map((id) =>
            api
              .get<{ workspace_ids: number[] }>(`/api/members/${id}/workspaces`)
              .then((r) => r.workspace_ids ?? [])
              .catch(() => [] as number[]),
          ),
        ),
        Promise.all(
          targets.map((id) =>
            api
              .get<{ folder_ids: number[]; folders?: { folder_id: number; workspace_id: number }[] }>(
                `/api/members/${id}/folders`,
              )
              .then((r) => r.folders ?? (r.folder_ids ?? []).map((fid) => ({ folder_id: fid, workspace_id: 0 })))
              .catch(() => [] as { folder_id: number; workspace_id: number }[]),
          ),
        ),
      ]);
      const grants = folderLists.flat();
      const folderWs: Record<number, number> = {};
      grants.forEach((g) => {
        if (g.workspace_id) folderWs[g.folder_id] = g.workspace_id;
      });
      setWsAccessFolderWs(folderWs);
      setWsAccessVal({
        workspaceIds: Array.from(new Set(wsLists.flat())),
        folderIds: Array.from(new Set(grants.map((g) => g.folder_id))),
      });
    } finally {
      setWsAccessLoading(false);
    }
  };
  const closeWorkspaceAccess = () => {
    if (wsAccessBusy) return;
    setWsAccessOpen(false);
  };
  // How many of the selected rows will actually be updated (owners skipped).
  const selectedNonOwnerCount = members.filter(
    (m) => selected.includes(m.id) && m.role.toLowerCase() !== "owner",
  ).length;
  const submitWorkspaceAccess = async () => {
    if (wsAccessBusy || selected.length === 0) return;
    setWsAccessBusy(true);
    setWsAccessError(null);
    try {
      const targets = members
        .filter((m) => selected.includes(m.id) && m.role.toLowerCase() !== "owner")
        .map((m) => m.id);
      // Each member's whole-workspace set AND folder-grant set are replaced.
      const results = await Promise.allSettled(
        targets.flatMap((id) => [
          api.patch<void>(`/api/members/${id}/workspaces`, { workspace_ids: wsAccessVal.workspaceIds }),
          api.patch<void>(`/api/members/${id}/folders`, { folder_ids: wsAccessVal.folderIds }),
        ]),
      );
      const failed = results.find((r) => r.status === "rejected");
      if (failed) {
        const reason = (failed as PromiseRejectedResult).reason;
        const msg =
          reason instanceof ApiError ? reason.message : "Some updates failed";
        setWsAccessError(msg);
        return;
      }
      setSelected([]);
      setWsAccessOpen(false);
    } finally {
      setWsAccessBusy(false);
    }
  };

  const bulkSetStatus = async (newStatus: "active" | "suspended") => {
    if (bulkStatusBusy || selected.length === 0) return;
    setBulkStatusBusy(true);
    try {
      const targetIds = selected.slice();
      await Promise.allSettled(
        targetIds.map((id) =>
          api.patch<void>(`/api/members/${id}/status`, { status: newStatus }),
        ),
      );
      // Refetch the canonical list so status pills + Owner-row exclusion
      // reflect the server state, rather than guessing per-row.
      try {
        const res = await api.get<{ members: APIMember[] }>("/api/members");
        setMembers((res.members ?? []).map(mapAPIMember));
      } catch {
        // ignore — next render will catch up
      }
      void loadSeats(); // suspend/activate changes the active-seat count
      setSelected([]);
    } finally {
      setBulkStatusBusy(false);
    }
  };

  // Delete-member modal. Holds the row being deleted (null = closed) so
  // the modal can render the user's name in the confirmation prompt.
  const [deleteTarget, setDeleteTarget] = useState<MemberRow | null>(null);
  const [deleteSubmitting, setDeleteSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const openDelete = (m: MemberRow) => {
    setDeleteTarget(m);
    setDeleteError(null);
  };
  const closeDelete = () => {
    if (deleteSubmitting) return;
    setDeleteTarget(null);
    setDeleteError(null);
  };
  const confirmDelete = async () => {
    if (!deleteTarget || deleteSubmitting) return;
    setDeleteSubmitting(true);
    setDeleteError(null);
    try {
      await api.delete<void>(`/api/members/${deleteTarget.id}`);
      setMembers((prev) => prev.filter((m) => m.id !== deleteTarget.id));
      setSelected((prev) => prev.filter((id) => id !== deleteTarget.id));
      setDeleteTarget(null);
      void loadSeats(); // a removed member frees a seat — refresh the meter
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : "Failed to delete user");
    } finally {
      setDeleteSubmitting(false);
    }
  };

  const openInvite = () => {
    setInviteEmails("");
    setInviteRole("");
    setInviteWsIds([]);
    setInviteFolderIds([]);
    setInviteError(null);
    setInviteResults(null);
    setCopiedToken(null);
    setInviteOpen(true);
  };
  const closeInvite = () => {
    // If invites were issued, refresh both lists on close. Pending
    // invites refresh is the important one — the new invite shows up
    // immediately in the Pending Invites panel.
    if (inviteResults && inviteResults.length > 0) {
      void api
        .get<{ members: APIMember[] }>("/api/members")
        .then((res) => setMembers((res.members ?? []).map(mapAPIMember)))
        .catch(() => {});
      void loadPending();
    }
    setInviteOpen(false);
  };

  const submitInvite = async () => {
    if (inviteSubmitting || !inviteValid) return;
    setInviteSubmitting(true);
    setInviteError(null);
    try {
      const emails = inviteEmails
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const res = await api.post<InviteCreateResponse>("/api/invites", {
        emails,
        role: inviteRole,
        // Admin invites bypass access selection; member/viewer require at least
        // one whole workspace and/or folder-scoped grant.
        workspace_ids: wsRequired ? inviteWsIds : [],
        folder_ids: wsRequired ? inviteFolderIds : [],
      });
      setInviteResults(res.invites ?? []);
      void loadSeats(); // pending invites consume seats — keep the meter fresh
    } catch (err) {
      setInviteError(err instanceof ApiError ? err.message : "Failed to create invite");
    } finally {
      setInviteSubmitting(false);
    }
  };

  const copyLink = async (token: string, link: string) => {
    try {
      await navigator.clipboard.writeText(link);
      setCopiedToken(token);
      setTimeout(() => {
        setCopiedToken((cur) => (cur === token ? null : cur));
      }, 1500);
    } catch {
      // Clipboard API failed (e.g. insecure context); fall back to a
      // synthetic selection so the user can Ctrl-C.
      window.prompt("Copy invite link:", link);
    }
  };

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .get<{ members: APIMember[] }>("/api/members")
      .then((res) => {
        if (!cancelled) {
          setMembers((res.members ?? []).map(mapAPIMember));
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof ApiError ? err.message : "Failed to load users");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    void loadPending();
    return () => {
      cancelled = true;
    };
  }, []);

  // Filter against name + email, case-insensitive. Empty query keeps all
  // rows.
  const filteredMembers = useMemo(() => {
    const q = activeQuery.trim().toLowerCase();
    if (q === "") return members;
    return members.filter(
      (m) => m.name.toLowerCase().includes(q) || m.email.toLowerCase().includes(q),
    );
  }, [activeQuery, members]);

  const totalFiltered = filteredMembers.length;
  const totalPages = Math.max(1, Math.ceil(totalFiltered / PAGE_SIZE));
  const currentPage = Math.min(totalPages, Math.floor(offset / PAGE_SIZE) + 1);
  const visibleRows = filteredMembers.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  // Owner rows are immutable and unselectable, so they're excluded from
  // every selection calculation — select-all skips them, and the
  // "indeterminate" / "all checked" state reflects only the rows the
  // user CAN bulk-act on.
  const selectableRows = visibleRows.filter((m) => m.role.toLowerCase() !== "owner");
  const allSelected =
    selectableRows.length > 0 &&
    selectableRows.every((m) => selected.includes(m.id));
  const someSelected =
    selected.length > 0 && !allSelected && selectableRows.some((m) => selected.includes(m.id));

  const toggleAll = () => {
    if (allSelected) {
      setSelected((prev) => prev.filter((id) => !selectableRows.some((m) => m.id === id)));
    } else {
      const ids = new Set([...selected, ...selectableRows.map((m) => m.id)]);
      setSelected([...ids]);
    }
  };
  const toggleOne = (id: number) => {
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  };

  const onSearchSubmit = (q: string) => {
    setActiveQuery(q.trim());
    setOffset(0);
  };
  const clearSearch = () => {
    setSearchInput("");
    setActiveQuery("");
    setOffset(0);
  };
  const jumpToPage = (p: number) => setOffset((p - 1) * PAGE_SIZE);
  const inSearch = activeQuery.trim() !== "";

  // Initial-load skeleton for the Users + Pending Invites layout.
  if (loading && members.length === 0) {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
          <Skeleton className="h-8 w-32" />
          <Skeleton className="h-10 w-full" />
          <div className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl space-y-6 p-6 lg:p-8">
        {/* Header + primary action on one strip. The page is called "Users"
            everywhere now (sidebar, title) — it used to masquerade as a
            "Settings" page with a lone "User Management" tab. */}
        <div className="flex items-end justify-between border-b border-border pb-3">
          <header>
            <h1 className="flex items-center gap-2 text-2xl font-bold tracking-tight text-foreground">
              <UsersIcon className="h-6 w-6 text-primary" />
              Users
            </h1>
            <p className="mt-0.5 text-sm text-muted-foreground">
              Manage the people in this account — roles, access, seats, and invites.
            </p>
          </header>
          <Button
            onClick={openInvite}
            className="mb-1 h-7 gap-1.5 rounded px-3 text-xs"
          >
            <Plus className="h-3.5 w-3.5" />
            <span>Invite Users</span>
          </Button>
        </div>

        {seats && (
          <SeatMeter seats={seats} canManage onAddSeats={() => setAddSeatsOpen(true)} />
        )}

        <section className="space-y-3">
          {/* Toolbar swaps based on selection. With nothing selected it's
              the usual "<n> users · Export" strip. As soon as one row is
              checked it flips to a contextual bar with Delete / Status /
              Role bulk-action menus on the left and a Clear Selection
              text button on the right — matching the reference layout. */}
          {selected.length === 0 ? (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <h2 className="text-sm font-medium text-foreground">Users</h2>
                <span className={sectionCountPillCls}>{totalFiltered}</span>
              </div>
              <div className="flex items-center gap-2">
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    onSearchSubmit(searchInput);
                  }}
                  className="w-full sm:w-64"
                >
                  <div className="relative">
                    <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                    <Input
                      placeholder="Search users…"
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
            </div>
          ) : (
            <div className="flex items-center justify-between rounded-lg border border-primary/20 bg-primary/[0.06] px-3 py-2 shadow-sm animate-in fade-in slide-in-from-bottom-2 duration-200 ease-out">
              <div className="flex items-center gap-3">
                <p className="text-sm text-foreground">
                  <span className="font-medium tabular-nums">{selected.length}</span> user
                  {selected.length === 1 ? "" : "s"} selected out of{" "}
                  <span className="font-medium tabular-nums">{totalFiltered}</span>
                </p>
                <div className="h-5 w-px bg-border" aria-hidden="true" />
                <button
                  type="button"
                  onClick={() => void openWorkspaceAccess()}
                  className="inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
                >
                  <Layers className="h-4 w-4" />
                  Workspace Access
                </button>
                <button
                  type="button"
                  className="inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
                >
                  <Trash2 className="h-4 w-4" />
                  Delete
                </button>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      disabled={bulkStatusBusy}
                      className="inline-flex items-center gap-1.5 rounded border border-border bg-background px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-60"
                    >
                      {bulkStatusBusy && (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      )}
                      Status
                      <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="z-[70] w-32">
                    <DropdownMenuItem onSelect={() => void bulkSetStatus("active")}>
                      Active
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => void bulkSetStatus("suspended")}>
                      Disabled
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      type="button"
                      className="inline-flex items-center gap-1.5 rounded border border-border bg-background px-2.5 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted"
                    >
                      Role
                      <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="w-32">
                    <DropdownMenuItem>Admin</DropdownMenuItem>
                    <DropdownMenuItem>Member</DropdownMenuItem>
                    <DropdownMenuItem>Viewer</DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
              <button
                type="button"
                onClick={() => setSelected([])}
                className="text-xs font-medium text-primary transition-colors hover:underline"
              >
                Clear Selection
              </button>
            </div>
          )}

          {/* User table — same chrome as Files/Recent: rounded-lg border,
              sticky thead, py-1.5 rows, top/bottom spacer rows. */}
          <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
            <table className="w-full table-fixed text-sm">
              {/* Shared column rail with the Pending Invites table —
                  keep these widths in sync between the two tables so
                  the visual grid stays aligned (checkbox/identity/short
                  field/short field/long field/last column). Middle
                  columns are spread a bit more evenly so the row reads
                  with consistent gutter spacing instead of one wide
                  primary column flanked by narrow status/role pairs. */}
              <colgroup>
                <col style={{ width: "3rem" }} />
                <col style={{ width: "22%" }} />
                <col style={{ width: "14%" }} />
                <col style={{ width: "14%" }} />
                <col style={{ width: "22%" }} />
                <col />
              </colgroup>
              <thead className="sticky top-0 z-10 bg-card">
                <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                  <th className="px-4 py-2.5 text-left">
                    <Checkbox
                      checked={allSelected}
                      ref={(el) => {
                        if (el) el.indeterminate = someSelected;
                      }}
                      onChange={toggleAll}
                      disabled={selectableRows.length === 0}
                      aria-label="Select all members"
                    />
                  </th>
                  <th className="px-4 py-2.5 text-left">Name</th>
                  <th className="px-4 py-2.5 text-left">Status</th>
                  <th className="px-4 py-2.5 text-left">Role</th>
                  <th className="hidden px-4 py-2.5 text-left sm:table-cell">Email</th>
                  <th className="hidden py-2.5 pl-4 pr-[10%] text-right md:table-cell">Date Added</th>
                </tr>
              </thead>
              <tbody>
                <tr aria-hidden="true">
                  <td colSpan={6} className="h-3 p-0" />
                </tr>
                {visibleRows.map((m) => (
                  <tr
                    key={m.id}
                    className={cn(
                      "group transition-colors",
                      selected.includes(m.id) ? "bg-primary/5" : "hover:bg-muted/40",
                    )}
                  >
                    <td className="px-4 py-1.5">
                      {/* Owner rows are immutable AND unselectable. Bulk
                          actions don't apply to Owners (they can't be
                          deleted / role-changed / suspended), so we
                          dim the checkbox so it reads as inert. */}
                      <Checkbox
                        checked={selected.includes(m.id)}
                        onChange={() => toggleOne(m.id)}
                        disabled={m.role.toLowerCase() === "owner"}
                        aria-label={`Select ${m.name}`}
                      />
                    </td>
                    <td className="px-4 py-1.5">
                      <span
                        className="block truncate text-xs font-medium text-foreground"
                        title={m.name}
                      >
                        {m.name}
                      </span>
                    </td>
                    <td className="px-4 py-1.5">
                      <div className="relative inline-flex items-center">
                        {/* Trash button is absolute-positioned so its
                            hidden state doesn't push the StatusPill
                            right; the pill stays aligned with the
                            "Status" header. Owners are immutable — the
                            delete affordance never appears on the Owner
                            row. Admins and below CAN be removed. */}
                        {m.role.toLowerCase() !== "owner" && (
                          <button
                            type="button"
                            onClick={() => openDelete(m)}
                            className="absolute right-full mr-1 inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground opacity-0 transition-opacity hover:bg-muted hover:text-destructive focus:opacity-100 group-hover:opacity-100"
                            title={`Delete ${m.name}`}
                            aria-label={`Delete ${m.name}`}
                          >
                            <Trash2 className="h-[17px] w-[17px]" />
                          </button>
                        )}
                        <StatusPill status={m.status} />
                      </div>
                    </td>
                    <td className="px-4 py-1.5 text-xs text-foreground/80">{m.role}</td>
                    <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                      <span className="block truncate" title={m.email}>
                        {m.email}
                      </span>
                    </td>
                    <td className="hidden py-1.5 pl-4 pr-[10%] text-right text-[12.5px] text-muted-foreground md:table-cell">
                      {m.dateAdded}
                    </td>
                  </tr>
                ))}
                {visibleRows.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      className="px-4 py-10 text-center text-xs text-muted-foreground"
                    >
                      {loading
                        ? "Loading users…"
                        : error
                          ? error
                          : inSearch
                            ? `No users match "${activeQuery}".`
                            : "No users yet."}
                    </td>
                  </tr>
                )}
                <tr aria-hidden="true">
                  <td colSpan={6} className="h-3 p-0" />
                </tr>
              </tbody>
            </table>
          </div>

          {totalFiltered > PAGE_SIZE && (
            <Paginator
              currentPage={currentPage}
              totalPages={totalPages}
              onJump={jumpToPage}
            />
          )}
        </section>

        {/* Pending Invites — auto-hides when there are no pending rows.
            Mirrors the Users table chrome (rounded-lg border, sticky
            thead, py-1.5 rows) so the two read as a single design
            language. Actions per row: Copy link / Resend / Revoke. */}
        {pendingInvites.length > 0 && (
          <section className="space-y-3">
            {/* Header swaps to a contextual toolbar when 1+ invites are
                checked — same animation + chrome as the Users table's
                bulk-action bar so the two read as a single design
                language. */}
            {selectedInvites.length === 0 ? (
              <div className="flex items-center gap-2">
                <h2 className="text-sm font-medium text-foreground">
                  Pending Invites
                </h2>
                <span className={sectionCountPillCls}>
                  {pendingInvites.length}
                </span>
              </div>
            ) : (
              <div className="flex items-center justify-between rounded-lg border border-primary/20 bg-primary/[0.06] px-3 py-2 shadow-sm animate-in fade-in slide-in-from-bottom-2 duration-200 ease-out">
                <div className="flex items-center gap-3">
                  <p className="text-sm text-foreground">
                    <span className="font-medium tabular-nums">
                      {selectedInvites.length}
                    </span>{" "}
                    invite{selectedInvites.length === 1 ? "" : "s"} selected
                    out of{" "}
                    <span className="font-medium tabular-nums">
                      {pendingInvites.length}
                    </span>
                  </p>
                  <div className="h-5 w-px bg-border" aria-hidden="true" />
                  <button
                    type="button"
                    onClick={() => void bulkResendInvites()}
                    disabled={bulkResending}
                    className="inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-50"
                  >
                    {bulkResending ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <RefreshCw className="h-4 w-4" />
                    )}
                    Resend
                  </button>
                  <button
                    type="button"
                    onClick={() => void bulkRevokeInvites()}
                    className="inline-flex items-center gap-1.5 rounded px-2 py-1 text-xs font-medium text-foreground transition-colors hover:bg-muted hover:text-destructive"
                  >
                    <Ban className="h-4 w-4" />
                    Revoke
                  </button>
                </div>
                <button
                  type="button"
                  onClick={() => setSelectedInvites([])}
                  className="text-xs font-medium text-primary transition-colors hover:underline"
                >
                  Clear Selection
                </button>
              </div>
            )}

            <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
              <table className="w-full table-fixed text-sm">
                {/* Column rail matches the Users table above so the
                    visual grid lines up: checkbox / identity / short /
                    short / long / last. Keep these widths in sync. */}
                <colgroup>
                  <col style={{ width: "3rem" }} />
                  <col style={{ width: "22%" }} />
                  <col style={{ width: "14%" }} />
                  <col style={{ width: "14%" }} />
                  <col style={{ width: "22%" }} />
                  <col />
                </colgroup>
                <thead className="sticky top-0 z-10 bg-card">
                  <tr className="border-b border-border text-[10.2px] uppercase tracking-wide text-muted-foreground">
                    <th className="px-4 py-2.5 text-left">
                      <Checkbox
                        checked={allInvitesSelected}
                        ref={(el) => {
                          if (el) el.indeterminate = someInvitesSelected;
                        }}
                        onChange={toggleAllInvites}
                        aria-label="Select all pending invites"
                      />
                    </th>
                    <th className="px-4 py-2.5 text-left">Email</th>
                    <th className="px-4 py-2.5 text-left">Role</th>
                    <th className="hidden px-4 py-2.5 text-left sm:table-cell">Sent</th>
                    <th className="hidden px-4 py-2.5 text-left sm:table-cell">Expires</th>
                    <th className="py-2.5 pl-4 pr-[9%] text-right">
                      {/* Width matches the icon row below (3× 28px buttons
                          with 4px gaps = 92px) so "Actions" reads as
                          centered directly above the three icons. */}
                      <span className="inline-block w-[92px] text-center">
                        Actions
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr aria-hidden="true">
                    <td colSpan={6} className="h-3 p-0" />
                  </tr>
                  {pendingInvites.map((inv) => (
                    <tr
                      key={inv.id}
                      className={cn(
                        "group transition-colors",
                        selectedInvites.includes(inv.id)
                          ? "bg-primary/5"
                          : "hover:bg-muted/40",
                      )}
                    >
                      <td className="px-4 py-1.5">
                        <Checkbox
                          checked={selectedInvites.includes(inv.id)}
                          onChange={() => toggleOneInvite(inv.id)}
                          aria-label={`Select invite for ${inv.email}`}
                        />
                      </td>
                      <td className="px-4 py-1.5">
                        <span
                          className="block truncate text-xs font-medium text-foreground"
                          title={inv.email}
                        >
                          {inv.email}
                        </span>
                      </td>
                      <td className="px-4 py-1.5 text-xs text-foreground/80">
                        {capitalize(inv.role)}
                      </td>
                      <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                        {formatRelativeFromExpiry(inv.expires_at)}
                      </td>
                      <td className="hidden px-4 py-1.5 text-[12.5px] text-muted-foreground sm:table-cell">
                        {formatExpiresAt(inv.expires_at)}
                      </td>
                      <td className="py-1.5 pl-4 pr-[9%]">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            type="button"
                            onClick={() => copyPendingLink(inv.token, inv.link)}
                            className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                            title="Copy invite link"
                            aria-label="Copy invite link"
                          >
                            {pendingCopiedToken === inv.token ? (
                              <Check className="h-4 w-4 text-emerald-500" />
                            ) : (
                              <Copy className="h-4 w-4" />
                            )}
                          </button>
                          <button
                            type="button"
                            onClick={() => void resendInvite(inv.id)}
                            disabled={resendingId === inv.id}
                            className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                            title="Resend invite email"
                            aria-label="Resend invite email"
                          >
                            {resendingId === inv.id ? (
                              <Loader2 className="h-4 w-4 animate-spin" />
                            ) : (
                              <RefreshCw className="h-4 w-4" />
                            )}
                          </button>
                          <button
                            type="button"
                            onClick={() => void revokeInvite(inv.id)}
                            className="inline-flex h-7 w-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-destructive"
                            title="Revoke invite"
                            aria-label="Revoke invite"
                          >
                            <Ban className="h-4 w-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                  <tr aria-hidden="true">
                    <td colSpan={6} className="h-3 p-0" />
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        )}
      </div>

      {/* Invite Users modal. Two states:
          1) Form: collect email(s) + role, POST /api/invites.
          2) Result: list of generated /invite/<token> links, each with a
             Copy button. SendGrid layer comes later — for now the Owner
             copies the link and shares it manually.
          Animation + chrome match the upload / move modals. */}
      {inviteOpen && (
        <div
          className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150"
          role="dialog"
          aria-modal="true"
          aria-labelledby="invite-modal-title"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeInvite();
          }}
        >
          <div className="w-[480px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in slide-in-from-bottom-2 duration-200 ease-out">
            <div className="mb-4 flex items-start gap-3">
              <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10">
                <UserPlus className="h-5 w-5 text-primary" />
              </div>
              <div className="min-w-0 flex-1">
                <h3
                  id="invite-modal-title"
                  className="text-base font-semibold text-foreground"
                >
                  Invite Users
                </h3>
                <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                  You can add multiple users to your account by separating them
                  with commas.
                </p>
              </div>
              <button
                type="button"
                onClick={closeInvite}
                className="ml-2 inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {inviteResults ? (
              <div className="space-y-3">
                <p className="text-xs text-muted-foreground">
                  {inviteResults.length === 1
                    ? "Invite link generated. Copy it and share with the invitee — they'll join your workspace after registering."
                    : `${inviteResults.length} invite links generated. Copy and share each one.`}
                </p>
                <ul className="space-y-2">
                  {inviteResults.map((inv) => (
                    <li
                      key={inv.token}
                      className="rounded-md border border-border bg-muted/40 p-3"
                    >
                      <div className="mb-1 flex items-center justify-between gap-2">
                        <span className="truncate text-xs font-medium text-foreground" title={inv.email}>
                          {inv.email}
                        </span>
                        <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">
                          {inv.role}
                        </span>
                      </div>
                      <div className="flex items-center gap-2">
                        <input
                          readOnly
                          value={inv.link}
                          className="flex-1 truncate rounded border border-input bg-background px-2 py-1 text-[11px] text-muted-foreground focus:outline-none"
                          onFocus={(e) => e.currentTarget.select()}
                        />
                        <button
                          type="button"
                          onClick={() => copyLink(inv.token, inv.link)}
                          className="inline-flex h-7 items-center gap-1.5 rounded border border-input bg-background px-2 text-[11px] font-medium text-foreground hover:bg-muted"
                          aria-label="Copy invite link"
                        >
                          {copiedToken === inv.token ? (
                            <>
                              <Check className="h-3.5 w-3.5 text-emerald-500" />
                              Copied
                            </>
                          ) : (
                            <>
                              <Copy className="h-3.5 w-3.5" />
                              Copy
                            </>
                          )}
                        </button>
                      </div>
                    </li>
                  ))}
                </ul>
                <div className="flex items-center justify-end pt-1">
                  <Button type="button" onClick={closeInvite} className="h-7">
                    Done
                  </Button>
                </div>
              </div>
            ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void submitInvite();
              }}
              className="space-y-4"
            >
              <div className="space-y-1.5">
                <label
                  htmlFor="invite-emails"
                  className="text-xs font-medium text-foreground"
                >
                  Email address
                </label>
                <div className="relative">
                  <Mail className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    id="invite-emails"
                    type="text"
                    placeholder="email@address.com"
                    value={inviteEmails}
                    onChange={(e) => setInviteEmails(e.target.value)}
                    autoFocus
                    className="h-7 rounded-[11px] border-input bg-background pl-10 pr-9 text-xs focus-visible:ring-1 focus-visible:ring-primary/40"
                  />
                  {inviteEmails && (
                    <button
                      type="button"
                      onClick={() => setInviteEmails("")}
                      className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                      aria-label="Clear"
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  )}
                </div>
              </div>

              <div className="space-y-1.5">
                <label
                  htmlFor="invite-role"
                  className="text-xs font-medium text-foreground"
                >
                  Role
                </label>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      id="invite-role"
                      type="button"
                      className={cn(
                        "flex h-7 w-full items-center justify-between rounded-[11px] border border-input bg-background px-3 text-xs transition-colors hover:border-primary/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary/40",
                        inviteRole ? "text-foreground" : "text-muted-foreground",
                      )}
                    >
                      <span>
                        {inviteRole
                          ? INVITE_ROLES.find((r) => r.value === inviteRole)?.label
                          : "Role"}
                      </span>
                      <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent
                    align="start"
                    // Modal backdrop is z-[60]; default popover z-50 was
                    // rendering BEHIND the modal so the menu was invisible
                    // even though it was opening. Bump above the backdrop.
                    className="z-[70] w-[var(--radix-dropdown-menu-trigger-width)] p-1"
                  >
                    {INVITE_ROLES.map((r) => (
                      <DropdownMenuItem
                        key={r.value}
                        onSelect={() => {
                          setInviteRole(r.value);
                          // Switching to admin clears any access picks —
                          // admins always reach everything.
                          if (r.value === "admin") {
                            setInviteWsIds([]);
                            setInviteFolderIds([]);
                          }
                        }}
                        className="flex-col items-start gap-0.5 py-2"
                      >
                        <span className="text-xs font-medium text-foreground">{r.label}</span>
                        <span className="text-[11px] text-muted-foreground">
                          {r.description}
                        </span>
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>

              {/* Workspace access — only relevant for Member/Viewer roles.
                  Admin invites skip this (they reach everything). */}
              {wsRequired && (
                <div className="space-y-1.5">
                  <label className="text-xs font-medium text-foreground">
                    Workspace access
                  </label>
                  <p className="text-[11px] leading-relaxed text-muted-foreground">
                    Tick a workspace for full access, or expand it and tick
                    specific folders to scope access to a folder depth. The
                    invitee can {inviteRole === "viewer" ? "view" : "view + edit"}{" "}
                    whatever you grant.
                  </p>
                  <WorkspaceFolderPicker
                    workspaces={accountWorkspaces}
                    value={{ workspaceIds: inviteWsIds, folderIds: inviteFolderIds }}
                    onChange={(v) => {
                      setInviteWsIds(v.workspaceIds);
                      setInviteFolderIds(v.folderIds);
                    }}
                  />
                </div>
              )}

              {inviteError && (
                <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  {inviteError}
                </div>
              )}

              <div className="flex items-center justify-end gap-2 pt-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={closeInvite}
                  className="h-7"
                  disabled={inviteSubmitting}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={!inviteValid || inviteSubmitting}
                  className="h-7"
                >
                  {inviteSubmitting && (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  )}
                  Invite
                </Button>
              </div>
            </form>
            )}
          </div>
        </div>
      )}

      {/* Delete confirmation modal. Mirrors the invite modal chrome
          (z-[60] backdrop, rounded-lg card, animated entrance) so the
          two read as a single design language. Destructive intent is
          signalled by a red Trash2 icon in the header and a destructive
          variant on the primary action. */}
      {/* Bulk Workspace Access modal */}
      {wsAccessOpen && (
        <div
          className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150"
          role="dialog"
          aria-modal="true"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeWorkspaceAccess();
          }}
        >
          <div className="w-[520px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in slide-in-from-bottom-2 duration-200 ease-out">
            <div className="mb-4 flex items-start gap-3">
              <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-primary/10">
                <Layers className="h-5 w-5 text-primary" />
              </div>
              <div className="min-w-0 flex-1">
                <h3 className="text-base font-semibold text-foreground">
                  Workspace access
                </h3>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  Pick the workspaces the selected{" "}
                  {selected.length === 1 ? "user" : "users"} can access. Each
                  member sees every folder and file inside an assigned workspace.
                  Owners in the selection are skipped — they always have full
                  access.
                </p>
              </div>
              <button
                type="button"
                onClick={closeWorkspaceAccess}
                disabled={wsAccessBusy}
                className="ml-2 inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {wsAccessLoading ? (
              <div className="flex items-center justify-center gap-2 rounded-md border border-input bg-background px-3 py-6 text-xs text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                Loading current access…
              </div>
            ) : (
              <WorkspaceFolderPicker
                workspaces={accountWorkspaces}
                value={wsAccessVal}
                onChange={setWsAccessVal}
                initialFolderWorkspaces={wsAccessFolderWs}
              />
            )}

            {!wsAccessLoading && selectedNonOwnerCount > 1 && (
              <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
                Showing the combined access of all selected users. Applying sets
                this exact access for every selected user.
              </p>
            )}

            {wsAccessError && (
              <div className="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {wsAccessError}
              </div>
            )}

            <div className="mt-4 flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={closeWorkspaceAccess}
                disabled={wsAccessBusy}
                className="h-7"
              >
                Cancel
              </Button>
              <Button
                type="button"
                onClick={() => void submitWorkspaceAccess()}
                disabled={wsAccessBusy || wsAccessLoading}
                className="h-7"
              >
                {wsAccessBusy && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                Apply
              </Button>
            </div>
          </div>
        </div>
      )}

      {deleteTarget && (
        <div
          className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 backdrop-blur-sm animate-in fade-in duration-150"
          role="dialog"
          aria-modal="true"
          aria-labelledby="delete-modal-title"
          onClick={(e) => {
            if (e.target === e.currentTarget) closeDelete();
          }}
        >
          <div className="w-[440px] max-w-[92vw] rounded-lg border border-border bg-card p-6 shadow-2xl animate-in zoom-in-95 fade-in slide-in-from-bottom-2 duration-200 ease-out">
            <div className="mb-4 flex items-start gap-3">
              <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-destructive/10">
                <Trash2 className="h-5 w-5 text-destructive" />
              </div>
              <div className="min-w-0 flex-1">
                <h3
                  id="delete-modal-title"
                  className="text-base font-semibold text-foreground"
                >
                  Delete user?
                </h3>
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  Remove{" "}
                  <span className="font-medium text-foreground">
                    {deleteTarget.name}
                  </span>{" "}
                  ({deleteTarget.email}) from this workspace? They'll lose
                  access immediately.
                </p>
              </div>
              <button
                type="button"
                onClick={closeDelete}
                disabled={deleteSubmitting}
                className="ml-2 inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {deleteError && (
              <div className="mb-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                {deleteError}
              </div>
            )}

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button
                type="button"
                variant="outline"
                onClick={closeDelete}
                disabled={deleteSubmitting}
                className="h-7"
              >
                Cancel
              </Button>
              <Button
                type="button"
                variant="destructive"
                onClick={() => void confirmDelete()}
                disabled={deleteSubmitting}
                className="h-7"
              >
                {deleteSubmitting && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                Yes, delete
              </Button>
            </div>
          </div>
        </div>
      )}

      {addSeatsOpen && seats && (
        <AddSeatsModal
          seats={seats}
          onClose={() => setAddSeatsOpen(false)}
          onDone={() => {
            setAddSeatsOpen(false);
            void loadSeats();
          }}
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
            <label className="text-muted-foreground" htmlFor="page-jump-members">
              Go to
            </label>
            <input
              id="page-jump-members"
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

function StatusPill({ status }: { status: MemberRow["status"] }) {
  const label =
    status === "active" ? "Active" : status === "pending" ? "Pending" : "Suspended";
  const dot =
    status === "active"
      ? "bg-emerald-500"
      : status === "pending"
        ? "bg-amber-500"
        : "bg-muted-foreground/60";
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-foreground/80">
      <span className={cn("h-2 w-2 rounded-full", dot)} aria-hidden="true" />
      {label}
    </span>
  );
}

