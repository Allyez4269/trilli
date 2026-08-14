// Visibility / rollout gates for Trilli Productivity.
//
// isProductivityAllowed — whether the Productivity SECTION is visible at all.
//   Trilli Docs is GA, so this is true for everyone.
//
// isAppEnabled — the per-app rollout gate, and the SINGLE source of truth for
//   which apps a given user can open. Every call site (editor route guard,
//   sidebar nav tree, launcher tiles) asks this, so a rollout is one edit here:
//     • Trilli Docs   — GA (everyone).
//     • Trilli Sheets — LIMITED rollout: allowlisted accounts only while we
//       build it out. Release to everyone by flipping SHEETS_GA to true (the
//       allowlist then no longer matters).
//     • Trilli Slides — pre-release (no one yet).
//
// NOTE: this is a UI/visibility gate. The /api/office endpoints are session-
// gated (requireAuth) but not per-app email-restricted, so it controls what the
// app surfaces, not a hard server boundary. Graduate to a server-side flag if a
// strict boundary is ever needed.
import type { ProductivityAppKey } from "./apps";

// Trilli Sheets is now GA — available to all users. (Set false to return it to
// the allowlist-only limited rollout below.)
const SHEETS_GA = true;

// Early-access accounts used while Sheets was in limited rollout. Moot now that
// SHEETS_GA is true; kept so toggling SHEETS_GA back restores the allowlist.
const SHEETS_ALLOWLIST: ReadonlySet<string> = new Set(["mike@prices.com"]);

// Trilli Slides is now GA — available to all users. (Set false to return it to
// the allowlist-only limited rollout below.)
const SLIDES_GA = true;
// Early-access accounts used while Slides was in limited rollout. Moot now that
// SLIDES_GA is true; kept so toggling SLIDES_GA back restores the allowlist.
const SLIDES_ALLOWLIST: ReadonlySet<string> = new Set(["mike@prices.com"]);

// Trilli Sign is in PRIVATE BETA while we build it out: allowlisted accounts
// only. Everyone else keeps the "coming soon" presentation — the sidebar badge,
// the stub page, and the (404-gated) APIs all key off this. Launch = flip
// SIGN_GA to true.
const SIGN_GA = true;
const SIGN_ALLOWLIST: ReadonlySet<string> = new Set(["mike@prices.com"]);

// canUseSign reports whether `email` may see and use Trilli Sign (beta).
export function canUseSign(email: string | null | undefined): boolean {
  return SIGN_GA || SIGN_ALLOWLIST.has(normalizeEmail(email));
}

function normalizeEmail(email: string | null | undefined): string {
  return (email ?? "").trim().toLowerCase();
}

export function isProductivityAllowed(_email: string | null | undefined): boolean {
  return true; // Trilli Docs is open to all users
}

// isAppEnabled reports whether `email` may open the given Productivity app.
export function isAppEnabled(
  appKey: ProductivityAppKey,
  email: string | null | undefined,
): boolean {
  switch (appKey) {
    case "docs":
      return true; // GA
    case "sheets":
      return SHEETS_GA || SHEETS_ALLOWLIST.has(normalizeEmail(email));
    case "slides":
      return SLIDES_GA || SLIDES_ALLOWLIST.has(normalizeEmail(email));
    default:
      return false;
  }
}
