import { useCallback, useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import {
  Download,
  File as FileIcon,
  Folder as FolderIcon,
  Loader2,
  Lock,
  ShieldAlert,
} from "lucide-react";

import { api, ApiError, type ShareBrowseEntry, type ShareMeta } from "@/lib/api";
import { TrilliLogo } from "@/components/Logo";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 ** 3) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}

export default function ShareLanding() {
  const { token = "" } = useParams();
  const [meta, setMeta] = useState<ShareMeta | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [pw, setPw] = useState("");
  const [unlocked, setUnlocked] = useState(false);
  // Name revealed only after the password unlocks it (PublicMeta hides it for
  // protected shares, so it's never exposed on the public landing page).
  const [unlockedName, setUnlockedName] = useState<string | null>(null);
  const [working, setWorking] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);

  // Folder browsing state
  const [entries, setEntries] = useState<ShareBrowseEntry[] | null>(null);
  const [crumbs, setCrumbs] = useState<{ id: number; name: string }[]>([]);

  const base = `/api/s/${encodeURIComponent(token)}`;
  const pwQuery = (extra?: string) => {
    const parts: string[] = [];
    if (meta?.has_password && pw) parts.push(`pw=${encodeURIComponent(pw)}`);
    if (extra) parts.push(extra);
    return parts.length ? `?${parts.join("&")}` : "";
  };

  // Share links are private, unlisted, token-gated (often password-protected) —
  // keep them out of search engines (backs up the server's X-Robots-Tag header).
  useEffect(() => {
    const tag = document.createElement("meta");
    tag.name = "robots";
    tag.content = "noindex, nofollow";
    document.head.appendChild(tag);
    return () => {
      document.head.removeChild(tag);
    };
  }, []);

  useEffect(() => {
    api
      .get<ShareMeta>(base)
      .then(setMeta)
      .catch((e) => setLoadErr(e instanceof ApiError ? e.message : "This link is unavailable."));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const browse = useCallback(
    async (folderID?: number) => {
      setWorking(true);
      setActionErr(null);
      try {
        const q = pwQuery(folderID ? `folder_id=${folderID}` : undefined);
        const r = await api.get<{ resource_name: string; entries: ShareBrowseEntry[] }>(`${base}/browse${q}`);
        setEntries(r.entries);
        if (r.resource_name) setUnlockedName(r.resource_name);
        setUnlocked(true);
      } catch (e) {
        setActionErr(e instanceof ApiError ? e.message : "Couldn't open this link.");
      } finally {
        setWorking(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [base, meta, pw],
  );

  // Auto-load folder contents once we know it's an unprotected folder.
  useEffect(() => {
    if (meta?.resource_type === "folder" && !meta.has_password && entries === null) {
      void browse();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [meta]);

  const downloadFile = async (fileID?: number) => {
    setWorking(true);
    setActionErr(null);
    try {
      const res = await fetch(`${base}/download${pwQuery(fileID ? `file_id=${fileID}` : undefined)}`);
      if (!res.ok) {
        let msg = "Download failed.";
        try {
          msg = (await res.json()).error ?? msg;
        } catch {
          /* ignore */
        }
        throw new Error(msg);
      }
      const blob = await res.blob();
      const cd = res.headers.get("Content-Disposition") ?? "";
      const m = /filename="(.+?)"/.exec(cd);
      const name = m ? m[1] : meta?.resource_name ?? "download";
      const u = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = u;
      a.download = name;
      a.click();
      URL.revokeObjectURL(u);
      setUnlocked(true);
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : "Download failed.");
    } finally {
      setWorking(false);
    }
  };

  const openInto = (e: ShareBrowseEntry) => {
    if (e.kind === "folder") {
      setCrumbs((c) => [...c, { id: e.id, name: e.name }]);
      void browse(e.id);
    } else {
      void downloadFile(e.id);
    }
  };

  const goCrumb = (idx: number) => {
    if (idx < 0) {
      setCrumbs([]);
      void browse();
    } else {
      const next = crumbs.slice(0, idx + 1);
      setCrumbs(next);
      void browse(next[next.length - 1].id);
    }
  };

  const locked = !!meta?.has_password && !unlocked;
  const displayName = unlockedName || meta?.resource_name || "";
  const year = new Date().getFullYear();

  return (
    <div className="flex min-h-screen flex-col bg-sidebar">
      <main className="flex flex-1 flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-lg">
        <div className="mb-5 flex justify-center">
          <TrilliLogo className="h-7" />
        </div>

        <div className="overflow-hidden rounded-2xl border border-border bg-card shadow-sm">
          {loadErr ? (
            <Centered icon={ShieldAlert} title="Link unavailable" body={loadErr} />
          ) : !meta ? (
            <div className="py-16 text-center">
              <Loader2 className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="p-6">
              {/* Header */}
              <div className="flex items-center gap-3 border-b border-border pb-4">
                {locked ? (
                  <span className="relative flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                    <Download className="h-5 w-5" />
                    <span className="absolute -bottom-0.5 -right-0.5 flex h-3.5 w-3.5 items-center justify-center rounded-full bg-primary text-primary-foreground ring-2 ring-card">
                      <Lock className="h-2 w-2" />
                    </span>
                  </span>
                ) : meta.resource_type === "folder" ? (
                  <FolderIcon className="h-9 w-9 flex-shrink-0 text-primary" />
                ) : (
                  <FileIcon className="h-9 w-9 flex-shrink-0 text-muted-foreground" />
                )}
                <div className="min-w-0">
                  <p className="truncate text-base font-semibold text-foreground">
                    {locked ? "Secure Protected Download" : displayName}
                  </p>
                  <p className="text-[12px] text-muted-foreground">
                    {locked
                      ? "Password-protected · authorized access only"
                      : `Shared with you via Trilli · ${meta.capability === "download" ? "view & download" : "view only"}`}
                  </p>
                </div>
              </div>

              {/* Password gate */}
              {locked ? (
                <div className="space-y-3 pt-4">
                  {/* Authorized-personnel disclosure */}
                  <div className="rounded-lg border border-amber-400/40 bg-amber-50 p-3 text-[12.5px] leading-relaxed text-amber-900 dark:border-amber-400/20 dark:bg-amber-500/10 dark:text-amber-200">
                    <p className="mb-1 flex items-center gap-1.5 font-semibold">
                      <ShieldAlert className="h-4 w-4 flex-shrink-0" /> Restricted — authorized personnel only
                    </p>
                    <p>
                      This is a protected archive intended solely for authorized recipients. Its contents are confidential
                      and access may be logged. By entering the password you confirm you are authorized to access and
                      retrieve this material. Unauthorized access, use, or distribution is prohibited.
                    </p>
                  </div>
                  <p className="flex items-center gap-1.5 text-[13px] text-muted-foreground">
                    <Lock className="h-3.5 w-3.5" /> Enter the password to continue.
                  </p>
                  <input
                    type="password"
                    value={pw}
                    onChange={(e) => setPw(e.target.value)}
                    placeholder="Enter password"
                    onKeyDown={(e) => e.key === "Enter" && (meta.resource_type === "folder" ? browse() : downloadFile())}
                    className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm"
                  />
                  {actionErr && <p className="text-[12px] text-destructive">{actionErr}</p>}
                  <button
                    onClick={() => (meta.resource_type === "folder" ? browse() : downloadFile())}
                    disabled={working || !pw}
                    className="inline-flex h-10 w-full items-center justify-center gap-2 rounded-md bg-primary text-sm font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
                  >
                    {working ? <Loader2 className="h-4 w-4 animate-spin" /> : <Lock className="h-4 w-4" />}
                    Unlock
                  </button>
                </div>
              ) : meta.resource_type === "file" ? (
                <div className="space-y-3 pt-4">
                  {actionErr && <p className="text-[12px] text-destructive">{actionErr}</p>}
                  <button
                    onClick={() => downloadFile()}
                    disabled={working || meta.capability !== "download"}
                    className="inline-flex h-10 w-full items-center justify-center gap-2 rounded-md bg-primary text-sm font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60"
                  >
                    {working ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
                    {meta.capability === "download" ? "Download" : "View only — download disabled"}
                  </button>
                </div>
              ) : (
                /* Folder listing */
                <div className="pt-4">
                  <div className="mb-2 flex flex-wrap items-center gap-1 text-[12px] text-muted-foreground">
                    <button onClick={() => goCrumb(-1)} className="hover:text-foreground">{displayName}</button>
                    {crumbs.map((c, i) => (
                      <span key={c.id} className="flex items-center gap-1">
                        <span>/</span>
                        <button onClick={() => goCrumb(i)} className="hover:text-foreground">{c.name}</button>
                      </span>
                    ))}
                  </div>
                  {actionErr && <p className="mb-2 text-[12px] text-destructive">{actionErr}</p>}
                  <div className="divide-y divide-border rounded-lg border border-border">
                    {(entries ?? []).length === 0 && !working ? (
                      <p className="px-3 py-6 text-center text-[13px] text-muted-foreground">This folder is empty.</p>
                    ) : (
                      (entries ?? []).map((e) => (
                        <button
                          key={`${e.kind}-${e.id}`}
                          onClick={() => openInto(e)}
                          className="flex w-full items-center gap-2.5 px-3 py-2.5 text-left text-[13px] hover:bg-muted/40"
                        >
                          {e.kind === "folder" ? (
                            <FolderIcon className="h-4 w-4 text-primary/70" />
                          ) : (
                            <FileIcon className="h-4 w-4 text-muted-foreground" />
                          )}
                          <span className="flex-1 truncate">{e.name}</span>
                          {e.kind === "file" && (
                            <span className="text-[11px] text-muted-foreground">{fmtBytes(e.size_bytes)}</span>
                          )}
                          {e.kind === "file" && <Download className="h-3.5 w-3.5 text-muted-foreground" />}
                        </button>
                      ))
                    )}
                    {working && (
                      <div className="px-3 py-4 text-center">
                        <Loader2 className="mx-auto h-4 w-4 animate-spin text-muted-foreground" />
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
        <p className="mt-4 text-center text-[11px] text-muted-foreground">Powered by Trilli</p>
      </div>
      </main>

      {/* Anchored footer */}
      <footer className="border-t border-border/60 px-4 py-5">
        <div className="mx-auto flex max-w-lg flex-col items-center gap-1.5 text-center">
          <div className="flex items-center gap-2.5 text-[12px]">
            <a
              href="https://trilli.com/terms"
              target="_blank"
              rel="noopener noreferrer"
              className="text-muted-foreground transition-colors hover:text-foreground"
            >
              Terms of Service
            </a>
            <span className="text-muted-foreground/40">·</span>
            <a
              href="https://trilli.com/privacy"
              target="_blank"
              rel="noopener noreferrer"
              className="text-muted-foreground transition-colors hover:text-foreground"
            >
              Privacy Policy
            </a>
          </div>
          <p className="text-[11px] text-muted-foreground">© {year} Trilli Media LLC. All rights reserved.</p>
        </div>
      </footer>
    </div>
  );
}

function Centered({ icon: Icon, title, body }: { icon: typeof ShieldAlert; title: string; body: string }) {
  return (
    <div className="px-6 py-14 text-center">
      <Icon className="mx-auto h-9 w-9 text-muted-foreground/50" />
      <p className="mt-3 text-base font-semibold text-foreground">{title}</p>
      <p className="mx-auto mt-1 max-w-sm text-[13px] text-muted-foreground">{body}</p>
    </div>
  );
}
