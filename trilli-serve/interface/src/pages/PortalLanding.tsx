import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { CheckCircle2, Inbox, Loader2, Lock, ShieldAlert, UploadCloud } from "lucide-react";

import { api, ApiError, type PortalMeta } from "@/lib/api";
import { TrilliLogo } from "@/components/Logo";

type Done = { name: string; ok: boolean; error?: string };

export default function PortalLanding() {
  const { token = "" } = useParams();
  const [meta, setMeta] = useState<PortalMeta | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [pw, setPw] = useState("");
  const [unlocked, setUnlocked] = useState(false);
  const [pwErr, setPwErr] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [done, setDone] = useState<Done[]>([]);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const base = `/api/p/${encodeURIComponent(token)}`;

  useEffect(() => {
    api
      .get<PortalMeta>(base)
      .then(setMeta)
      .catch((e) => setLoadErr(e instanceof ApiError ? e.message : "This link is unavailable."));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const needsUnlock = meta?.has_password && !unlocked;

  const uploadOne = (file: File) =>
    new Promise<Done>((resolve) => {
      const xhr = new XMLHttpRequest();
      const q = meta?.has_password && pw ? `?pw=${encodeURIComponent(pw)}` : "";
      xhr.open("POST", `${base}/upload${q}`);
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          resolve({ name: file.name, ok: true });
        } else {
          let msg = "Upload failed.";
          try {
            msg = JSON.parse(xhr.responseText).error ?? msg;
          } catch {
            /* ignore */
          }
          resolve({ name: file.name, ok: false, error: msg });
        }
      };
      xhr.onerror = () => resolve({ name: file.name, ok: false, error: "Network error" });
      const fd = new FormData();
      fd.append("file", file, file.name);
      xhr.send(fd);
    });

  const handleFiles = async (files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 0) return;
    // If protected and not yet verified, verify password by attempting the
    // first upload; a 401 surfaces as a password error.
    setUploading(true);
    setPwErr(null);
    for (const f of list) {
      const r = await uploadOne(f);
      if (!r.ok && /password/i.test(r.error ?? "") && !unlocked) {
        setPwErr(r.error ?? "Incorrect password");
        setUploading(false);
        return;
      }
      setUnlocked(true);
      setDone((d) => [r, ...d]);
    }
    setUploading(false);
  };

  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-sidebar px-4 py-10">
      <div className="w-full max-w-lg">
        <div className="mb-5 flex justify-center">
          <TrilliLogo className="h-7" />
        </div>

        <div className="overflow-hidden rounded-2xl border border-border bg-card shadow-sm">
          {loadErr ? (
            <div className="px-6 py-14 text-center">
              <ShieldAlert className="mx-auto h-9 w-9 text-muted-foreground/50" />
              <p className="mt-3 text-base font-semibold text-foreground">Link unavailable</p>
              <p className="mx-auto mt-1 max-w-sm text-[13px] text-muted-foreground">{loadErr}</p>
            </div>
          ) : !meta ? (
            <div className="py-16 text-center">
              <Loader2 className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="p-6">
              <div className="flex items-center gap-3 border-b border-border pb-4">
                <Inbox className="h-9 w-9 text-primary" />
                <div className="min-w-0">
                  <p className="truncate text-base font-semibold text-foreground">{meta.title || "Upload files"}</p>
                  <p className="text-[12px] text-muted-foreground">Send files to {meta.folder_name} via Trilli — no account needed.</p>
                </div>
              </div>

              {needsUnlock && done.length === 0 && (
                <div className="space-y-2 pt-4">
                  <p className="flex items-center gap-1.5 text-[13px] text-muted-foreground">
                    <Lock className="h-3.5 w-3.5" /> This request is password protected — enter it, then upload.
                  </p>
                  <input
                    type="password"
                    value={pw}
                    onChange={(e) => setPw(e.target.value)}
                    placeholder="Enter password"
                    className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm"
                  />
                  {pwErr && <p className="text-[12px] text-destructive">{pwErr}</p>}
                </div>
              )}

              {/* Drop zone */}
              <div
                onDragOver={(e) => {
                  e.preventDefault();
                  setDragOver(true);
                }}
                onDragLeave={() => setDragOver(false)}
                onDrop={(e) => {
                  e.preventDefault();
                  setDragOver(false);
                  if (e.dataTransfer.files.length) void handleFiles(e.dataTransfer.files);
                }}
                onClick={() => inputRef.current?.click()}
                className={`mt-4 flex cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed px-6 py-10 text-center transition-colors ${
                  dragOver ? "border-primary bg-primary/5" : "border-border hover:border-primary/40"
                }`}
              >
                {uploading ? (
                  <Loader2 className="h-7 w-7 animate-spin text-primary" />
                ) : (
                  <UploadCloud className="h-7 w-7 text-muted-foreground" />
                )}
                <p className="mt-2 text-sm font-medium text-foreground">
                  {uploading ? "Uploading…" : "Drop files here or click to choose"}
                </p>
                <p className="text-[12px] text-muted-foreground">They go straight to {meta.folder_name}.</p>
                <input
                  ref={inputRef}
                  type="file"
                  multiple
                  className="hidden"
                  onChange={(e) => e.target.files && handleFiles(e.target.files)}
                />
              </div>

              {done.length > 0 && (
                <ul className="mt-4 space-y-1.5">
                  {done.map((d, i) => (
                    <li key={i} className="flex items-center gap-2 text-[13px]">
                      {d.ok ? (
                        <CheckCircle2 className="h-4 w-4 text-emerald-600" />
                      ) : (
                        <ShieldAlert className="h-4 w-4 text-destructive" />
                      )}
                      <span className="flex-1 truncate text-foreground">{d.name}</span>
                      <span className={d.ok ? "text-[12px] text-muted-foreground" : "text-[12px] text-destructive"}>
                        {d.ok ? "Sent" : d.error}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
        <p className="mt-4 text-center text-[11px] text-muted-foreground">Powered by Trilli</p>
      </div>
    </div>
  );
}
