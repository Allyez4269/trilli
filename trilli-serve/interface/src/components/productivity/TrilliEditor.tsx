// <TrilliEditor> — the embeddable React wrapper around the Collabora engine.
//
// It loads the editor into an iframe via a form POST (so the access_token rides
// in the POST body, never the URL/history), then bridges the Collabora
// postMessage API: it reports load/modified/save state up to the host, hides
// Collabora's menubar once the doc is loaded so the surface reads as Trilli, and
// exposes an imperative save() for our own toolbar.
//
// Collabora only emits postMessages when CheckFileInfo returns PostMessageOrigin
// = this app's origin (the test WOPI host is configured to do so for Phase 1).
import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { EditorSession } from "@/lib/productivity/wopi";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";

export type SaveState = "loading" | "ready" | "modified" | "saving" | "saved";

export interface TrilliEditorHandle {
  // Resolves once the engine confirms the save (Action_Save_Resp), so callers
  // can read the freshly-written bytes. Falls back after a timeout.
  save: () => Promise<void>;
  // Send a UNO command to the engine (e.g. ".uno:Bold"), optional UNO Args.
  exec: (command: string, args?: Record<string, unknown>) => void;
  // Open a file picker (host gesture) and insert the chosen image.
  insertImage: () => void;
  // Step the zoom by +1/-1 engine level (host-tracked + persisted).
  zoomBy: (dir: 1 | -1) => void;
  // Start the fullscreen slideshow (Impress) — triggers the engine's client
  // presentation, not a server UNO dispatch (which can't open the slideshow).
  present: () => void;
}

const FRAME_NAME = "trilli-office-frame";

// Host-controlled zoom. The engine's internal default-zoom path is gated by
// early-returns and proved unreliable, so the host owns the zoom level: we set it
// on load with the same .uno commands the toolbar uses (which DO work), and
// persist it per-browser. Engine zoom levels for Writer: 8=70%, 9=85%, 10=100%,
// 11=120%, ... up to 13=170% (max). Default 11 = 120%.
// localStorage key — a FAST CACHE of the resolved zoom (instant apply on load).
// The source of truth is the server (user pref → tenant default → this code
// default); the cache is reconciled from /api/productivity/settings on mount.
const ZOOM_MIN = 1;
const ZOOM_MAX = 13;
// Zoom is per-APP: Slides has its own default + cache key, isolated from the
// Docs/Sheets zoom. The engine's zoom levels are GEOMETRIC (~×1.2/step: 8≈70%,
// 9≈85%, 10=100%), so there's no integer level for 90/95%. We target ~95% with a
// FRACTIONAL level (~9.7); the engine either renders it or snaps to the nearest
// level (85/100). (Bump the slides key version when changing the default so the
// stale cached value is discarded and users land on the new default.)
function zoomKeyFor(appKey?: string): string {
  return appKey === "slides" ? "trilli.office.zoomLevel.slides.v3" : "trilli.office.zoomLevel.v2";
}
function zoomDefaultFor(appKey?: string): number {
  return appKey === "slides" ? 9.7 : 9; // ~95% for Slides; 85% otherwise
}
function readZoomLevel(appKey?: string): number {
  const v = parseInt(localStorage.getItem(zoomKeyFor(appKey)) || "", 10);
  return Number.isNaN(v) ? zoomDefaultFor(appKey) : Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, v));
}

export const TrilliEditor = forwardRef<
  TrilliEditorHandle,
  {
    session: EditorSession;
    // App key ("docs" | "sheets" | "slides") — drives the app-shaped loading
    // skeleton (a document page for docs/slides, a spreadsheet grid for sheets).
    appKey?: string;
    onStateChange?: (s: SaveState) => void;
    // Live command states from the engine (e.g. {".uno:Bold":"true"}) so the
    // native toolbar can reflect the cursor's current formatting.
    onCommandState?: (states: Record<string, string>) => void;
    onViews?: (views: import("@/components/productivity/PresenceAvatars").CollabView[]) => void;
    // Optional surface for host-visible notices (e.g. image-upload failure).
    onNotice?: (msg: string) => void;
  }
