// Client for the Cloud Import (Google Drive) backend at /api/integrations/google/*.
export type ConnectResult = { status: "ok" | "error"; email?: string; error?: string };

export type PickerToken = { access_token: string; api_key: string; app_id: string; client_id: string };

export async function getStatus(): Promise<{ connected: boolean; email?: string }> {
  try {
    const r = await fetch("/api/integrations/google/status", { credentials: "include" });
    if (!r.ok) return { connected: false };
    return await r.json();
  } catch {
    return { connected: false };
  }
}

export async function getPickerToken(): Promise<{ token?: PickerToken; error?: string }> {
  const r = await fetch("/api/integrations/google/picker-token", { credentials: "include" });
  if (!r.ok) {
    const body = await r.json().catch(() => ({}));
    return { error: body.error || "picker_unavailable" };
  }
  return { token: await r.json() };
}

export async function importFiles(
  fileIds: string[],
  folderId: number | null,
  workspaceId: number | null,
): Promise<{ imported?: number; failed?: { id: string; name?: string; error: string }[]; error?: string }> {
  const r = await fetch("/api/integrations/google/import", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ file_ids: fileIds, folder_id: folderId, workspace_id: workspaceId }),
  });
  return r.json();
}

export async function disconnectGoogle(): Promise<void> {
  try {
    await fetch("/api/integrations/google/disconnect", { method: "POST", credentials: "include" });
  } catch {
    /* ignore */
  }
}

// connectGoogle opens the OAuth consent popup and resolves when the callback page
// reports a result — via postMessage (fast path) or app-origin localStorage
// (reliable fallback, since a Cross-Origin-Opener-Policy on Google's pages can
// sever window.opener).
export function connectGoogle(): Promise<ConnectResult> {
  return new Promise((resolve) => {
    // Sized to comfortably fit Google's two-column consent screen (it's cramped
    // below ~600px wide). Clamped to the available screen on small displays.
    const w = Math.min(640, window.screen.availWidth - 40);
    const h = Math.min(780, window.screen.availHeight - 40);
    const left = window.screenX + Math.max(0, (window.outerWidth - w) / 2);
    const top = window.screenY + Math.max(0, (window.outerHeight - h) / 2);
    const popup = window.open(
      `/api/integrations/google/auth?origin=${encodeURIComponent(window.location.origin)}`,
      "trilli-clouddrive",
      `width=${w},height=${h},left=${left},top=${top}`,
    );

    const KEY = "trilli_clouddrive_result";
    try {
      localStorage.removeItem(KEY);
    } catch {
      /* ignore */
    }

    let done = false;
    const finish = (r: ConnectResult) => {
      if (done) return;
      done = true;
      window.removeEventListener("message", onMsg);
      window.clearInterval(poll);
      resolve(r);
    };
    const onMsg = (e: MessageEvent) => {
      if (e.origin === window.location.origin && e.data?.source === "trilli-clouddrive") {
        finish(e.data as ConnectResult);
      }
    };
    const poll = window.setInterval(() => {
      try {
        const raw = localStorage.getItem(KEY);
        if (raw) {
          localStorage.removeItem(KEY);
          finish(JSON.parse(raw) as ConnectResult);
          return;
        }
      } catch {
        /* ignore */
      }
      if (popup && popup.closed) finish({ status: "error", error: "The sign-in window was closed." });
    }, 400);
    window.addEventListener("message", onMsg);
  });
}
