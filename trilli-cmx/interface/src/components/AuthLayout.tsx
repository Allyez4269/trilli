import { type ReactNode } from "react";
import { ChevronLeft } from "lucide-react";
import { TrilliLogo } from "@/components/Logo";

// AuthLayout mirrors app.trilli.com/login (SPEC §5.3): a two-column shell — the
// form in an elevated card on the left, a branded promo panel on the right —
// recolored to the CMX brand (slate primary / navy chrome) and themed for the
// operator console rather than the consumer app.
//
// onBack, when provided, renders a single consistent "Back" control at the top
// of the card to return to the start of a multi-step flow (2FA, enrollment).
export default function AuthLayout({ children, onBack }: { children: ReactNode; onBack?: () => void }) {
  const year = new Date().getFullYear();
  return (
    <div className="flex min-h-screen bg-background">
      {/* Left — form */}
      <div className="flex w-full flex-col px-6 py-8 sm:px-10 lg:w-[44%] lg:px-16 xl:px-24">
        <div className="flex flex-1 flex-col justify-center py-10">
          <div className="mx-auto w-full max-w-[26rem]">
            <div className="rounded-2xl border border-border bg-card p-8 shadow-lg shadow-primary/5 sm:p-10">
              <TrilliLogo className="mb-7 h-8 text-foreground" />
              {onBack && (
                <button
                  type="button"
                  onClick={onBack}
                  className="group mb-5 -ml-2 inline-flex items-center gap-1 rounded-md px-2 py-1 text-[13px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                >
                  <ChevronLeft className="h-4 w-4 transition-transform group-hover:-translate-x-0.5" />
                  Back
                </button>
              )}
              {children}
            </div>
          </div>
        </div>
        {/* Subtle footer notice — dynamic year. */}
        <p className="text-center text-[13px] text-muted-foreground">
          © {year} Trilli Media LLC. All rights reserved.
        </p>
      </div>

      {/* Right — promotional / context panel (CMX brand gradient) */}
      <div className="relative hidden overflow-hidden lg:flex lg:w-[56%]">
        <div className="absolute inset-0 bg-gradient-to-br from-chrome to-primary" />
        {/* Soft decorative orbs for depth. */}
        <div className="absolute -right-28 -top-28 h-[28rem] w-[28rem] rounded-full bg-white/[0.06]" />
        <div className="absolute -bottom-32 -left-24 h-[26rem] w-[26rem] rounded-full bg-white/[0.05]" />
        <div className="absolute right-1/4 top-1/3 h-40 w-40 rounded-full bg-white/[0.04]" />

        <div className="relative z-10 flex w-full flex-col justify-center px-16 py-16 text-primary-foreground xl:px-24">
          <p className="text-[11px] font-semibold uppercase tracking-[0.22em] text-primary-foreground/60">
            Trilli CMX
          </p>
          <h2 className="mt-4 max-w-sm text-[2.25rem] font-semibold leading-[1.15] tracking-tight xl:text-[2.5rem]">
            The operator
            <br />
            console.
          </h2>
          <p className="mt-5 max-w-xs text-[13px] leading-relaxed text-primary-foreground/60">
            Staff-only access. Every action is audited and requires two-factor authentication.
          </p>
        </div>
      </div>
    </div>
  );
}
