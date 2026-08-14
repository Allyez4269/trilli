// Cloud Import — the import wizard.
//   1. select   — pick a provider (only Google Drive is live)
//   2. connect  — OAuth consent popup (drive.file: non-sensitive, any user)
//   3. transfer — 50/50: Google Drive (Picker) on the left → a native Trilli
//                 destination navigator (workspace dropdown + folders) on the right
//   4. done     — import summary
// The import runs file-by-file with a shared progress modal + cancel.
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  File,
  Folder,
  Layers,
  Loader2,
  Lock,
  ShieldCheck,
  X,
} from "lucide-react";

import { cn } from "@/lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TransferProgressModal } from "@/components/TransferProgressModal";
import {
  api,
  type BreadcrumbItem,
  type BrowseResponse,
  type FolderRecord,
  type Workspace,
  type WorkspacesResponse,
} from "@/lib/api";
import { connectGoogle, disconnectGoogle, getPickerToken, getStatus, importFiles } from "@/lib/cloudimport/api";
import { openDrivePicker, type PickedFile } from "@/lib/cloudimport/picker";
import { CLOUD_PROVIDERS, GoogleDriveLogo, type CloudProvider } from "./providers";

type Step = "select" | "connect" | "transfer" | "done";
type Dest = { folderId: number | null; workspaceId: number | null; label: string };

