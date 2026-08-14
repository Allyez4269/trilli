// OAuth popup helper for the social sign-in buttons (Google, Microsoft).
//
// continueWithProvider opens the provider's "Choose an account" chooser in a
// centered popup window (over the current page) instead of a full-page
// redirect, then navigates onward when the popup reports back.
//
// The popup runs the whole OAuth round-trip on the APP origin
// (start → provider → callback), so the session cookie is set first-party
// inside the popup — no cross-site cookies even when opened from the marketing
// site. The callback posts {redirect|error} back here via postMessage; we then
// move the top window to the app. If the popup is blocked, we fall back to the
// classic full-page redirect.
//
// appUrl: "" for the app itself (same origin); the app's absolute origin
// (e.g. https://app.trilli.com) when called from a different site.
// Returns a controller to cancel the in-flight popup (close it + stop
// listening), or null if the popup was blocked and we fell back to a full-page
// redirect. onClosed fires if the user closes the popup themselves without
// finishing — so the caller can revert its "connecting…" UI.

type Provider = "google" | "microsoft";

export interface ProviderPopupOpts {
  mode: "signup" | "login" | "reauth";
  plan?: string;
  cycle?: string;
  appUrl?: string;
  onError?: (msg: string) => void;
  onClosed?: () => void;
  // Fired when the provider authenticated successfully but the email has no
  // Trilli account yet — the caller shows a "let's get you started" prompt.
  onNoAccount?: () => void;
  // Fired in "reauth" mode: the provider verified the user's identity for a
  // step-up (no new session). Receives a short-lived token to pass to the
  // gated action instead of navigating.
  onReauth?: (token: string) => void;
}

