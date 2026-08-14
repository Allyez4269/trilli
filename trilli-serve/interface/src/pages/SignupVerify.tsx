import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { Loader2, AlertCircle } from "lucide-react";
import { TrilliLogo } from "@/components/Logo";
import { api, ApiError } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

// SignupVerify is the destination of the confirmation email link. It consumes
// the token (POST, so email-client prefetchers can't burn it) and forwards the
// browser to hosted Stripe Checkout — or to Step 6 if already paid.
export default function SignupVerify() {
  const { token = "" } = useParams();
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return; // StrictMode double-invoke guard
    ran.current = true;
    (async () => {
      try {
        const res = await api.post<{ checkout_url?: string; next?: string }>("/api/signup/verify", {
          token,
        });
        if (res.checkout_url) {
          window.location.assign(res.checkout_url); // leave the SPA for Stripe
          return;
        }
        if (res.next) {
          // Already paid — go finish setup (relative path within the app).
          const path = res.next.replace(/^https?:\/\/[^/]+/, "");
          navigate(path, { replace: true });
          return;
        }
        setError("Something went wrong. Please start again.");
      } catch (err) {
        setError(
          err instanceof ApiError
            ? err.message || "This link is no longer valid."
            : "Network error. Please try again.",
        );
      }
    })();
  }, [token, navigate]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <CardTitle className="flex justify-center">
            <TrilliLogo className="h-[34px] text-brand-purple" />
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col items-center gap-3 py-6 text-center">
          {error ? (
            <>
              <AlertCircle className="h-10 w-10 text-destructive" />
              <p className="text-sm text-muted-foreground">{error}</p>
              <button
                type="button"
                onClick={() => navigate("/signup", { replace: true })}
                className="mt-1 text-sm text-brand-purple hover:underline"
              >
                Start again
              </button>
            </>
          ) : (
            <>
              <Loader2 className="h-10 w-10 animate-spin text-brand-purple" />
              <p className="text-sm text-muted-foreground">Confirming your email and opening secure checkout…</p>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