function formatSize(n: number): string {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${u[i]}`;
}

export function CloudImportModal({
  open,
  onClose,
  destinationLabel,
  destinationFolderId,
  destinationWorkspaceId,
  onImported,
}: {
  open: boolean;
  onClose: () => void;
  destinationLabel: string;
  destinationFolderId: number | null;
  destinationWorkspaceId: number | null;
  onImported?: () => void;
}) {
  const [step, setStep] = useState<Step>("select");
  const [provider, setProvider] = useState<CloudProvider | null>(null);
  const [connecting, setConnecting] = useState(false);
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);

  const [picked, setPicked] = useState<PickedFile[]>([]);
  const [picking, setPicking] = useState(false);
  const [result, setResult] = useState<{ imported: number; failed: number; cancelled?: boolean } | null>(null);

  const [transfer, setTransfer] = useState<{ done: number; total: number; name: string } | null>(null);
  const cancelRef = useRef(false);

  const [dest, setDest] = useState<Dest>({
    folderId: destinationFolderId,
    workspaceId: destinationWorkspaceId,
    label: destinationLabel,
  });

  const reset = useCallback(() => {
    setStep("select");
    setProvider(null);
    setConnecting(false);
    setError(null);
    setPicked([]);
    setPicking(false);
    setTransfer(null);
    setResult(null);
    setDest({ folderId: destinationFolderId, workspaceId: destinationWorkspaceId, label: destinationLabel });
  }, [destinationFolderId, destinationWorkspaceId, destinationLabel]);

  const close = () => {
    reset();
    onClose();
  };

  useEffect(() => {
    if (!open) return;
    reset();
    let cancelled = false;
    void (async () => {
      const s = await getStatus();
      if (cancelled || !s.connected) return;
      setProvider(CLOUD_PROVIDERS.find((p) => p.key === "google") ?? null);
      setEmail(s.email ?? "");
      setStep("transfer");
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (!open) return null;

  const pickProvider = (p: CloudProvider) => {
    if (!p.enabled) return;
    setProvider(p);
    setStep("connect");
  };

  const doConnect = async () => {
    setConnecting(true);
    setError(null);
    const res = await connectGoogle();
    setConnecting(false);
    if (res.status === "ok") {
      setEmail(res.email ?? "");
      setStep("transfer");
    } else {
      setError(res.error ?? "Could not connect. Please try again.");
    }
  };

  const openPicker = async () => {
    setPicking(true);
    setError(null);
    const { token, error: tErr } = await getPickerToken();
    if (!token) {
      setPicking(false);
      setError(
        tErr === "picker_not_configured"
          ? "The Google Picker isn't configured yet (missing API key)."
          : tErr === "not_connected"
            ? "Your connection expired — please reconnect."
            : "Couldn't open Google Drive. Please try again.",
      );
      return;
    }
    try {
      const docs = await openDrivePicker({ accessToken: token.access_token, apiKey: token.api_key, appId: token.app_id });
      if (docs.length) {
        setPicked((prev) => {
          const seen = new Set(prev.map((p) => p.id));
          return [...prev, ...docs.filter((d) => !seen.has(d.id))];
        });
      }
    } catch {
      setError("Couldn't open the Google Picker.");
    } finally {
      setPicking(false);
    }
  };

  const removePicked = (id: string) => setPicked((prev) => prev.filter((p) => p.id !== id));

  // Import file-by-file so we can show real progress and cancel between files.
  const doImport = async () => {
    if (picked.length === 0) return;
    cancelRef.current = false;
    setError(null);
    const total = picked.length;
    setTransfer({ done: 0, total, name: picked[0].name });
    let imported = 0;
    let failed = 0;
    for (let i = 0; i < total; i++) {
      if (cancelRef.current) break;
      setTransfer({ done: i, total, name: picked[i].name });
      let res: Awaited<ReturnType<typeof importFiles>>;
      try {
        res = await importFiles([picked[i].id], dest.folderId, dest.workspaceId);
      } catch {
        res = { error: "failed" };
      }
      if (res.error || (res.imported ?? 0) < 1) failed++;
      else imported++;
    }
    const wasCancelled = cancelRef.current;
    setTransfer(null);
    setResult({ imported, failed, cancelled: wasCancelled });
    setStep("done");
    if (imported > 0) onImported?.();
  };

  const cancelImport = () => {
    cancelRef.current = true;
  };

  const doDisconnect = async () => {
    await disconnectGoogle();
    setEmail("");
    setPicked([]);
    setStep("select");
  };

  return (
    <>
      <div className="fixed inset-0 z-[150] flex items-center justify-center p-4">
        <div className="absolute inset-0 bg-foreground/40 backdrop-blur-[2px]" onClick={close} />
        <div className="relative flex max-h-[90vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl">
          {/* header */}
          <div className="flex items-center gap-3 border-b border-border px-5 py-3.5">
            {step === "connect" && (
              <button type="button" onClick={() => setStep("select")} className="text-muted-foreground hover:text-foreground" aria-label="Back">
                <ArrowLeft className="h-4 w-4" />
              </button>
            )}
            <div className="min-w-0 flex-1">
              <h2 className="text-[15px] font-semibold text-foreground">Cloud Import</h2>
              <p className="truncate text-[12px] text-muted-foreground">
                {step === "select" && "Bring your files from another cloud into Trilli"}
                {step === "connect" && `Connect your ${provider?.name} account`}
                {step === "transfer" && (email ? `Connected as ${email} · ${provider?.name ?? "Google Drive"}` : "Choose what to import")}
                {step === "done" && "Import complete"}
              </p>
            </div>
            {step === "transfer" && email && (
              <button type="button" onClick={doDisconnect} className="text-[12px] text-muted-foreground hover:text-foreground">
                Disconnect
              </button>
            )}
            <button type="button" onClick={close} className="text-muted-foreground hover:text-foreground" aria-label="Close">
              <X className="h-5 w-5" />
            </button>
          </div>

          {error && <div className="border-b border-border bg-destructive/10 px-5 py-2 text-[12.5px] text-destructive">{error}</div>}

          {/* body */}
          <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
            {step === "select" && <SelectStep onPick={pickProvider} />}
            {step === "connect" && provider && <ConnectStep provider={provider} connecting={connecting} onConnect={doConnect} />}
            {step === "transfer" && (
              <TransferStep
                picked={picked}
                picking={picking}
                onPick={openPicker}
                onRemove={removePicked}
                initialWorkspaceId={destinationWorkspaceId}
                initialFolderId={destinationFolderId}
                onDestChange={setDest}
              />
            )}
            {step === "done" && result && <DoneStep result={result} destinationLabel={dest.label} />}
          </div>

          {/* footer */}
          {step === "transfer" && (
            <div className="flex items-center justify-between gap-3 border-t border-border px-5 py-3">
              <span className="truncate text-[12.5px] text-muted-foreground">
                {picked.length === 0
                  ? "Select files from Drive"
                  : `${picked.length} file${picked.length === 1 ? "" : "s"} → ${dest.label}`}
              </span>
              <button
                type="button"
                onClick={doImport}
                disabled={picked.length === 0 || !!transfer}
                className="inline-flex min-w-[11rem] items-center justify-center gap-2 rounded-lg bg-primary px-4 py-2 text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
              >
                Import to Trilli
              </button>
            </div>
          )}
          {step === "done" && (
            <div className="flex justify-end border-t border-border px-5 py-3">
              <button type="button" onClick={close} className="rounded-lg bg-primary px-4 py-2 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90">
                Done
              </button>
            </div>
          )}
        </div>
      </div>

      {transfer && (
        <TransferProgressModal
          title="Importing from Google Drive"
          done={transfer.done}
          total={transfer.total}
          currentLabel={transfer.name}
          onCancel={cancelImport}
          indeterminate
        />
      )}
    </>
  );
}

function SelectStep({ onPick }: { onPick: (p: CloudProvider) => void }) {
  return (
    <div className="grid gap-3 p-5 sm:grid-cols-3">
      {CLOUD_PROVIDERS.map((p) => (
        <button
          key={p.key}
          type="button"
          onClick={() => onPick(p)}
          disabled={!p.enabled}
          className={cn(
            "group relative flex flex-col items-center gap-3 rounded-xl border p-5 text-center transition-all",
            p.enabled
              ? "cursor-pointer border-border bg-card hover:border-primary/50 hover:shadow-md"
              : "cursor-not-allowed border-border/60 bg-muted/30",
          )}
        >
          <p.Logo className={cn("h-10 w-10", !p.enabled && "opacity-40 grayscale")} />
          <div>
            <div className={cn("text-[13.5px] font-semibold", p.enabled ? "text-foreground" : "text-muted-foreground")}>{p.name}</div>
            <div className="text-[11.5px] text-muted-foreground">{p.blurb}</div>
          </div>
          {!p.enabled && (
            <span className="absolute right-2 top-2 rounded-full bg-secondary px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
              Soon
            </span>
          )}
        </button>
      ))}
    </div>
  );
}

function ConnectStep({ provider, connecting, onConnect }: { provider: CloudProvider; connecting: boolean; onConnect: () => void }) {
  return (
    <div className="flex flex-col items-center gap-5 px-6 py-10 text-center">
      <provider.Logo className="h-14 w-14" />
      <div>
        <p className="text-[15px] font-semibold text-foreground">Connect {provider.name}</p>
        <p className="mx-auto mt-1 max-w-sm text-[12.5px] text-muted-foreground">
          You'll sign in and choose which files to share with Trilli. We only ever see the files you pick — nothing else
          in your {provider.name}.
        </p>
      </div>
      <button
        type="button"
        onClick={onConnect}
        disabled={connecting}
        className="inline-flex items-center gap-2 rounded-lg border border-border bg-card px-5 py-2.5 text-[13.5px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:opacity-60"
      >
        {connecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <provider.Logo className="h-4 w-4" />}
        {connecting ? "Waiting for Google…" : `Sign in with ${provider.name}`}
      </button>
      <div className="flex items-center gap-1.5 text-[11.5px] text-muted-foreground">
        <ShieldCheck className="h-3.5 w-3.5" /> You choose exactly which files to share
      </div>
    </div>
  );
}

function TransferStep({
  picked,
  picking,
  onPick,
  onRemove,
  initialWorkspaceId,
  initialFolderId,
  onDestChange,
}: {
  picked: PickedFile[];
  picking: boolean;
  onPick: () => void;
  onRemove: (id: string) => void;
  initialWorkspaceId: number | null;
  initialFolderId: number | null;
  onDestChange: (d: Dest) => void;
}) {
  return (
    <div className="grid min-h-[24rem] flex-1 grid-cols-2 divide-x divide-border">
      {/* left — Google Drive (Picker) */}
      <div className="flex min-w-0 flex-col p-4">
        <div className="mb-2 flex items-center gap-2 text-[12px] font-semibold uppercase tracking-wide text-muted-foreground">
          <GoogleDriveLogo className="h-4 w-4" /> Google Drive
        </div>
        <button
          type="button"
          onClick={onPick}
          disabled={picking}
          className="mb-3 inline-flex items-center gap-2 self-start rounded-lg border border-border bg-card px-3.5 py-2 text-[13px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:opacity-60"
        >
          {picking ? <Loader2 className="h-4 w-4 animate-spin" /> : <GoogleDriveLogo className="h-4 w-4" />}
          {picking ? "Opening…" : picked.length ? "Add more files" : "Choose files from Google Drive"}
        </button>

        {picked.length === 0 ? (
          <div className="flex flex-1 items-center justify-center rounded-lg border border-dashed border-border text-[13px] text-muted-foreground">
            No files chosen yet
          </div>
        ) : (
          <ul className="min-h-0 flex-1 space-y-1 overflow-y-auto pr-1">
            {picked.map((f) => (
              <li key={f.id} className="flex items-center gap-2.5 rounded-lg border border-border bg-card px-2.5 py-2">
                <File className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-[13px] text-foreground">{f.name}</span>
                {f.sizeBytes > 0 && <span className="flex-shrink-0 text-[11px] text-muted-foreground">{formatSize(f.sizeBytes)}</span>}
                <button type="button" onClick={() => onRemove(f.id)} className="text-muted-foreground hover:text-foreground" aria-label="Remove">
                  <X className="h-4 w-4" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* right — Trilli destination */}
      <div className="flex min-w-0 flex-col p-4">
        <div className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted-foreground">Trilli destination</div>
        <DestinationPicker initialWorkspaceId={initialWorkspaceId} initialFolderId={initialFolderId} onSelect={onDestChange} />
      </div>
    </div>
  );
}

function DestinationPicker({
  initialWorkspaceId,
  initialFolderId,
  onSelect,
}: {
  initialWorkspaceId: number | null;
  initialFolderId: number | null;
  onSelect: (d: Dest) => void;
}) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [wsId, setWsId] = useState<number | null>(initialWorkspaceId);
  const [folderId, setFolderId] = useState<number | null>(initialFolderId);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [crumbs, setCrumbs] = useState<BreadcrumbItem[]>([]);
  const [loading, setLoading] = useState(false);

  const wsName = (id: number | null) => workspaces.find((w) => w.id === id)?.name ?? "Files";

  const report = useCallback(
    (ws: number | null, folder: number | null, cr: BreadcrumbItem[], list: Workspace[]) => {
      const name = cr.length ? cr[cr.length - 1].name : (list.find((w) => w.id === ws)?.name ?? "Files");
      onSelect({ folderId: folder, workspaceId: ws, label: `Trilli · ${name}` });
    },
    [onSelect],
  );

  const browse = useCallback(async (ws: number, folder: number | null): Promise<BreadcrumbItem[]> => {
    setLoading(true);
    try {
      const qs = new URLSearchParams({ workspace_id: String(ws) });
      if (folder != null) qs.set("folder_id", String(folder));
      const b = await api.get<BrowseResponse>(`/api/browse?${qs.toString()}`);
      setFolders(b.folders ?? []);
      setCrumbs(b.breadcrumb ?? []);
      return b.breadcrumb ?? [];
    } catch {
      setFolders([]);
      setCrumbs([]);
      return [];
    } finally {
      setLoading(false);
    }
  }, []);

  // Initial load: workspaces + the starting folder.
  useEffect(() => {
    let on = true;
    void (async () => {
      try {
        const r = await api.get<WorkspacesResponse>("/api/workspaces");
        if (!on) return;
        setWorkspaces(r.workspaces);
        const init =
          initialWorkspaceId != null && r.workspaces.some((w) => w.id === initialWorkspaceId)
            ? initialWorkspaceId
            : (r.workspaces[0]?.id ?? null);
        setWsId(init);
        if (init != null) {
          const cr = await browse(init, initialFolderId);
          if (on) report(init, initialFolderId, cr, r.workspaces);
        }
      } catch {
        /* ignore */
      }
    })();
    return () => {
      on = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const selectWorkspace = async (id: number) => {
    setWsId(id);
    setFolderId(null);
    const cr = await browse(id, null);
    report(id, null, cr, workspaces);
  };
  const enterFolder = async (f: FolderRecord) => {
    if (wsId == null) return;
    setFolderId(f.id);
    const cr = await browse(wsId, f.id);
    report(wsId, f.id, cr, workspaces);
  };
  const goCrumb = async (idx: number) => {
    if (wsId == null) return;
    const target = idx < 0 ? null : crumbs[idx].id;
    setFolderId(target);
    const cr = await browse(wsId, target);
    report(wsId, target, cr, workspaces);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* workspace dropdown (Files-style, with the workspace icon) */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="mb-2 flex h-9 w-full items-center gap-2 rounded-lg border border-border bg-background px-2.5 text-[13px] outline-none transition-colors hover:bg-muted/40 focus:border-primary focus-visible:outline-none"
          >
            <Layers className="h-4 w-4 flex-shrink-0 text-primary" />
            <span className="min-w-0 flex-1 truncate text-left text-foreground">{wsName(wsId)}</span>
            <ChevronDown className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-64 w-[--radix-dropdown-menu-trigger-width] min-w-[12rem] overflow-y-auto">
          {workspaces.map((w) => (
            <DropdownMenuItem key={w.id} onSelect={() => void selectWorkspace(w.id)} className="gap-2 text-[13px]">
              <Layers className="h-4 w-4 text-primary" />
              <span className="min-w-0 flex-1 truncate">{w.name}</span>
              {w.id === wsId && <Check className="h-4 w-4 flex-shrink-0 text-primary" />}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>

      {/* breadcrumb */}
      <div className="mb-1.5 flex flex-wrap items-center gap-1 text-[11.5px] text-muted-foreground">
        <button
          type="button"
          onClick={() => void goCrumb(-1)}
          className={cn("hover:text-foreground", folderId == null && "font-medium text-foreground")}
        >
          {wsName(wsId)}
        </button>
        {crumbs.map((c, i) => (
          <span key={c.id} className="flex items-center gap-1">
            <ChevronRight className="h-3 w-3 opacity-50" />
            <button
              type="button"
              onClick={() => void goCrumb(i)}
              className={cn("max-w-[9rem] truncate hover:text-foreground", i === crumbs.length - 1 && "font-medium text-foreground")}
            >
              {c.name}
            </button>
          </span>
        ))}
      </div>

      {/* folders */}
      <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border border-border">
        {loading ? (
          <div className="flex h-full items-center justify-center py-8">
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          </div>
        ) : folders.length === 0 ? (
          <div className="flex h-full items-center justify-center px-3 py-8 text-center text-[12.5px] text-muted-foreground">
            No subfolders — files import here
          </div>
        ) : (
          folders.map((f) => (
            <button
              key={f.id}
              type="button"
              onClick={() => void enterFolder(f)}
              className="flex w-full items-center gap-2.5 border-b border-border px-3 py-2 text-left text-[13px] last:border-b-0 hover:bg-muted/50"
            >
              <Folder className="h-4 w-4 flex-shrink-0 fill-amber-300 text-amber-500" />
              <span className="min-w-0 flex-1 truncate text-foreground">{f.name}</span>
              <ChevronRight className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
            </button>
          ))
        )}
      </div>

      <p className="mt-2 flex items-center gap-1.5 text-[11.5px] text-muted-foreground">
        <Lock className="h-3.5 w-3.5 flex-shrink-0" />
        Files are saved to encrypted storage in this workspace.
      </p>
    </div>
  );
}

function DoneStep({
  result,
  destinationLabel,
}: {
  result: { imported: number; failed: number; cancelled?: boolean };
  destinationLabel: string;
}) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
      <CheckCircle2 className="h-12 w-12 text-emerald-500" />
      <p className="text-[15px] font-semibold text-foreground">
        {result.cancelled ? "Import stopped" : result.imported > 0 ? "Success!" : "Nothing imported"}
      </p>
      <p className="text-[12.5px] text-muted-foreground">
        {result.imported > 0
          ? `${result.imported} file${result.imported === 1 ? "" : "s"} saved to ${destinationLabel}.`
          : "No files were imported."}
        {result.failed > 0 && ` ${result.failed} item${result.failed === 1 ? "" : "s"} couldn't be imported.`}
      </p>
    </div>
  );
}