export function continueWithProvider(
  provider: Provider,
  opts: ProviderPopupOpts,
): { cancel: () => void } | null {
  const appUrl = opts.appUrl ?? "";
  const appOrigin = appUrl ? new URL(appUrl).origin : window.location.origin;
  // The Go callback tags its result payload with this source, per provider.
  const source = `trilli-${provider}`;
  // Same-origin relay key the callback writes the result to (see below).
  const RESULT_KEY = "trilli_oauth_result";

  // Clear any stale result from an earlier attempt before we start.
  try {
    localStorage.removeItem(RESULT_KEY);
  } catch {
    /* storage may be unavailable */
  }

  const params = (extra: Record<string, string>) => {
    const q = new URLSearchParams({ mode: opts.mode, ...extra });
    if (opts.plan) {
      q.set("plan", opts.plan);
      if (opts.cycle) q.set("cycle", opts.cycle);
    }
    return q.toString();
  };

  const startPath = `${appUrl}/api/auth/${provider}/start`;
  const popupUrl = `${startPath}?${params({ popup: "1", origin: window.location.origin })}`;
  const W = 500;
  const H = 640;
  const left = window.screenX + Math.max(0, (window.outerWidth - W) / 2);
  const top = window.screenY + Math.max(0, (window.outerHeight - H) / 2);
  const popup = window.open(popupUrl, `trilli-${provider}`, `width=${W},height=${H},left=${left},top=${top}`);

  // Popup blocked → fall back to the full-page redirect (no popup/origin).
  if (!popup) {
    window.location.href = `${startPath}?${params({})}`;
    return null;
  }

  let done = false;
  let graceTimer = 0;

  type Result = { source?: string; status?: string; redirect?: string; error?: string; token?: string };

  // teardown removes every listener/timer. Called once we're truly finished —
  // a result was handled, the popup was dismissed, or we timed out.
  function teardown() {
    done = true;
    window.removeEventListener("message", onMessage);
    window.removeEventListener("storage", onStorage);
    window.removeEventListener("focus", onFocus);
    window.clearInterval(poll);
    window.clearInterval(dismissPoll);
    window.clearTimeout(timeout);
    window.clearTimeout(graceTimer);
  }

  const finish = (d: Result) => {
    if (done) return;
    teardown();
    if (d.status === "reauth_ok") {
      // Step-up re-auth: don't navigate — hand the token back to the caller.
      opts.onReauth?.(d.token || "");
    } else if (d.status === "ok" && d.redirect) {
      // The redirect may be an absolute URL (e.g. the marketing plans page on a
      // different host) or an app-relative path; only prefix appUrl for the latter.
      window.location.href = /^https?:\/\//i.test(d.redirect) ? d.redirect : `${appUrl}${d.redirect}`;
    } else if (d.status === "no_account") {
      opts.onNoAccount?.();
    } else {
      opts.onError?.(d.error || "Sign-in didn't complete. Please try again.");
    }
  };

  // Channel 1 — postMessage. Works when the opener link survives the round-trip.
  const onMessage = (e: MessageEvent) => {
    if (e.origin !== appOrigin) return;
    const d = e.data as Result;
    if (!d || d.source !== source) return;
    finish(d);
  };

  // Channel 2 — same-origin localStorage relay. This is the reliable path:
  // providers like Microsoft/Google can apply Cross-Origin-Opener-Policy on their
  // pages, which severs window.opener (postMessage then silently no-ops AND the
  // opener sees popup.closed as true). The callback page is same-origin with the
  // app, so it ALSO writes the result here, where we read it regardless of COOP.
  const readResult = () => {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(RESULT_KEY);
    } catch {
      /* ignore */
    }
    if (!raw) return;
    try {
      localStorage.removeItem(RESULT_KEY);
    } catch {
      /* ignore */
    }
    try {
      const d = JSON.parse(raw) as Result;
      if (d && d.source === source) finish(d);
    } catch {
      /* malformed */
    }
  };
  const onStorage = (e: StorageEvent) => {
    if (e.key === RESULT_KEY && e.newValue) readResult();
  };
  // Poll too — storage events can be missed/coalesced, and don't fire in the
  // window that wrote them.
  const poll = window.setInterval(readResult, 400);

  // Dismissal detection. A popup steals focus, so when it closes — including
  // when dismissed WITHOUT authenticating — focus returns to this window. We
  // schedule a revert to the sign-in screen, keeping the result channel alive
  // through a 1s grace so an auth that completes right as focus returns still
  // wins (and so does a result if the user merely clicked back here mid-auth).
  const scheduleRevert = () => {
    // Idempotent: once a grace is pending, don't restart it — the dismiss poll
    // calls this every 500ms, and resetting a 1000ms timer each tick would mean
    // it never fires.
    if (done || graceTimer) return;
    graceTimer = window.setTimeout(() => {
      graceTimer = 0;
      if (done) return;
      teardown();
      opts.onClosed?.();
    }, 1000);
  };
  // Two detectors for robustness: the focus EVENT (occasionally missed), and a
  // poll of popup.closed GATED by document.hasFocus(). The gate is the trick —
  // COOP can make popup.closed read true while the popup is still open, but a
  // still-open popup holds focus, so document.hasFocus() stays false until it's
  // genuinely closed (or the user returns here). Polling the state also catches
  // a close that never fired a focus event.
  const onFocus = () => {
    if (done) return;
    readResult();
    if (!done) scheduleRevert();
  };
  const dismissPoll = window.setInterval(() => {
    if (done) return;
    readResult();
    if (!done && popup.closed && document.hasFocus()) scheduleRevert();
  }, 500);

  // Ultimate fallback if focus never fires (e.g. the popup never took focus).
  const timeout = window.setTimeout(() => {
    if (done) return;
    teardown();
    opts.onClosed?.();
  }, 180000);

  window.addEventListener("message", onMessage);
  window.addEventListener("storage", onStorage);
  window.addEventListener("focus", onFocus);

  return {
    cancel: () => {
      if (done) return;
      teardown();
      try {
        popup.close();
      } catch {
        /* already closed */
      }
    },
  };
}

export function continueWithGoogle(opts: ProviderPopupOpts) {
  return continueWithProvider("google", opts);
}

export function continueWithMicrosoft(opts: ProviderPopupOpts) {
  return continueWithProvider("microsoft", opts);
}
