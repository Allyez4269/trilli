// Trilli Productivity editor page — composes the Trilli toolbar over the
// embedded <TrilliEditor> canvas, filling the AppShell content area.
//
// Phase 2: the editor's live document is a per-session working blob in the
// tenant's own encrypted blob store. A session is minted on trilli-serve
// (POST /api/office/session) — for a brand-new doc or seeded from an existing
// Trilli file (Edit/Open). The server returns the WOPISrc + access token the
// engine loads; Save As / Download / Open / New all reference the session key.
// The server owns session sanitation (New/Open ends the prior session; a
// janitor reaps idle ones), so the client just reflects the current session.
import { useCallback, useEffect, useRef, useState } from "react";
import { Navigate, useLocation, useNavigate, useParams } from "react-router-dom";
import { CheckCircle2, Loader2, TriangleAlert } from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import { isAppEnabled, isProductivityAllowed } from "@/lib/productivity/access";
import { DocsMenuBar } from "@/components/productivity/DocsMenuBar";
import { DocsToolbar } from "@/components/productivity/DocsToolbar";
import { SheetsMenuBar } from "@/components/productivity/SheetsMenuBar";
import { SheetsToolbar } from "@/components/productivity/SheetsToolbar";
import { SlidesMenuBar } from "@/components/productivity/SlidesMenuBar";
import { SlidesToolbar } from "@/components/productivity/SlidesToolbar";
import { EditorToolbar } from "@/components/productivity/EditorToolbar";
import { FormatPanel } from "@/components/productivity/FormatPanel";
import { PresenceAvatars, type CollabView } from "@/components/productivity/PresenceAvatars";
import { TrilliEditor, type SaveState, type TrilliEditorHandle } from "@/components/productivity/TrilliEditor";
import { SaveToTrilliModal, type SaveTarget } from "@/components/productivity/SaveToTrilliModal";
import { NewDocumentConfirmModal } from "@/components/productivity/NewDocumentConfirmModal";
import { OpenFromTrilliModal } from "@/components/productivity/OpenFromTrilliModal";
import { appByKey } from "@/lib/productivity/apps";
import type { AppFormat } from "@/lib/productivity/apps";
import { fetchSession, resumeSession, type EditorSession } from "@/lib/productivity/wopi";
import { registerOfficeFlush } from "@/lib/productivity/sessionFlush";
import { api, type FileRecord } from "@/lib/api";

// Per-app file types for the Open dialog's type filter (first = default).
const OPEN_EXTS: Record<string, string[]> = {
  docs: ["docx", "doc", "odt", "rtf"],
  sheets: ["xlsx", "xls", "csv", "ods"],
  slides: ["pptx", "ppt", "odp"],
};

// Per-app default name for a brand-new file (the server is authoritative — see
// officesessions.Create — but the client seeds the UI with the same wording).
const DEFAULT_DOC_NAMES: Record<string, string> = {
  docs: "New document",
  sheets: "New spreadsheet",
  slides: "New presentation",
};
function defaultDocName(key: string | undefined): string {
  return DEFAULT_DOC_NAMES[key ?? ""] ?? "New document";
}
const FALLBACK_FORMAT: AppFormat = { ext: "docx", label: "DOCX" };

// stripOfficeExt removes a trailing source extension so the Save As modal's
// name field + format suffix don't double up. Covers every extension the
// editor can OPEN (see EDITABLE_EXTS in edit.ts), not just the native ones —
// otherwise opening e.g. "sales.csv" would save as "sales.csv.xlsx".
function stripOfficeExt(name: string): string {
  return name.replace(
    /\.(docx|doc|odt|rtf|txt|md|markdown|log|rst|asciidoc|xlsx|xls|xlsm|csv|tsv|ods|pptx|ppt|pps|ppsx|odp)$/i,
    "",
  );
}