>(function TrilliEditor({ session, appKey, onStateChange, onCommandState, onViews, onNotice }, ref) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  // Tracks whether the engine has sent us anything yet, so the load-failure
  // fallback timer doesn't fire after a successful load.
  const loadedRef = useRef(false);
  // Accumulated command states from CommandStateChanged events.
  const statesRef = useRef<Record<string, string>>({});
  // Monotonic "edited since our last SUCCESSFUL save?" flag. Set true on any
  // Doc_ModifiedStatus(Modified:true); cleared ONLY when save() confirms a
  // durable flush. Unlike the engine's instantaneous modified flag, this is
  // never falsely clear while a WOPI upload is still pending — that false-clear
  // is what let subsequent saves export pre-edit bytes ("doesn't stick").
  const dirtySinceLastSaveRef = useRef(false);
  // Set when the engine acks a save (Action_Save_Resp) — a SECONDARY "nothing
  // left to upload" signal used only inside save() (see there).
  const saveRespSeenRef = useRef(false);
  // The session's put_seq captured at EDIT time (the clean→dirty transition),
  // before the engine's ~2s idle-save flushes it. save() uses it to recognise
  // "the edits are already on disk" when a manual Save produces no NEW PutFile —
  // which is what deadlocked Calc saves (idle-save beats the click; the engine
  // re-saves nothing and emits no Action_Save_Resp, so the other resolve paths
  // never fire). -1 = no pending edit captured.
  const dirtyAtSeqRef = useRef(-1);
  // Host-tracked zoom level (persisted per-browser).
  const zoomRef = useRef<number>(readZoomLevel(appKey));
  const [failed, setFailed] = useState(false);
  // The canvas stays masked until chrome-hide + default zoom have applied, so the
  // user never sees the brief 100%→zoomed readjustment on load.
  const [revealed, setRevealed] = useState(false);

  const setState = (s: SaveState) => onStateChange?.(s);

  // Post a Collabora WOPI postMessage command into the editor frame.
  const post = (MessageId: string, Values: Record<string, unknown> = {}) => {
    iframeRef.current?.contentWindow?.postMessage(
      JSON.stringify({ MessageId, Values }),
      session.engineOrigin,
    );
  };

  // Read the session's flush counter. put_seq increments on EVERY WOPI PutFile,
  // so it detects the engine's flush even when the re-saved document has the same
  // byte length (size_bytes would miss that). Component-scope so both save() and
  // the Doc_ModifiedStatus handler can use it. -1 on error.
  const getPutSeq = async (): Promise<number> => {
    try {
      const r = await fetch(
        `/api/office/session/size?key=${encodeURIComponent(session.sessionKey)}`,
        { credentials: "same-origin" },
      );
      if (r.ok) {
        const j = await r.json();
        return j.put_seq ?? 0;
      }
    } catch {
      /* ignore — best-effort */
    }
    return -1;
  };

  useImperativeHandle(ref, () => ({
    // Resolves once the user's edits are DURABLE in trilli-serve's working blob,
    // or REJECTS if that can't be confirmed — callers must not export the blob
    // unless this resolves (otherwise they save stale/blank bytes).
    save: () =>
      new Promise<void>(async (resolve, reject) => {
        const preSeq = await getPutSeq();

        // No edits since our last successful save → the working blob is already
        // durable; resolve immediately. dirtySinceLastSaveRef is monotonic, so
        // (unlike the engine's instantaneous modified flag) it is never falsely
        // clear while a WOPI upload is still pending.
        if (!dirtySinceLastSaveRef.current) { setState("saved"); resolve(); return; }

        saveRespSeenRef.current = false;
        post("Action_Save", { Notify: true, DontTerminateEdit: true, DontSaveIfUnmodified: false });
        setState("saving");

        // WAIT until the engine's PutFile actually lands — put_seq advancing past
        // preSeq is the ONLY proof the edits are durable, and the host reads the
        // blob the instant this resolves. So we do NOT resolve on Action_Save_Resp
        // (the engine acks BEFORE its upload reaches trilli-serve). With the
        // engine's idle-save at ~2s, a pending flush lands well within the grace
        // below; the grace only fires when the edits were ALREADY flushed (nothing
        // new to upload). On a real timeout we REJECT (fail closed) so the caller
        // refuses to overwrite the file with stale content.
        const start = Date.now();
        const hardDeadline = start + 30000;
        const poll = async () => {
          const seq = await getPutSeq();
          if (seq > preSeq) {                                       // durable flush landed
            dirtySinceLastSaveRef.current = false;
            dirtyAtSeqRef.current = -1;
            setState("saved");
            resolve();
            return;
          }
          // The edits we're trying to save were ALREADY flushed by the engine's
          // idle-save before this click (put_seq advanced past where it was when
          // the edit happened) — so they're durable even though THIS Action_Save
          // produced no new PutFile. This is the Calc deadlock case: resolve.
          if (dirtyAtSeqRef.current >= 0 && seq > dirtyAtSeqRef.current) {
            dirtySinceLastSaveRef.current = false;
            dirtyAtSeqRef.current = -1;
            setState("saved");
            resolve();
            return;
          }
          if (saveRespSeenRef.current && Date.now() - start > 4000) {
            // Engine acked + no new PutFile within the grace → already flushed.
            dirtySinceLastSaveRef.current = false;
            dirtyAtSeqRef.current = -1;
            setState("saved");
            resolve();
            return;
          }
          if (Date.now() > hardDeadline) {
            setState("modified");
            reject(new Error("save-timeout"));
            return;
          }
          window.setTimeout(poll, 400);
        };
        window.setTimeout(poll, 400);
      }),
    exec: (command: string, args?: Record<string, unknown>) => {
      post("Send_UNO_Command", args ? { Command: command, Args: args } : { Command: command });
    },
    // The engine can't open a file picker from a received postMessage (no user
    // gesture in the iframe), so the HOST picks the file here (we have the
    // gesture). The engine's Action_InsertGraphic takes a URL — but its
    // insertfile fetches that URL server-side from inside the kit JAIL, which
    // can't reach our asset endpoint. And we can't fire the engine's
    // 'insertgraphic' event directly (cross-origin iframe). So we send a
    // custom Trilli_InsertGraphicData postMessage with the image as base64; a
    // bundle patch builds a File from it and fires 'insertgraphic' — the SAME
    // _sendFile path drag-and-drop uses (upload to WOPI host, insert by name),
    // which works because the upload is server-side.
    insertImage: () => {
      const input = document.createElement("input");
      input.type = "file";
      input.accept = "image/*";
      input.style.position = "fixed";
      input.style.top = "-10000px";
      input.style.left = "-10000px";
      input.style.opacity = "0";
      input.onchange = () => {
        const file = input.files?.[0];
        if (!file) {
          input.remove();
          return;
        }
        onNotice?.("Inserting image…");
        const reader = new FileReader();
        reader.onload = () => {
          // reader.result is an ArrayBuffer; base64-encode it for the postMessage.
          const bytes = new Uint8Array(reader.result as ArrayBuffer);
          let binary = "";
          for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
          const data = btoa(binary);
          post("Trilli_InsertGraphicData", {
            data,
            name: file.name,
            type: file.type || "image/png",
          });
        };
        reader.readAsArrayBuffer(file);
        input.remove();
      };
      document.body.appendChild(input);
      setTimeout(() => input.click(), 0);
    },
    zoomBy: (dir: 1 | -1) => {
      // Track the target level and set it ABSOLUTELY (Trilli_SetZoom) — never
      // relative, so it can't drift from the engine's state. Cache locally for
      // instant next-load, and persist to the user's account (follows them across
      // devices) via the productivity prefs API.
      zoomRef.current = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, zoomRef.current + dir));
      try {
        localStorage.setItem(zoomKeyFor(appKey), String(zoomRef.current));
      } catch {
        /* ignore */
      }
      post("Trilli_SetZoom", { zoom: zoomRef.current });
      api.put("/api/productivity/prefs", { key: "zoom", value: String(zoomRef.current) }).catch(() => {});
    },
    present: () => {
      // The slideshow is a CLIENT action. The engine bundle is patched so the
      // Trilli_Present message runs app.dispatcher.dispatch("fullscreen-
      // presentation") — the same path the engine's own present button uses.
      // BUT the engine's requestFullscreen (fired from our postMessage) lacks a
      // user gesture and is blocked, so the show stays windowed with Trilli chrome
      // visible. This click DOES carry the gesture, so we fullscreen the IFRAME
      // from here (hides all outer chrome), THEN start the show so it fills the
      // fullscreen viewport. If fullscreen is unavailable we still start the show.
      const el = iframeRef.current;
      const start = () => post("Trilli_Present");
      if (el?.requestFullscreen) {
        el.requestFullscreen().then(start).catch(start);
      } else {
        start();
      }
    },
  }));

  // Load (or reload) the editor whenever the session changes — form POST so the
  // token isn't exposed in the iframe URL.
  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) return;
    setFailed(false);
    setState("loading");
    loadedRef.current = false;
    setRevealed(false);
    const form = document.createElement("form");
    form.method = "POST";
    form.action = session.editorUrl;
    form.target = FRAME_NAME;
    const tok = document.createElement("input");
    tok.type = "hidden";
    tok.name = "access_token";
    tok.value = session.accessToken;
    form.appendChild(tok);
    document.body.appendChild(form);
    form.submit();
    document.body.removeChild(form);
    // If the engine never sends anything (tunnel down / blocked), surface a
    // hint — but only if nothing has loaded by then.
    const t = window.setTimeout(() => {
      if (!loadedRef.current) setFailed(true);
    }, 12000);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.editorUrl, session.accessToken]);

  // Presence self-heal: re-request the view list on an interval. Collabora
  // pushes Views_List on join/leave, but a dropped push (network blip, race)
  // would otherwise leave the avatars stale forever; this reconciles it.
  useEffect(() => {
    const id = window.setInterval(() => {
      if (loadedRef.current) post("Get_Views");
    }, 10000);
    return () => window.clearInterval(id);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Collabora postMessage bridge.
  useEffect(() => {
    function onMessage(e: MessageEvent) {
      // Bridge diagnostics — confirmed working (P3-G1); now opt-in via
      // localStorage["trilli.office.debug"]="1" so the console stays clean.
      if (localStorage.getItem("trilli.office.debug") === "1") {
        // eslint-disable-next-line no-console
        console.log("[TrilliEditor] msg", e.origin, e.data);
      }
      if (e.origin !== session.engineOrigin) return;
      let msg: { MessageId?: string; Values?: Record<string, unknown> } | null = null;
      try {
        msg = typeof e.data === "string" ? JSON.parse(e.data) : e.data;
      } catch {
        return;
      }
      if (!msg?.MessageId) return;
      // Any valid message from the engine proves the channel works — mark
      // loaded (cancels the fallback) and drop the "taking a while" hint.
      loadedRef.current = true;
      setFailed(false);
      switch (msg.MessageId) {
        case "App_LoadingStatus":
          if (msg.Values?.Status === "Document_Loaded") {
            // Hide the engine's chrome we don't want; KEEP the ruler (restyled
            // to Trilli via branding.css) — it's the real, accurate, draggable
            // ruler. The notebookbar/toolbar (not hideable here) is removed via
            // engine-side CSS — see staging/collabora/headless.
            post("Hide_Menubar");
            post("Show_Ruler");
            post("Hide_StatusBar");
            // Presence: tell the engine our postMessage side is live and ask
            // for the current view list; it then streams Views_List on every
            // join/leave, powering the collaborator avatars.
            post("Host_PostmessageReady");
            post("Get_Views");
            // Re-ask a few times: the first Get_Views can race the engine's
            // collab init and come back solo even when others are present.
            window.setTimeout(() => post("Get_Views"), 500);
            window.setTimeout(() => post("Get_Views"), 1500);
            window.setTimeout(() => post("Get_Views"), 4000);
            // Hide the Properties sidebar. This postMessage runs only AFTER the
            // doc loads, so on its own it lets the sidebar paint then yank it away
            // (the visible "slides in then vanishes" flash). The real suppression
            // is engine CSS (#sidebar-dock-wrapper display:none in headless.css),
            // which stops it painting at all; this stays as belt-and-suspenders.
            post("Hide_Sidebar");
            // Apply the resolved zoom ABSOLUTELY (Trilli_SetZoom → client setZoom),
            // re-applied a couple of times to win any late engine-init race that
            // was randomly reverting it. zoomRef = the server-resolved level (user
            // pref → tenant default → code default), reconciled on mount.
            post("Trilli_SetZoom", { zoom: zoomRef.current });
            window.setTimeout(() => post("Trilli_SetZoom", { zoom: zoomRef.current }), 350);
            window.setTimeout(() => {
              post("Trilli_SetZoom", { zoom: zoomRef.current });
              setState("ready");
              setRevealed(true);
            }, 850);
          }
          break;
        case "Doc_ModifiedStatus":
          // Latch dirty on ANY modification; it's cleared only when save()
          // confirms a durable flush. Never clear it here — the engine reports
          // Modified:false after its INTERNAL save while the WOPI upload to
          // trilli-serve is still pending, which is exactly the false-clear that
          // made subsequent saves export stale bytes.
          if (msg.Values?.Modified) {
            // On the clean→dirty transition, snapshot put_seq BEFORE the engine's
            // ~2s idle-save flushes this edit. save() compares against it so a
            // manual Save can confirm the edit became durable even when the click
            // lost the race to idle-save and produced no new PutFile (the Calc
            // deadlock). Runs well within 2s, so it records the pre-flush seq.
            if (!dirtySinceLastSaveRef.current) {
              void getPutSeq().then((s) => {
                if (s >= 0) dirtyAtSeqRef.current = s;
              });
            }
            dirtySinceLastSaveRef.current = true;
          }
          setState(msg.Values?.Modified ? "modified" : "saved");
          break;
        case "Action_Save_Resp":
          // Secondary signal for save()'s grace check. We do NOT resolve save()
          // here — the engine acks before its WOPI PutFile reaches trilli-serve,
          // and save() must wait for the durable flush (put_seq advance).
          saveRespSeenRef.current = true;
          setState("saved");
          break;
        case "Views_List":
        case "Get_Views_Resp": {
          const raw = (msg.Values ?? {}) as Record<string, unknown>;
          const list = (Array.isArray(raw.Views) ? raw.Views : Array.isArray(msg.Values) ? msg.Values : []) as import("@/components/productivity/PresenceAvatars").CollabView[];
          onViews?.(list);
          break;
        }
        case "CommandStateChanged": {
          // Engine reports per-command state so the toolbar can reflect the
          // selection. Payload keys vary slightly by version — accept both.
          const v = (msg.Values ?? {}) as Record<string, unknown>;
          const name = (v.commandName ?? v.Command ?? v.unoName) as string | undefined;
          const stateVal = (v.state ?? v.State ?? v.disabled) as unknown;
          if (name) {
            statesRef.current = { ...statesRef.current, [name]: String(stateVal) };
            onCommandState?.(statesRef.current);
          }
          break;
        }
        default:
          break;
      }
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.engineOrigin]);

  // Reconcile the zoom from the server: user pref → tenant default → code default.
  // localStorage gave an instant value at init; this corrects it (and applies it
  // if the doc is already up). Source of truth lives in the DB so it follows the
  // user across devices.
  useEffect(() => {
    // Slides has its own fixed default (70%) + its own cache key, so it does NOT
    // adopt the shared (Docs-oriented) account zoom pref — otherwise a user's
    // saved Docs zoom would override the Slides default. Slides keeps whatever it
    // last had locally (or 70%).
    if (appKey === "slides") return;
    let alive = true;
    api
      .get<{ default_zoom?: number; prefs?: Record<string, string> }>("/api/productivity/settings")
      .then((s) => {
        if (!alive) return;
        const raw = s?.prefs?.zoom ?? (s?.default_zoom != null ? String(s.default_zoom) : "");
        const z = parseInt(raw, 10);
        if (Number.isNaN(z)) return;
        zoomRef.current = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, z));
        try {
          localStorage.setItem(zoomKeyFor(appKey), String(zoomRef.current));
        } catch {
          /* ignore */
        }
        post("Trilli_SetZoom", { zoom: zoomRef.current });
      })
      .catch(() => {
        /* fall back to the localStorage cache / code default */
      });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="relative h-full w-full overflow-hidden bg-[#e6e7f1]">
      <iframe
        ref={iframeRef}
        name={FRAME_NAME}
        title="Trilli Productivity editor"
        className="h-full w-full border-0"
        // fullscreen: Impress "Present" launches the slideshow via the browser
        // fullscreen API, which is blocked unless the iframe is granted it.
        allow="clipboard-read 'https://productivity.trilli.com'; clipboard-write 'https://productivity.trilli.com'; fullscreen"
        allowFullScreen
      />
      {/* Loading mask — covers ONLY the document-canvas area. The real toolbars
          (EditorToolbar / DocsMenuBar / DocsToolbar) render ABOVE this component
          in Editor.tsx and are interactive during load, so the skeleton must NOT
          redraw them (doing so produced a second, offset set of bars). It mirrors
          the LOADED canvas — a ruler strip + a full-height centered page on the
          indigo backdrop — so the reveal doesn't shift the layout. */}
      {!revealed && !failed &&
        (appKey === "sheets" ? (
          // Sheets skeleton — an empty spreadsheet grid (column-header row + row-
          // number gutter + cells with light gridlines), mirroring the loaded Calc
          // canvas so there's no layout jump on reveal.
          <div className="absolute inset-0 z-10 flex flex-col overflow-hidden bg-white">
            {/* formula/name-box strip */}
            <div className="h-7 flex-shrink-0 border-b border-[#e3e5ee] bg-[#f8f9fb]" />
            {/* column header row */}
            <div className="flex h-6 flex-shrink-0 border-b border-[#d7d9e6] bg-[#f1f3f9]">
              <div className="w-10 flex-shrink-0 border-r border-[#d7d9e6]" />
              {Array.from({ length: 14 }).map((_, i) => (
                <div key={i} className="min-w-[72px] flex-1 border-r border-[#e3e5ee]" />
              ))}
            </div>
            {/* rows: gray row-number gutter + cells */}
            <div className="flex-1 overflow-hidden">
              {Array.from({ length: 24 }).map((_, r) => (
                <div key={r} className="flex h-7 border-b border-[#eef0f5]">
                  <div className="w-10 flex-shrink-0 border-r border-[#d7d9e6] bg-[#f1f3f9]" />
                  {Array.from({ length: 14 }).map((_, c) => (
                    <div key={c} className="min-w-[72px] flex-1 border-r border-[#eef0f5]" />
                  ))}
                </div>
              ))}
            </div>
          </div>
        ) : appKey === "slides" ? (
          // Slides skeleton — a centered 16:9 white slide on the indigo backdrop
          // (title + body placeholders), matching the loaded Impress canvas.
          <div className="absolute inset-0 z-10 flex flex-col overflow-hidden bg-[#e6e7f1]">
            <div className="flex flex-1 items-center justify-center overflow-hidden p-8">
              <div className="aspect-[16/9] w-full max-w-[920px] rounded-sm bg-white px-[7%] pt-[5%] shadow-md">
                <Skeleton className="h-7 w-3/5" />
                <Skeleton className="mt-7 h-3.5 w-11/12" />
                <Skeleton className="mt-3 h-3.5 w-10/12" />
                <Skeleton className="mt-3 h-3.5 w-9/12" />
                <Skeleton className="mt-3 h-3.5 w-11/12" />
                <Skeleton className="mt-3 h-3.5 w-7/12" />
              </div>
            </div>
          </div>
        ) : (
          // Docs skeleton — a ruler strip + a full-height centered page on the
          // indigo backdrop, matching the loaded Collabora page.
          <div className="absolute inset-0 z-10 flex flex-col overflow-hidden bg-[#e6e7f1]">
            <div className="h-6 flex-shrink-0 border-b border-[#d7d9e6] bg-[#eef0f7]" />
            <div className="flex flex-1 justify-center overflow-hidden pt-4">
              <div className="w-full max-w-[850px] bg-white px-[9%] pt-14 shadow-sm">
                <Skeleton className="h-5 w-2/5" />
                <Skeleton className="mt-6 h-3 w-full" />
                <Skeleton className="mt-2.5 h-3 w-11/12" />
                <Skeleton className="mt-2.5 h-3 w-10/12" />
                <Skeleton className="mt-5 h-3 w-full" />
                <Skeleton className="mt-2.5 h-3 w-9/12" />
                <Skeleton className="mt-2.5 h-3 w-11/12" />
                <Skeleton className="mt-6 h-3 w-full" />
                <Skeleton className="mt-2.5 h-3 w-10/12" />
                <Skeleton className="mt-5 h-3 w-8/12" />
              </div>
            </div>
          </div>
        ))}
      {failed && (
        <div className="pointer-events-none absolute inset-x-0 top-3 mx-auto w-fit rounded-md bg-amber-500/15 px-3 py-1.5 text-[12px] text-amber-700 shadow-sm">
          Editor is taking a while to load — check your connection and try again.
        </div>
      )}
    </div>
  );
});
