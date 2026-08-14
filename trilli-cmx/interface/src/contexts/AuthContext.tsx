import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { api, ApiError, type MeResponse, type Operator } from "@/lib/api";

interface AuthState {
  operator: Operator | null;
  loading: boolean;
  stepUpFresh: boolean;
  refresh: () => Promise<void>;
  setOperator: (op: Operator | null) => void;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [operator, setOperator] = useState<Operator | null>(null);
  const [stepUpFresh, setStepUpFresh] = useState(false);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const me = await api.get<MeResponse>("/api/cmx/me");
      setOperator(me.operator);
      setStepUpFresh(me.step_up_fresh);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setOperator(null);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.post("/api/cmx/logout");
    } catch {
      // ignore — clear locally regardless
    }
    setOperator(null);
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return (
    <AuthContext.Provider value={{ operator, loading, stepUpFresh, refresh, setOperator, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
