import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { api, ApiError, type Identity } from "@/lib/api";
import { probeAndReportIPs, resetIPProbe } from "@/lib/ipProbe";
import { flushOfficeBeforeTeardown } from "@/lib/productivity/sessionFlush";

interface AuthState {
  identity: Identity | null;
  loading: boolean;
  // refresh re-fetches the identity. Pass { silent: true } for an in-place
  // refresh AFTER the app has booted (e.g. post plan-change): it updates the
  // identity without toggling the global `loading` flag, which ProtectedRoute
  // uses to swap in a full-screen loader — that swap unmounts the current page
  // and would wipe its local state (e.g. a confirmation modal set right after).
  refresh: (opts?: { silent?: boolean }) => Promise<void>;
  logout: () => Promise<void>;
  setIdentity: (id: Identity | null) => void;
  // switchTenant re-binds the session to another account the user belongs to
  // and updates the identity in place. Clears the per-tenant workspace memory
  // so the new account opens at its own default workspace.
  switchTenant: (tenantId: number) => Promise<void>;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async (opts?: { silent?: boolean }) => {
    const silent = opts?.silent === true;
    if (!silent) setLoading(true);
    try {
      const id = await api.get<Identity>("/api/me");
      setIdentity(id);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setIdentity(null);
      } else {
        console.error("auth refresh failed", err);
        setIdentity(null);
      }
    } finally {
      if (!silent) setLoading(false);
    }
  }, []);

  const logout = useCallback(async () => {
    // If a Productivity editor is open, flush its pending edits to the working
    // blob (and let any in-flight Save complete) BEFORE we tear down the office
    // sessions below — otherwise a late WOPI PutFile races the deletion and the
    // last edits are lost to a 401. Bounded so logout never hangs.
    await flushOfficeBeforeTeardown();
    // End all Productivity office sessions server-side BEFORE the session cookie
    // is revoked — wipes the scratch working blobs so the next login starts
    // fresh, not resuming a stale doc the user abandoned without saving.
    try {
      await api.delete("/api/office/sessions");
    } catch {
      /* best-effort — the janitor will reap expired sessions anyway */
    }
    try {
      await api.post("/api/auth/logout");
    } catch (err) {
      console.error("logout failed", err);
    }
    resetIPProbe(); // next login re-probes for the new session
    // Drop any in-progress Productivity document state so the next login starts
    // from a fresh "New document" instead of the previous session's file.
    Object.keys(sessionStorage)
      .filter((k) => k.startsWith("trilli.office.doc."))
      .forEach((k) => sessionStorage.removeItem(k));
    setIdentity(null);
  }, []);

  const switchTenant = useCallback(async (tenantId: number) => {
    const id = await api.post<Identity>("/api/auth/switch-tenant", { tenant_id: tenantId });
    // The remembered Files workspace belongs to the PREVIOUS account — carrying
    // it across would point Files at a workspace the new account doesn't have.
    try {
      window.localStorage.removeItem("trilli.lastWorkspaceId");
    } catch {
      /* storage unavailable */
    }
    // Workspaces are per-tenant — drop the remembered one so the new account
    // resolves its own default (Files.tsx reads this key).
    try {
      window.localStorage.removeItem("trilli.lastWorkspaceId");
    } catch {
      /* storage disabled — non-fatal */
    }
    setIdentity(id);
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Once authenticated, capture both client IPs (v4 + v6) for the session.
  useEffect(() => {
    if (identity) void probeAndReportIPs();
  }, [identity]);

  return (
    <AuthContext.Provider value={{ identity, loading, refresh, logout, setIdentity, switchTenant }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
