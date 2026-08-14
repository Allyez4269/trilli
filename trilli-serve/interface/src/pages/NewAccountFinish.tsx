import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { Loader2, PartyPopper } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { useAuth } from "@/contexts/AuthContext";
import { firstNameOf } from "@/lib/user-name";
import { TrilliLogo } from "@/components/Logo";
import { Button } from "@/components/ui/button";

// NewAccountFinish is the final step of an authenticated second-account
// purchase (payment already taken). The user names their new account; on submit
// the server provisions a new tenant for their existing identity and switches
// their session into it. Then we refresh identity and drop them into it.
export default function NewAccountFinish() {
  const { token = "" } = useParams();
  const { identity, loading, refresh } = useAuth();
  const navigate = useNavigate();
  const first = firstNameOf(identity?.user);
  const [name, setName] = useState(first ? `${first}'s Trilli` : "My Trilli");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (loading) {
    return (
      <div className="flex signup-fullscreen items-center justify-center bg-muted/30">
        <Loader2 className="h-8 w-8 animate-spin text-primary" />
      </div>
    );
  }
  if (!identity) return <Navigate to="/login" replace />;

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await api.post("/api/account/new/complete", {
        token,
        business_name: name.trim(),
      });
      // The server switched our session into the new account — refresh identity
      // so the app (and the account switcher) reflect it, then go home.
      await refresh({ silent: true });
      navigate("/home", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message || "Couldn't finish setup." : "Network error.");
      setBusy(false);
    }
  };

  return (
    <div className="flex signup-fullscreen items-center justify-center bg-muted/30 px-4">
      <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 shadow-sm sm:p-10">
        <TrilliLogo className="mb-6 h-8 text-primary" />
        <div className="mb-2 inline-flex items-center gap-2 rounded-full bg-emerald-500/10 px-3 py-1 text-[12px] font-semibold text-emerald-700">
          <PartyPopper className="h-3.5 w-3.5" /> Payment received
        </div>
        <h1 className="text-2xl font-bold text-foreground">Name your new account</h1>
        <p className="mt-1.5 text-[14px] text-muted-foreground">
          This is your own account — separate storage, billing, and members. You can rename it later.
        </p>

        <label className="mt-6 block text-[13px] font-medium text-foreground">Account name</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && void submit()}
          autoFocus
          className="mt-1.5 h-11 w-full rounded-lg border border-border bg-background px-3 text-[15px] outline-none focus:border-primary/50"
          placeholder="My Trilli"
        />

        {error && <p className="mt-3 text-[13px] text-destructive">{error}</p>}

        <Button onClick={() => void submit()} disabled={busy || !name.trim()} className="mt-6 h-11 w-full text-[14px] font-semibold">
          {busy ? <><Loader2 className="mr-2 h-4 w-4 animate-spin" /> Setting up…</> : "Open my new account"}
        </Button>
      </div>
    </div>
  );
}
