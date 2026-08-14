import { Navigate, useLocation } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import { AppShell } from "@/components/AppShell";

function isAdminOrOwner(role: string | undefined | null): boolean {
  const r = (role ?? "").toLowerCase().trim();
  return r === "owner" || r === "admin";
}

interface ProtectedRouteProps {
  children: React.ReactNode;
  // When true, only Owner/Admin can reach this route — Members and
  // Viewers get redirected to /files. Mirrors the sidebar filter and
  // server-side RequireAdminOrOwner gate.
  adminOnly?: boolean;
  // When true, the route requires auth but renders WITHOUT the AppShell —
  // full-bleed experiences (e.g. Trilli Sign's ceremony preview) that must
  // look exactly like their public counterpart.
  bare?: boolean;
}

export function ProtectedRoute({ children, adminOnly, bare }: ProtectedRouteProps) {
  const { identity, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div className="flex h-screen items-center justify-center">
        <div className="text-muted-foreground">Loading…</div>
      </div>
    );
  }
  if (!identity) {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }
  if (adminOnly && !isAdminOrOwner(identity.membership?.role)) {
    return <Navigate to="/files" replace />;
  }
  if (bare) {
    return <>{children}</>;
  }
  return <AppShell>{children}</AppShell>;
}
