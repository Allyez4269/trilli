import { useEffect, useState } from "react";
import { Check, ChevronDown, ChevronRight, Folder, Layers, Loader2, Minus, Search } from "lucide-react";

import { api, type BrowseResponse, type Workspace } from "@/lib/api";
import { cn } from "@/lib/utils";

// WorkspaceFolderPicker assigns access per workspace: tick the workspace for
// whole access, or expand it and tick specific folders to scope access to a
// folder depth inside it. A workspace is whole XOR folder-scoped — ticking the
// workspace clears its folder picks, and ticking a folder unticks the
// workspace. Folder grants cascade to descendants server-side, so selecting a
// folder is enough (no need to tick every child).
//
// Long lists scroll; past a threshold a search box filters the workspaces.

export interface WSAccessValue {
  workspaceIds: number[]; // whole-workspace assignments
  folderIds: number[]; // folder-scoped grants (topmost folder per branch)
}

interface Node {
  id: number;
  name: string;
  children?: Node[]; // undefined = not loaded
  loading?: boolean;
}

const SEARCH_THRESHOLD = 7;

export function WorkspaceFolderPicker({
  workspaces,
  value,
  onChange,
  initialFolderWorkspaces,
}: {
  workspaces: Workspace[];
  value: WSAccessValue;
  onChange: (v: WSAccessValue) => void;
  // folderId -> workspaceId for already-granted folders, so a workspace shows
  // as partially selected on open without expanding its tree.
  initialFolderWorkspaces?: Record<number, number>;
}) {
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [trees, setTrees] = useState<Record<number, Node[]>>({});
  // folderId -> workspaceId, learned as trees load (drives the whole/folder XOR
  // and the partial-selection indicator). Seeded from existing grants.
  const [folderWs, setFolderWs] = useState<Record<number, number>>(
    () => ({ ...(initialFolderWorkspaces ?? {}) }),
  );

  // A workspace is "partially selected" when it isn't whole but holds at least
  // one selected folder (attributed via folderWs).
  const partialCount = (wid: number) =>
    value.folderIds.filter((fid) => folderWs[fid] === wid).length;

  const showSearch = workspaces.length > SEARCH_THRESHOLD;
  const q = query.trim().toLowerCase();
  const filtered = q ? workspaces.filter((w) => w.name.toLowerCase().includes(q)) : workspaces;

  const rememberFolders = (wid: number, ids: number[]) =>
    setFolderWs((prev) => {
      const next = { ...prev };
      ids.forEach((id) => (next[id] = wid));
      return next;
    });

  const loadRoots = async (wid: number) => {
    if (trees[wid]) return;
    setTrees((prev) => ({ ...prev, [wid]: prev[wid] ?? [] }));
    try {
      const res = await api.get<BrowseResponse>(`/api/browse?workspace_id=${wid}`);
      const nodes: Node[] = (res.folders ?? []).map((f) => ({ id: f.id, name: f.name }));
      setTrees((prev) => ({ ...prev, [wid]: nodes }));
      rememberFolders(wid, nodes.map((n) => n.id));
    } catch {
      setTrees((prev) => ({ ...prev, [wid]: [] }));
    }
  };

  const loadChildren = async (wid: number, parentId: number) => {
    setTrees((prev) => ({ ...prev, [wid]: mark(prev[wid] ?? [], parentId, { loading: true }) }));
    try {
      const res = await api.get<BrowseResponse>(`/api/browse?folder_id=${parentId}`);
      const children: Node[] = (res.folders ?? []).map((f) => ({ id: f.id, name: f.name }));
      setTrees((prev) => ({
        ...prev,
        [wid]: mark(prev[wid] ?? [], parentId, { children, loading: false }),
      }));
      rememberFolders(wid, children.map((n) => n.id));
    } catch {
      setTrees((prev) => ({ ...prev, [wid]: mark(prev[wid] ?? [], parentId, { loading: false }) }));
    }
  };

  const toggleExpand = (wid: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(wid)) next.delete(wid);
      else {
        next.add(wid);
        void loadRoots(wid);
      }
      return next;
    });
  };

  // When scoped to a single workspace (the Edit Workspace access editor), expand
  // it automatically so its folders are visible right away.
  useEffect(() => {
    if (workspaces.length === 1) {
      const only = workspaces[0].id;
      setExpanded((prev) => (prev.has(only) ? prev : new Set(prev).add(only)));
      void loadRoots(only);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaces.length === 1 ? workspaces[0]?.id : null]);

  const toggleWhole = (wid: number) => {
    if (value.workspaceIds.includes(wid)) {
      onChange({ ...value, workspaceIds: value.workspaceIds.filter((x) => x !== wid) });
    } else {
      onChange({
        workspaceIds: [...value.workspaceIds, wid],
        folderIds: value.folderIds.filter((fid) => folderWs[fid] !== wid),
      });
    }
  };

  // Toggling a folder. `checked` is its current effective state (inherited from
  // a whole workspace or an ancestor grant, or its own grant).
  const onToggleFolder = (wid: number, node: Node, checked: boolean) => {
    if (checked) demote(wid, node);
    else grant(wid, node);
  };

  // Uncheck a covered folder: drop the covering scope (whole workspace or the
  // nearest ancestor grant) and re-grant every sibling along the path EXCEPT the
  // unchecked folder — i.e. "everything except this", expressed as folder grants.
  const demote = (wid: number, node: Node) => {
    const roots = trees[wid] ?? [];
    const path = findPath(roots, node.id) ?? [node];
    let fids = value.folderIds.slice();
    let wids = value.workspaceIds.slice();

    let coverIdx = -1; // index in path of nearest ancestor grant; -1 = whole
    for (let i = path.length - 2; i >= 0; i--) {
      if (fids.includes(path[i].id)) {
        coverIdx = i;
        break;
      }
    }
    if (coverIdx === -1) {
      wids = wids.filter((x) => x !== wid); // was whole
    } else {
      fids = fids.filter((id) => id !== path[coverIdx].id);
    }
    for (let lvl = coverIdx + 1; lvl < path.length; lvl++) {
      const levelNodes = lvl === 0 ? roots : path[lvl - 1].children ?? [];
      for (const sib of levelNodes) {
        if (sib.id !== path[lvl].id) fids.push(sib.id);
      }
    }
    const subtree = collectSubtree(node);
    fids = fids.filter((id) => !subtree.has(id));
    onChange({ workspaceIds: wids, folderIds: unique(fids) });
  };

  // Check a folder: grant it (dropping now-redundant descendant grants). If that
  // makes every root folder granted, promote back to whole-workspace.
  const grant = (wid: number, node: Node) => {
    const subtree = collectSubtree(node);
    let fids = value.folderIds.filter((id) => !subtree.has(id));
    fids.push(node.id);
    let wids = value.workspaceIds.slice();

    const roots = trees[wid] ?? [];
    const wsGrants = new Set(fids.filter((id) => folderWs[id] === wid));
    if (roots.length > 0 && roots.every((r) => wsGrants.has(r.id))) {
      wids = wids.includes(wid) ? wids : [...wids, wid];
      fids = fids.filter((id) => folderWs[id] !== wid);
    }
    onChange({ workspaceIds: wids, folderIds: unique(fids) });
  };

  // parentChecked = the folder is covered by the whole workspace or an ancestor
  // grant, so it renders pre-checked even with no grant row of its own.
  const renderNodes = (wid: number, nodes: Node[], depth: number, parentChecked: boolean) =>
    nodes.map((n) => {
      const checked = parentChecked || value.folderIds.includes(n.id);
      const isOpen = expandedFolders.has(n.id);
      return (
        <div key={n.id}>
          <div
            className="flex items-center gap-1.5 rounded-md py-1 pr-2 hover:bg-muted"
            style={{ paddingLeft: `${depth * 16 + 8}px` }}
          >
            <button
              type="button"
              onClick={() => toggleFolderExpand(wid, n.id)}
              className="flex h-4 w-4 flex-shrink-0 items-center justify-center text-muted-foreground"
              aria-label={isOpen ? "Collapse" : "Expand"}
            >
              {n.loading ? (
                <Loader2 className="h-3 w-3 animate-spin" />
              ) : isOpen ? (
                <ChevronDown className="h-3.5 w-3.5" />
              ) : (
                <ChevronRight className="h-3.5 w-3.5" />
              )}
            </button>
            <button
              type="button"
              onClick={() => onToggleFolder(wid, n, checked)}
              className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
            >
              <span
                className={cn(
                  "flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border",
                  checked ? "border-primary bg-primary text-primary-foreground" : "border-foreground/30",
                )}
              >
                {checked && <Check className="h-3 w-3" />}
              </span>
              <Folder className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
              <span className="truncate text-sm text-foreground">{n.name}</span>
            </button>
          </div>
          {isOpen && n.children && n.children.length > 0 && renderNodes(wid, n.children, depth + 1, checked)}
          {isOpen && n.children && n.children.length === 0 && (
            <p
              className="py-1 text-[11px] text-muted-foreground"
              style={{ paddingLeft: `${(depth + 1) * 16 + 28}px` }}
            >
              No subfolders.
            </p>
          )}
        </div>
      );
    });

  // Folder expand state is separate from workspace expand state.
  const [expandedFolders, setExpandedFolders] = useState<Set<number>>(new Set());
  const toggleFolderExpand = (wid: number, fid: number) => {
    setExpandedFolders((prev) => {
      const next = new Set(prev);
      if (next.has(fid)) next.delete(fid);
      else {
        next.add(fid);
        const node = findNode(trees[wid] ?? [], fid);
        if (node && node.children === undefined) void loadChildren(wid, fid);
      }
      return next;
    });
  };

  if (workspaces.length === 0) {
    return (
      <div className="rounded-md border border-input bg-background px-3 py-4 text-center text-xs text-muted-foreground">
        No workspaces yet. Create one from Files first.
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {showSearch && (
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search workspaces…"
            className="h-7 w-full rounded-md border border-foreground/20 bg-background pl-8 pr-2 text-sm outline-none focus:border-primary/50"
          />
        </div>
      )}
      <div className="max-h-72 space-y-1 overflow-y-auto rounded-md border border-input bg-background p-1.5">
        {filtered.length === 0 ? (
          <p className="px-2 py-3 text-center text-xs text-muted-foreground">
            No workspaces match “{query.trim()}”.
          </p>
        ) : (
          filtered.map((w) => {
            const whole = value.workspaceIds.includes(w.id);
            const partial = !whole && partialCount(w.id) > 0;
            const isOpen = expanded.has(w.id);
            const tree = trees[w.id];
            return (
              <div key={w.id} className="rounded-md">
                <div
                  className={cn(
                    "flex items-center gap-1.5 rounded-md py-1.5 pr-2",
                    whole && "bg-primary/10",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => toggleExpand(w.id)}
                    className="flex h-5 w-5 flex-shrink-0 items-center justify-center text-muted-foreground"
                    aria-label={isOpen ? "Collapse" : "Expand"}
                  >
                    {isOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                  </button>
                  <button
                    type="button"
                    onClick={() => toggleWhole(w.id)}
                    className="flex min-w-0 flex-1 cursor-pointer items-center gap-2.5 text-left"
                    title={
                      partial
                        ? "Partial access — specific folders selected. Tick for the whole workspace."
                        : "Tick for full workspace access"
                    }
                  >
                    <span
                      className={cn(
                        "flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border",
                        whole
                          ? "border-primary bg-primary text-primary-foreground"
                          : partial
                            ? "border-primary text-primary"
                            : "border-foreground/30",
                      )}
                    >
                      {whole ? (
                        <Check className="h-3 w-3" />
                      ) : partial ? (
                        <Minus className="h-3 w-3" strokeWidth={3} />
                      ) : null}
                    </span>
                    <Layers className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">{w.name}</span>
                    {partial && (
                      <span className="flex-shrink-0 rounded-full bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                        {partialCount(w.id)} folder{partialCount(w.id) === 1 ? "" : "s"}
                      </span>
                    )}
                  </button>
                </div>
                {isOpen && (
                  <div className="pb-1">
                    {tree === undefined ? (
                      <div className="flex items-center gap-2 py-1 pl-9 text-[11px] text-muted-foreground">
                        <Loader2 className="h-3 w-3 animate-spin" /> Loading folders…
                      </div>
                    ) : tree.length === 0 ? (
                      <p className="py-1 pl-9 text-[11px] text-muted-foreground">
                        {whole ? "Whole workspace selected." : "No folders in this workspace yet."}
                      </p>
                    ) : (
                      renderNodes(w.id, tree, 1, whole)
                    )}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

// ----- tree helpers ---------------------------------------------------------

// findPath returns the chain of nodes from a root down to id (inclusive).
function findPath(nodes: Node[], id: number, acc: Node[] = []): Node[] | null {
  for (const n of nodes) {
    const here = [...acc, n];
    if (n.id === id) return here;
    if (n.children) {
      const hit = findPath(n.children, id, here);
      if (hit) return hit;
    }
  }
  return null;
}

// collectSubtree returns the ids of a node and all its loaded descendants.
function collectSubtree(node: Node): Set<number> {
  const out = new Set<number>([node.id]);
  const walk = (n: Node) => (n.children ?? []).forEach((c) => {
    out.add(c.id);
    walk(c);
  });
  walk(node);
  return out;
}

const unique = (ids: number[]): number[] => Array.from(new Set(ids));

function findNode(nodes: Node[], id: number): Node | null {
  for (const n of nodes) {
    if (n.id === id) return n;
    if (n.children) {
      const hit = findNode(n.children, id);
      if (hit) return hit;
    }
  }
  return null;
}

function mark(nodes: Node[], id: number, patch: Partial<Node>): Node[] {
  return nodes.map((n) => {
    if (n.id === id) return { ...n, ...patch };
    if (n.children) return { ...n, children: mark(n.children, id, patch) };
    return n;
  });
}
