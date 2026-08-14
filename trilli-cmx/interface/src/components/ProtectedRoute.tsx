import { type ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { Loader2 } from "lucide-react";

import { useAuth } from "@/contexts/AuthContext";
import AppShell from "@/components/AppShell";

// Gates the authenticated app: shows a loader while the session is resolving,
// redirects to /login when unauthenticated, otherwise renders inside the shell.
export default function ProtectedRoute({ children }: { children: ReactNode }) {
  const { operator, loading } = useAuth();

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!operator) {
    return <Navigate to="/login" replace />;
  }
  return <AppShell>{children}</AppShell>;
}
