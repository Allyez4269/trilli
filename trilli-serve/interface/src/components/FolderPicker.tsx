import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight, Folder } from "lucide-react";

import { api, type BrowseResponse, type FolderRecord } from "@/lib/api";
import { checkboxCls, cn } from "@/lib/utils";

// FolderPicker is a multi-select tree of the tenant's folders. Children
// are lazy-loaded on expand. When a folder is checked, every loaded
// descendant is also auto-checked — the user can then uncheck any
// subfolder individually. Newly-loaded descendants of a checked folder
// are auto-checked the moment they arrive, so expand-after-check still
// surfaces them as ticked by default.

interface FolderPickerProps {
  value: number[];
  onChange: (folderIds: number[]) => void;
}

interface FolderNode {
  id: number;
  name: string;
  children?: FolderNode[]; // undefined = not loaded; [] = loaded, no children
  loading?: boolean;
}

export function FolderPicker({ value, onChange }: FolderPickerProps) {
  const [roots, setRoots] = useState<FolderNode[] | null>(null);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .get<BrowseResponse>("/api/browse")
      .then((res) => {
        if (cancelled) return;
        setRoots((res.folders ?? []).map(folderToNode));
        setError(null);
      })
      .catch(() => {
        if (!cancelled) setError("Couldn't load folders");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const loadChildren = async (parentId: number) => {
    try {
      const res = await api.get<BrowseResponse>(`/api/browse?folder_id=${parentId}`);
      const children = (res.folders ?? []).map(folderToNode);
      setRoots((prev) => updateNode(prev, parentId, (n) => ({ ...n, children, loading: false })));
      // If the parent is currently selected, auto-extend that selection
      // to the freshly-loaded children — keeps the "check parent =
      // descendants pre-checked" promise even when lazy-loaded later.
      if (value.includes(parentId) && children.length > 0) {
        const next = new Set(value);
        children.forEach((c) => next.add(c.id));
        onChange([...next]);
      }
    } catch {
      setRoots((prev) => updateNode(prev, parentId, (n) => ({ ...n, loading: false })));
    }
  };

  const toggleExpand = (n: FolderNode) => {
    const next = new Set(expanded);
    if (next.has(n.id)) {
      next.delete(n.id);
    } else {
      next.add(n.id);
      if (n.children === undefined && !n.loading) {
        setRoots((prev) => updateNode(prev, n.id, (x) => ({ ...x, loading: true })));
        void loadChildren(n.id);
      }
    }
    setExpanded(next);
  };

  const toggleCheck = (id: number) => {
    if (value.includes(id)) {
      // Plain uncheck — only this folder leaves the set. Descendants
      // already in the set stay (the user might still want them).
      onChange(value.filter((v) => v !== id));
      return;
    }
    // Check this folder + every loaded descendant in one go.
    const node = findNode(roots, id);
    const ids = new Set(value);
    ids.add(id);
    if (node) collectLoadedDescendants(node).forEach((did) => ids.add(did));
    onChange([...ids]);
  };

  if (error) {
    return (
      <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
        {error}
      </div>
    );
  }

  if (roots === null) {
    return (
      <div className="rounded-md border border-input bg-background px-3 py-4 text-center text-xs text-muted-foreground">
        Loading folders…
      </div>
    );
  }

  if (roots.length === 0) {
    return (
      <div className="rounded-md border border-input bg-background px-3 py-4 text-center text-xs text-muted-foreground">
        No folders yet. Create one in Files before inviting Members or Viewers.
      </div>
    );
  }

  return (
    <div className="space-y-1.5">
      <div className="max-h-[220px] overflow-y-auto rounded-md border border-input bg-background p-1">
        {roots.map((n) => (
          <TreeRow
            key={n.id}
            node={n}
            depth={0}
            selected={value}
            expanded={expanded}
            onToggleExpand={toggleExpand}
            onToggleCheck={toggleCheck}
          />
        ))}
      </div>
      <p className="text-[11px] text-muted-foreground">
        {value.length === 0
          ? "No folders selected yet."
          : `${value.length} folder${value.length === 1 ? "" : "s"} selected. ` +
            "Subfolders are pre-checked when their parent is — uncheck any you'd like to leave out."}
      </p>
    </div>
  );
}

function TreeRow({
  node,
  depth,
  selected,
  expanded,
  onToggleExpand,
  onToggleCheck,
}: {
  node: FolderNode;
  depth: number;
  selected: number[];
  expanded: Set<number>;
  onToggleExpand: (n: FolderNode) => void;
  onToggleCheck: (id: number) => void;
}) {
  const isExpanded = expanded.has(node.id);
  const isChecked = selected.includes(node.id);
  const hasChildren = node.children === undefined || (node.children?.length ?? 0) > 0;

  return (
    <>
      <div
        className="flex items-center gap-1 rounded px-1.5 py-1 text-xs transition-colors hover:bg-muted/60"
        style={{ paddingLeft: `${depth * 14 + 6}px` }}
      >
        <button
          type="button"
          onClick={() => onToggleExpand(node)}
          className={cn(
            "inline-flex h-4 w-4 flex-shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted",
            !hasChildren && "invisible",
          )}
          aria-label={isExpanded ? "Collapse" : "Expand"}
        >
          {isExpanded ? (
            <ChevronDown className="h-3 w-3" />
          ) : (
            <ChevronRight className="h-3 w-3" />
          )}
        </button>
        <input
          type="checkbox"
          checked={isChecked}
          onChange={() => onToggleCheck(node.id)}
          className={checkboxCls}
          aria-label={`Select ${node.name}`}
        />
        <Folder
          className={cn(
            "h-3.5 w-3.5 flex-shrink-0",
            isChecked ? "fill-amber-300 text-amber-500" : "text-muted-foreground",
          )}
        />
        <button
          type="button"
          onClick={() => onToggleCheck(node.id)}
          className="flex-1 truncate text-left text-foreground"
        >
          {node.name}
        </button>
      </div>
      {isExpanded &&
        node.children?.map((child) => (
          <TreeRow
            key={child.id}
            node={child}
            depth={depth + 1}
            selected={selected}
            expanded={expanded}
            onToggleExpand={onToggleExpand}
            onToggleCheck={onToggleCheck}
          />
        ))}
    </>
  );
}

function folderToNode(f: FolderRecord): FolderNode {
  return { id: f.id, name: f.name };
}

function findNode(nodes: FolderNode[] | null, id: number): FolderNode | null {
  if (!nodes) return null;
  for (const n of nodes) {
    if (n.id === id) return n;
    if (n.children) {
      const hit = findNode(n.children, id);
      if (hit) return hit;
    }
  }
  return null;
}

// Walk down all currently-loaded descendants of a node and return
// their ids. Unloaded branches are skipped — when they're expanded
// later, loadChildren auto-extends the selection.
function collectLoadedDescendants(node: FolderNode): number[] {
  const out: number[] = [];
  if (!node.children) return out;
  for (const child of node.children) {
    out.push(child.id);
    out.push(...collectLoadedDescendants(child));
  }
  return out;
}

function updateNode(
  nodes: FolderNode[] | null,
  id: number,
  patch: (n: FolderNode) => FolderNode,
): FolderNode[] | null {
  if (nodes === null) return null;
  return nodes.map((n) => {
    if (n.id === id) return patch(n);
    if (n.children) return { ...n, children: updateNode(n.children, id, patch)! };
    return n;
  });
}
