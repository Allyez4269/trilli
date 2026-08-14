// Small display formatters shared across CMX pages.

// parseTime parses an API timestamp into a Date. Postgres `to_char(..., '…OF')`
// emits a BARE-HOUR tz offset for whole-hour zones (e.g. "2026-06-16T17:39:08-04"),
// which Date() rejects as invalid ISO 8601 (it requires ±HH:MM). Pad a trailing
// bare-hour offset to ":00" so every CMX timestamp parses.
function parseTime(iso: string): Date {
  return new Date(iso.replace(/(T\d{2}:\d{2}:\d{2})([+-]\d{2})$/, "$1$2:00"));
}

// shortIP truncates a long IPv6 address to its first and last group
// (e.g. "2600:1700:…:1a2b"); IPv4 and short values pass through unchanged.
export function shortIP(ip?: string): string {
  if (!ip) return "—";
  if (ip.includes(":")) {
    const parts = ip.split(":").filter(Boolean);
    if (parts.length > 2) return `${parts[0]}:…:${parts[parts.length - 1]}`;
  }
  return ip;
}

export function formatBytes(n: number): string {
  if (!n || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
  const v = n / Math.pow(1024, i);
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export function formatDate(iso?: string): string {
  if (!iso) return "—";
  const d = parseTime(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

export function formatDateTime(iso?: string): string {
  if (!iso) return "—";
  const d = parseTime(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

export function formatDateTimeSec(iso?: string): string {
  if (!iso) return "—";
  const d = parseTime(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function relativeTime(iso?: string): string {
  if (!iso) return "never";
  const d = parseTime(iso).getTime();
  if (isNaN(d)) return "—";
  const secs = Math.round((Date.now() - d) / 1000);
  const abs = Math.abs(secs);
  const table: [number, string][] = [
    [60, "s"],
    [3600, "m"],
    [86400, "h"],
    [2592000, "d"],
    [31536000, "mo"],
  ];
  if (abs < 60) return "just now";
  for (let i = 1; i < table.length; i++) {
    if (abs < table[i][0]) {
      const unit = table[i - 1][1];
      return `${Math.round(abs / table[i - 1][0])}${unit} ago`;
    }
  }
  return `${Math.round(abs / 31536000)}y ago`;
}

export function formatCents(cents: number, period?: string): string {
  if (!cents) return "—";
  const dollars = (cents / 100).toLocaleString(undefined, { style: "currency", currency: "USD" });
  return period ? `${dollars}/${period === "annual" ? "yr" : "mo"}` : dollars;
}

export function storagePct(used: number, max: number): number | null {
  if (!max || max <= 0) return null;
  return Math.min(100, Math.round((used / max) * 100));
}
