// readFlash reads — and immediately clears — the one-time `trilli_flash` cookie
// that server-side redirects (e.g. the Google OAuth callback) use to surface a
// message. This keeps outcomes out of the URL: no brittle ?error=<code> query
// strings, just a transient cookie shown once and discarded.
export function readFlash(): string | null {
  const m = document.cookie.match(/(?:^|;\s*)trilli_flash=([^;]*)/);
  if (!m) return null;
  // Expire it so it never shows twice (e.g. on refresh).
  document.cookie = "trilli_flash=; Path=/; Max-Age=0; SameSite=Lax";
  try {
    // Go's url.QueryEscape encodes spaces as "+", which decodeURIComponent does
    // not convert — normalize them to %20 first so spaces survive.
    return decodeURIComponent(m[1].replace(/\+/g, "%20"));
  } catch {
    return m[1];
  }
}
