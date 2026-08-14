// A teardown hook so the open Productivity editor can flush its pending edits to
// the working blob BEFORE AuthContext deletes office sessions on logout. Without
// it, logout can yank the session/working blob while the engine still has a
// pending WOPI PutFile (or an in-flight explicit Save), losing those last edits
// to a 401. The editor registers a flush fn on mount; logout awaits it (bounded)
// before the teardown DELETE.
let flushFn: (() => Promise<void>) | null = null;

export function registerOfficeFlush(fn: (() => Promise<void>) | null): void {
  flushFn = fn;
}

// Run the registered flush, bounded by timeoutMs so logout never hangs. Safe to
// call when no editor is mounted (no-op). Best-effort: errors are swallowed.
export async function flushOfficeBeforeTeardown(timeoutMs = 6000): Promise<void> {
  const fn = flushFn;
  if (!fn) return;
  try {
    await Promise.race([
      fn().catch(() => {}),
      new Promise<void>((resolve) => setTimeout(resolve, timeoutMs)),
    ]);
  } catch {
    /* best-effort */
  }
}
