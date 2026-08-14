import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";

// Fallback used only if the server hasn't told us the window yet (e.g. the
// brief moment after a fresh login, before the next /api/me). The authoritative
// value is identity.idle_timeout_seconds (server const session.DefaultIdleTimeout),
// and the server enforces the same window independently — this hook is the
// proactive client-side auto-logout so an idle user is signed out + redirected
// without waiting for their next request to 401.
const FALLBACK_IDLE_SECONDS = 30 * 60;

// Any of these count as "the user is here". Passive listeners so we never
// interfere with scrolling/clicking.
const ACTIVITY_EVENTS = ["mousemove", "mousedown", "keydown", "scroll", "touchstart", "click", "wheel"];

// Don't re-arm the timer more than once per second even under a flood of
// mousemove events.
const RESET_THROTTLE_MS = 1000;

/**
 * useInactivityLogout signs the user out after a period with no interaction
 * (clicks, keys, scroll, pointer movement). Any interaction resets the timer.
 * Mount once, inside an authenticated boundary (AppShell).
 */
export function useInactivityLogout() {
  const { identity, logout } = useAuth();
  const navigate = useNavigate();

  const loggedIn = !!identity;
  const idleSeconds =
    identity?.idle_timeout_seconds && identity.idle_timeout_seconds > 0
      ? identity.idle_timeout_seconds
      : FALLBACK_IDLE_SECONDS;

  // Keep the latest callbacks in refs so identity refreshes (which change the
  // `identity` object reference) don't tear down and re-arm the timer — only a
  // real change in logged-in state or the timeout value should.
  const logoutRef = useRef(logout);
  const navigateRef = useRef(navigate);
  logoutRef.current = logout;
  navigateRef.current = navigate;

  useEffect(() => {
    if (!loggedIn) return;

    const idleMs = idleSeconds * 1000;
    let timer: number | undefined;
    let lastReset = Date.now();
    let firedOnce = false;

    const signOut = () => {
      if (firedOnce) return;
      firedOnce = true;
      // Tell the login screen why they're back there (cleared on read).
      try {
        sessionStorage.setItem("auth:session-ended", "idle");
      } catch {
        /* private-mode / storage-disabled — non-fatal */
      }
      void Promise.resolve(logoutRef.current()).finally(() => {
        navigateRef.current("/login", { replace: true });
      });
    };

    const arm = () => {
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(signOut, idleMs);
    };

    const onActivity = () => {
      const now = Date.now();
      if (now - lastReset < RESET_THROTTLE_MS) return;
      lastReset = now;
      arm();
    };

    // Background tabs throttle setTimeout, so the timer may not fire on time
    // while hidden. When the tab becomes visible again, reconcile against the
    // real elapsed time and sign out immediately if the window already passed.
    const onVisibility = () => {
      if (document.visibilityState !== "visible") return;
      if (Date.now() - lastReset >= idleMs) {
        signOut();
      } else {
        arm();
      }
    };

    // Capture phase (not bubble): some interactive elements call
    // stopPropagation() on clicks/keys (e.g. the file-row action cells and
    // menus/dialogs), which would otherwise hide a real interaction from this
    // window-level listener and let an actively-used session time out. Capture
    // runs top-down before any child can stop the event, so every interaction
    // resets the timer.
    ACTIVITY_EVENTS.forEach((e) =>
      window.addEventListener(e, onActivity, { passive: true, capture: true }),
    );
    document.addEventListener("visibilitychange", onVisibility);
    arm();

    return () => {
      if (timer) window.clearTimeout(timer);
      ACTIVITY_EVENTS.forEach((e) =>
        window.removeEventListener(e, onActivity, { capture: true }),
      );
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [loggedIn, idleSeconds]);
}
