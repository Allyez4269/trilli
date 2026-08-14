// Builds the Collabora editor session for the embed component.
//
//
// Phase 2: the WOPISrc + access token come from trilli-serve (POST
// /api/office/session), which mints a per-session working blob in the tenant's
// own encrypted blob store and returns the loopback WOPISrc the engine resolves
// server-side. The browser never contacts the WOPI host directly; the session
// key is the only credential the engine sees.
import type { ProductivityApp } from "./apps";
import { api } from "@/lib/api";

export interface EditorSession {
  editorUrl: string; // engine cool.html?WOPISrc=...&lang=... (NO token in URL)
  accessToken: string; // POSTed into the iframe (form body), not the URL
  wopiSrc: string; // resolved by the engine SERVER-SIDE; browser never fetches it
  engineOrigin: string; // for postMessage target/origin checks
  sessionKey: string; // the trilli-serve office session key (also the access token)
  fileName: string; // current document display name
  sourceFileId?: number; // the Trilli file this session was opened from / saved as (for silent overwrite on resume)
}

// The engine is served from a real subdomain over HTTPS (no localhost, no
// Private-Network-Access prompt). It's a service endpoint the iframe calls, not
// a user-facing site. Overridable for testing via localStorage["trilli.office.engine"].
const DEFAULT_ENGINE_ORIGIN = "https://productivity.trilli.com";
// The editor asset hash from /hosting/discovery. TODO: trilli-serve reads
// discovery server-side and returns editor_url, so this is never hardcoded.
const EDITOR_HASH = "7d478de54b";

export function engineOrigin(): string {
  try {
    return localStorage.getItem("trilli.office.engine") || DEFAULT_ENGINE_ORIGIN;
  } catch {
    return DEFAULT_ENGINE_ORIGIN;
  }
}

// CreateSessionInput mirrors the trilli-serve POST /api/office/session body.
export interface CreateSessionInput {
  app: string;
  fileId?: number | null; // open an existing Trilli file (Edit/Open); omit for a blank New doc
  fileName?: string;
}

// fetchSession mints an edit session on trilli-serve and returns the
// EditorSession the <TrilliEditor> loads. The server owns the WOPISrc (loopback
// to trilli-serve's WOPI listener) + the session key (access token); the client
// only composes the public engine editorUrl from the WOPISrc.
export async function fetchSession(app: ProductivityApp, input: CreateSessionInput = {}): Promise<EditorSession> {
  const res = await api.post<{
    session_key: string;
    wopi_src: string;
    access_token: string;
    file_name?: string;
  }>("/api/office/session", {
    app: app.key,
    file_id: input.fileId ?? null,
    file_name: input.fileName ?? "",
  });
  return buildSession(app, res);
}

// resumeSession resumes the user's ACTIVE session for an app (if one exists),
// so the editor can preserve the auto-saved working blob on refresh instead of
// creating a new blank session. Returns null if no active session exists.
export async function resumeSession(app: ProductivityApp): Promise<EditorSession | null> {
  try {
    const res = await api.get<{
      session_key: string;
      wopi_src: string;
      access_token: string;
      file_name?: string;
      source_file_id?: number;
    }>(`/api/office/session/active?app=${app.key}`);
    const s = buildSession(app, res);
    if (res.source_file_id) s.sourceFileId = res.source_file_id;
    return s;
  } catch {
    return null; // 404 = no active session, caller creates a new one
  }
}

// buildSession composes the EditorSession from a server response (shared by
// fetchSession + resumeSession).
function buildSession(app: ProductivityApp, res: {
  session_key: string;
  wopi_src: string;
  access_token: string;
  file_name?: string;
}): EditorSession {
  const origin = engineOrigin();
  const editorUrl =
    `${origin}/browser/${EDITOR_HASH}/cool.html` +
    `?WOPISrc=${encodeURIComponent(res.wopi_src)}&lang=en-US&closebutton=0`;
  return {
    editorUrl,
    accessToken: res.access_token,
    wopiSrc: res.wopi_src,
    engineOrigin: origin,
    sessionKey: res.session_key,
    // Server is authoritative for the name; this fallback only fires if it's
    // ever empty. Keep the wording app-appropriate.
    fileName:
      res.file_name ??
      (app.key === "sheets" ? "New spreadsheet" : app.key === "slides" ? "New presentation" : "New document"),
  };
}
