import { api } from "@/lib/api";

// A single connection only reveals one address (v4 or v6). To capture BOTH, the
// browser hits protocol-specific echo hosts — api4.ipify.org resolves only over
// IPv4, api6.ipify.org only over IPv6 — then reports the pair to the server,
// which records both on the current session and resolves geo from IPv4 (else
// IPv6). Best-effort and runs at most once per browser session.

const DONE_KEY = "trilli.ipprobe.done";

async function fetchIP(url: string): Promise<string> {
  try {
    const res = await fetch(url, { cache: "no-store", signal: AbortSignal.timeout(6000) });
    if (!res.ok) return "";
    const j = (await res.json()) as { ip?: string };
    return typeof j.ip === "string" ? j.ip : "";
  } catch {
    // A missing protocol (e.g. no IPv6 connectivity) just fails this fetch.
    return "";
  }
}

/** Probe both client IPs and report them to the current session. */
export async function probeAndReportIPs(): Promise<void> {
  try {
    if (sessionStorage.getItem(DONE_KEY)) return;
  } catch {
    /* sessionStorage unavailable — just proceed */
  }
  const [ipv4, ipv6] = await Promise.all([
    fetchIP("https://api4.ipify.org?format=json"),
    fetchIP("https://api6.ipify.org?format=json"),
  ]);
  if (!ipv4 && !ipv6) return;
  try {
    await api.post("/api/me/session/ips", { ipv4, ipv6 });
    try {
      sessionStorage.setItem(DONE_KEY, "1");
    } catch {
      /* ignore */
    }
  } catch {
    /* best-effort — never disrupt the app */
  }
}

/** Clear the once-per-session guard (called on logout so the next login re-probes). */
export function resetIPProbe(): void {
  try {
    sessionStorage.removeItem(DONE_KEY);
  } catch {
    /* ignore */
  }
}