export default function ProductivityEditor() {
  const { app: appKey } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const { identity } = useAuth();
  const app = appByKey(appKey);
  // When navigated here from the file browser's "Edit" action, router state
  // carries the file id to open directly (instead of starting a new doc).
  const openFileId = (location.state as { fileId?: number } | null)?.fileId ?? null;

  const [state, setState] = useState<SaveState>("loading");
  const [cmdStates, setCmdStates] = useState<Record<string, string>>({});
  const [views, setViews] = useState<CollabView[]>([]);
  const [sourceDeleted, setSourceDeleted] = useState(false);
  const [panelOpen, setPanelOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [openOpen, setOpenOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const editorRef = useRef<TrilliEditorHandle>(null);
  const [downloading, setDownloading] = useState(false);
  // The active edit session (server-minted). null while a session is being
  // minted — the iframe must NOT mount until the working blob is ready, or the
  // engine races onto stale/empty bytes.
  const [session, setSession] = useState<EditorSession | null>(null);
  // The id of the last file Saved As to Trilli (Print uses it). Cleared on New.
  const [lastSavedFileId, setLastSavedFileId] = useState<number | null>(null);
  const [format, setFormat] = useState<AppFormat>(() => {
    const saved = sessionStorage.getItem(`trilli.office.format.${app?.key ?? ""}`);
    return app?.formats.find((f) => f.ext === saved) ?? app?.formats[0] ?? FALLBACK_FORMAT;
  });
  const [docName, setDocName] = useState<string>(defaultDocName(app?.key));
  // Unsaved-changes confirm modal (File > New / Open while the doc has edits).
  const [confirmNew, setConfirmNew] = useState<null | (() => void)>(null);
  const [savingForNew, setSavingForNew] = useState(false);
  const pendingAfterSaveRef = useRef<null | (() => void)>(null);

  const exec = useCallback(
    (command: string, args?: Record<string, unknown>) => editorRef.current?.exec(command, args),
    [],
  );

  const setCurrentDoc = useCallback((name: string) => setDocName(name), []);

  const changeFormat = useCallback(
    (f: AppFormat) => {
      setFormat(f);
      try {
        sessionStorage.setItem(`trilli.office.format.${app?.key ?? ""}`, f.ext);
      } catch {
        /* ignore */
      }
    },
    [app?.key],
  );

  // startSession mints a fresh edit session on trilli-serve. fileId opens an
  // existing Trilli file (Edit/Open); omit it for a blank New doc. Replaces any
  // prior session — the server ends the prior session for this user+app first
  // (sanitation), so the scratch area never accumulates stale docs.
  const startSession = useCallback(
    async (fileId?: number) => {
      if (!app) return null;
      setSession(null);
      setState("loading");
      try {
        const s = await fetchSession(app, { fileId: fileId ?? null });
        setSession(s);
        setDocName(s.fileName);
        if (fileId) setLastSavedFileId(fileId);
        else setLastSavedFileId(null);
        return s;
      } catch {
        setToast("Couldn't start the editor");
        return null;
      }
    },
    [app],
  );

  // Mount: open the Edit file if present, else start a blank New doc. Runs once.
  const initRef = useRef(false);
  useEffect(() => {
    if (!app || initRef.current) return;
    initRef.current = true;
    window.scrollTo(0, 0);
    document.querySelector('main [class*="overflow"]')?.scrollTo(0, 0);
    // If arriving via Edit (openFileId), create a new session seeded from that
    // file. Otherwise, try to RESUME the user's active session — the engine
    // auto-saves to the working blob every ~30s via WOPI PutFile, so on refresh
    // the working blob holds the latest edits + inserted images. Only create a
    // new blank session when no active session exists.
    if (openFileId) {
      void startSession(openFileId).then((s) => {
        if (s) setToast(`Opened ${s.fileName}`);
      });
    } else {
      void resumeSession(app).then((s) => {
        if (s) {
          setSession(s);
          setDocName(s.fileName);
          setState("loading");
          // If the resumed session was previously saved (has a source file ID),
          // set lastSavedFileId so subsequent Save clicks silently overwrite
          // instead of opening the Save As modal.
          setLastSavedFileId(s.sourceFileId ?? null);
        } else {
          void startSession(undefined);
        }
      });
    }
  }, [app, openFileId, startSession]);

  // Let logout flush this editor's pending edits to the working blob BEFORE it
  // tears down office sessions (avoids the teardown race / lost last edits).
  // save() is the put_seq flush handshake; bounded by flushOfficeBeforeTeardown.
  useEffect(() => {
    registerOfficeFlush(async () => {
      try {
        await editorRef.current?.save();
      } catch {
        /* best-effort */
      }
    });
    return () => registerOfficeFlush(null);
  }, []);

  // Detect the source file being deleted out from under a live session (e.g. a
  // co-editor trashed it). Poll while we have a session bound to a real file.
  useEffect(() => {
    if (!session?.sessionKey) return;
    let stop = false;
    const check = async () => {
      try {
        const r = await api.get<{ source_deleted: boolean }>(
          `/api/office/session/status?key=${encodeURIComponent(session.sessionKey)}`,
        );
        if (!stop) setSourceDeleted(!!r.source_deleted);
      } catch {
        /* transient — keep the last known state */
      }
    };
    void check();
    const id = window.setInterval(check, 12000);
    return () => {
      stop = true;
      window.clearInterval(id);
    };
  }, [session?.sessionKey]);

  // Auto-dismiss the toast.
  useEffect(() => {
    if (!toast) return;
    const t = window.setTimeout(() => setToast(null), 3200);
    return () => window.clearTimeout(t);
  }, [toast]);

  const handleSaveToTrilli = useCallback(
    async (t: SaveTarget) => {
      if (!session) return;
      // Note: the caller (handleSave or the Save As modal) is responsible for
      // flushing the engine via editorRef.save() BEFORE calling this. The
      // server's SaveAs handler also polls the working blob for a size change
      // to ensure it reads the fresh bytes.
      const overwriteId = t.overwriteFileId ?? lastSavedFileId ?? undefined;
      const file = await api.post<{ id?: number; name?: string; resaved_new?: boolean }>("/api/office/save-as", {
        session_key: session.sessionKey,
        name: t.name,
        format: t.format,
        workspace_id: t.workspaceId,
        folder_id: t.folderId,
        overwrite_file_id: overwriteId ?? null,
      });
      setCurrentDoc(file?.name ?? `${stripOfficeExt(t.name)}.${t.format}`);
      if (file?.id) setLastSavedFileId(file.id);
      setSaveOpen(false);
      if (file?.resaved_new) {
        // the original was deleted; we preserved the work as a new file
        setSourceDeleted(false);
        setToast("The original file was deleted — your changes were saved as a new copy.");
        const afterNew = pendingAfterSaveRef.current;
        pendingAfterSaveRef.current = null;
        if (afterNew) afterNew();
        return;
      }
      setToast(
        t.location
          ? `Saved to ${t.location}`
          : app?.key === "sheets"
            ? "Spreadsheet saved"
            : app?.key === "slides"
              ? "Presentation saved"
              : "Document saved",
      );
      const afterSave = pendingAfterSaveRef.current;
      pendingAfterSaveRef.current = null;
      if (afterSave) afterSave();
    },
    [session, setCurrentDoc, lastSavedFileId],
  );

  // handleSave: the toolbar Save button + File > Save.
  // - If the doc was never saved (no lastSavedFileId), open the Save As modal.
  // - If already saved, silently overwrite the existing file (no modal).
  const handleSave = useCallback(async () => {
    if (lastSavedFileId == null) {
      setSaveOpen(true);
      return;
    }
    // Silently overwrite the existing file. AWAIT the editor flush and only
    // proceed once it's CONFIRMED durable — if save() rejects (the engine never
    // confirmed the upload), do NOT overwrite the file with stale bytes.
    try {
      await editorRef.current?.save();
    } catch {
      setToast("Couldn't save your changes — the editor didn't confirm them. Please try again.");
      return;
    }
    await handleSaveToTrilli({
      workspaceId: 0,
      folderId: null,
      name: stripOfficeExt(docName),
      format: format.ext,
      location: docName,
      overwriteFileId: lastSavedFileId,
    });
  }, [lastSavedFileId, handleSaveToTrilli, docName, format]);

  const handleOpenFromTrilli = useCallback(
    (f: FileRecord) => {
      guardNew(() => startSession(f.id).then((s) => {
        setOpenOpen(false);
        if (s) setToast(`Opened ${s.fileName}`);
      }));
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [startSession],
  );

  const hasUnsavedChanges = state === "modified" || (lastSavedFileId === null && state !== "loading");

  const guardNew = useCallback(
    (proceed: () => void) => {
      if (hasUnsavedChanges) {
        setConfirmNew(() => proceed);
      } else {
        proceed();
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [hasUnsavedChanges],
  );

  const handleNew = useCallback(() => {
    guardNew(() =>
      startSession(undefined).then(
        (s) =>
          s &&
          setToast(
            app?.key === "sheets"
              ? "Started a new spreadsheet"
              : app?.key === "slides"
                ? "Started a new presentation"
                : "Started a new document",
          ),
      ),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [guardNew, startSession]);

  // Print converts the LIVE working blob to PDF (via headless LibreOffice on
  // trilli-serve) and opens it in a hidden iframe → the browser's native print
  // dialog. Works from the auto-saved scratch — no explicit Save As required.
  // The host flushes the engine's latest edits first (editor.save()).
  const handlePrint = useCallback(async () => {
    if (!session) return;
    // Confirm the engine flushed before printing, so the PDF reflects the latest
    // edits rather than a stale working blob.
    try {
      await editorRef.current?.save();
    } catch {
      setToast(
        `Couldn't prepare the ${app?.key === "sheets" ? "spreadsheet" : app?.key === "slides" ? "presentation" : "document"} for printing — please try again.`,
      );
      return;
    }
    const url = `/api/office/print?session_key=${session.sessionKey}`;
    const iframe = document.createElement("iframe");
    iframe.style.position = "fixed";
    iframe.style.right = "0";
    iframe.style.bottom = "0";
    iframe.style.width = "0";
    iframe.style.height = "0";
    iframe.style.border = "0";
    iframe.setAttribute("aria-hidden", "true");
    iframe.setAttribute("title", "print");
    iframe.src = url;
    let printed = false;
    const doPrint = () => {
      if (printed) return;
      printed = true;
      try {
        iframe.contentWindow?.focus();
        iframe.contentWindow?.print();
      } catch {
        /* cross-origin PDF plugin can't be scripted — fall back to a new tab */
        window.open(url, "_blank", "noopener");
      }
      window.setTimeout(() => iframe.remove(), 10000);
    };
    iframe.onload = () => window.setTimeout(doPrint, 300);
    document.body.appendChild(iframe);
    window.setTimeout(doPrint, 6000);
  }, [session]);

  const handleDownload = useCallback(async () => {
    if (!app || !session || downloading) return;
    setDownloading(true);
    try {
      await editorRef.current?.save();
      const params = new URLSearchParams({
        session_key: session.sessionKey,
        format: format.ext,
        name: docName,
      });
      const res = await fetch(`/api/office/download?${params.toString()}`, { credentials: "include" });
      if (!res.ok) {
        let msg = "Download failed";
        try {
          const j = await res.json();
          if (j?.error) msg = j.error;
        } catch {
          /* ignore */
        }
        setToast(msg);
        return;
      }
      const blob = await res.blob();
      const cd = res.headers.get("Content-Disposition") || "";
      const m = cd.match(/filename="([^"]+)"/);
      const fname = m?.[1] || `${stripOfficeExt(docName)}.${format.ext}`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = fname;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      setToast(`Downloaded ${fname}`);
    } catch {
      setToast("Download failed");
    } finally {
      setDownloading(false);
    }
  }, [app, session, format, docName, downloading]);

  // Gated preview — non-allowlisted users can't reach an app's editor by direct
  // URL. isAppEnabled is the single rollout gate (Docs GA; Sheets allowlisted;
  // Slides pre-release), so an unenabled app bounces back to the launcher.
  if (!isProductivityAllowed(identity?.user?.email)) return <Navigate to="/home" replace />;
  if (!app || !isAppEnabled(app.key, identity?.user?.email)) return <Navigate to="/apps/productivity" replace />;

  // While a session is being minted (working blob seeded server-side), show a
  // loading state — mounting the iframe early races the engine onto empty bytes.
  if (!session) {
    return (
      <div className="flex h-full min-h-0 flex-col overflow-hidden">
        <EditorToolbar
          app={app}
          fileName={docName}
          state="loading"
          format={format}
          onSave={handleSave}
          onDownload={handleDownload}
          downloading={downloading}
          onBack={() => navigate("/apps/productivity")}
        />
        <div className="flex min-h-0 flex-1 items-center justify-center text-sm text-muted-foreground">
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />{" "}
          {app.key === "sheets" ? "Opening spreadsheet…" : app.key === "slides" ? "Opening presentation…" : "Opening document…"}
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <EditorToolbar
        app={app}
        fileName={docName}
        state={state}
        format={format}
        onSave={handleSave}
        onDownload={handleDownload}
        downloading={downloading}
        onBack={() => navigate("/apps/productivity")}
      />
      {app.key === "docs" && (
        <>
          <DocsMenuBar
            presence={<PresenceAvatars views={views} />}
            exec={exec}
            onSave={handleSave}
            onSaveAs={() => setSaveOpen(true)}
            onDownload={handleDownload}
            onPrint={handlePrint}
            onNew={handleNew}
            onOpen={() => setOpenOpen(true)}
            onInsertImage={() => editorRef.current?.insertImage()}
          />
          <DocsToolbar
            exec={exec}
            states={cmdStates}
            onTogglePanel={() => setPanelOpen((v) => !v)}
            panelOpen={panelOpen}
            onInsertImage={() => editorRef.current?.insertImage()}
            onZoom={(dir) => editorRef.current?.zoomBy(dir)}
            onPrint={handlePrint}
          />
        </>
      )}
      {app.key === "sheets" && (
        // 3-row layout (like Docs): the horizontal menu bar above the toolbar.
        <>
          <SheetsMenuBar
            presence={<PresenceAvatars views={views} />}
            exec={exec}
            onSave={handleSave}
            onSaveAs={() => setSaveOpen(true)}
            onDownload={handleDownload}
            onPrint={handlePrint}
            onNew={handleNew}
            onOpen={() => setOpenOpen(true)}
            onInsertImage={() => editorRef.current?.insertImage()}
          />
          <SheetsToolbar
            exec={exec}
            states={cmdStates}
            onInsertImage={() => editorRef.current?.insertImage()}
            onZoom={(dir) => editorRef.current?.zoomBy(dir)}
            onPrint={handlePrint}
          />
        </>
      )}
      {app.key === "slides" && (
        // 3-row layout (like Docs/Sheets): horizontal menu bar above the toolbar.
        <>
          <SlidesMenuBar
            presence={<PresenceAvatars views={views} />}
            exec={exec}
            onSave={handleSave}
            onSaveAs={() => setSaveOpen(true)}
            onDownload={handleDownload}
            onPrint={handlePrint}
            onNew={handleNew}
            onOpen={() => setOpenOpen(true)}
            onInsertImage={() => editorRef.current?.insertImage()}
            onPresent={() => editorRef.current?.present()}
          />
          <SlidesToolbar
            exec={exec}
            states={cmdStates}
            onInsertImage={() => editorRef.current?.insertImage()}
            onZoom={(dir) => editorRef.current?.zoomBy(dir)}
            onPrint={handlePrint}
            onPresent={() => editorRef.current?.present()}
          />
        </>
      )}
      {sourceDeleted && (
        <div className="flex flex-shrink-0 items-center gap-2 border-b border-amber-300 bg-amber-50 px-4 py-2 text-[13px] text-amber-900">
          <TriangleAlert className="h-4 w-4 flex-shrink-0 text-amber-600" />
          <span className="min-w-0 flex-1">
            This file was deleted by someone else. Your changes are safe here — use{" "}
            <span className="font-semibold">Save</span> to keep them as a new file.
          </span>
          <button
            type="button"
            onClick={() => setSaveOpen(true)}
            className="flex-shrink-0 rounded-md bg-amber-600 px-3 py-1 text-[12.5px] font-semibold text-white hover:bg-amber-700"
          >
            Save a copy
          </button>
        </div>
      )}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <div className="min-w-0 flex-1 overflow-hidden">
          <TrilliEditor
            ref={editorRef}
            session={session}
            appKey={app.key}
            onStateChange={setState}
            onCommandState={setCmdStates}
        onViews={setViews}
            onNotice={setToast}
          />
        </div>
        {app.key === "docs" && panelOpen && (
          <FormatPanel exec={exec} states={cmdStates} onClose={() => setPanelOpen(false)} />
        )}
      </div>

      {saveOpen && (
        <SaveToTrilliModal
          defaultName={stripOfficeExt(docName)}
          formats={app.formats}
          format={format}
          onFormatChange={changeFormat}
          onClose={() => setSaveOpen(false)}
          onSave={async (t) => {
            // Flush the engine and only save once the upload is CONFIRMED durable;
            // if it can't be confirmed, don't write a stale/blank file.
            try {
              await editorRef.current?.save();
            } catch {
              setToast("Couldn't save your changes — the editor didn't confirm them. Please try again.");
              return;
            }
            await handleSaveToTrilli(t);
          }}
        />
      )}
      {openOpen && (
        <OpenFromTrilliModal
          extensions={OPEN_EXTS[app.key] ?? ["docx"]}
          onClose={() => setOpenOpen(false)}
          onOpen={handleOpenFromTrilli}
        />
      )}
      {confirmNew && (
        <NewDocumentConfirmModal
          fileName={docName}
          saving={savingForNew}
          onClose={() => setConfirmNew(null)}
          onDiscard={() => {
            const proceed = confirmNew;
            setConfirmNew(null);
            proceed();
          }}
          onSave={() => {
            setSavingForNew(false);
            pendingAfterSaveRef.current = confirmNew;
            setConfirmNew(null);
            setSaveOpen(true);
          }}
        />
      )}
      {toast && (
        <div className="pointer-events-none fixed bottom-5 right-5 z-[90] flex items-center gap-2 rounded-lg border border-border bg-card px-4 py-2.5 text-[13px] text-foreground shadow-lg">
          <CheckCircle2 className="h-4 w-4 text-emerald-500" />
          {toast}
        </div>
      )}
    </div>
  );
}
