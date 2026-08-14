import { type ReactNode } from "react";
import { ChevronLeft, FolderTree, Lock, ShieldCheck } from "lucide-react";
import { TrilliLogo } from "@/components/Logo";

// AuthLayout is the shared two-column shell for the public auth pages (login,
// password reset). Left: the form, enclosed in an elevated card. Right (lg+): a
// branded promo panel.
//
// onBack, when provided, renders a single consistent "Back" control at the top
// of the card — the one clean way to return to the start of the flow from any
// sub-step (a chosen provider, the password step, 2FA, or forgot-password).
export default function AuthLayout({ children, onBack }: { children: ReactNode; onBack?: () => void }) {
  return (
    <div className="flex min-h-screen bg-background">
      {/* Left — form */}
      <div className="flex w-full flex-col px-6 py-8 sm:px-10 lg:w-[44%] lg:px-16 xl:px-24">
        <div className="flex flex-1 flex-col justify-center py-10">
          <div className="mx-auto w-full max-w-[30.8rem]">
            {/* The form lives inside a soft, elevated card so it reads as a
                distinct, focused surface against the canvas. */}
            <div className="rounded-2xl border border-border bg-card p-8 shadow-lg shadow-primary/5 sm:p-10">
              <TrilliLogo className="mb-7 h-9 text-foreground" />
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
        {/* One line, centered on the same axis as the card — so it spreads
            evenly, stretching symmetrically past the card edges rather than
            wrapping. whitespace-nowrap keeps it from ever breaking to two lines. */}
        <p className="-translate-x-[20px] whitespace-nowrap text-center text-[13.75px] text-muted-foreground">
          © {new Date().getFullYear()} Trilli Media LLC, all rights reserved.
          <span className="px-1.5 text-border">|</span>
          File storage and collaboration for modern teams.
        </p>
      </div>

      {/* Right — promotional panel */}
      <div className="relative hidden overflow-hidden lg:flex lg:w-[56%]">
        <div className="absolute inset-0 bg-gradient-to-br from-primary to-[oklch(0.30_0.13_278)]" />
        {/* Soft decorative orbs for subtle depth without clutter. */}
        <div className="absolute -right-28 -top-28 h-[28rem] w-[28rem] rounded-full bg-white/[0.06]" />
        <div className="absolute -bottom-32 -left-24 h-[26rem] w-[26rem] rounded-full bg-white/[0.05]" />
        <div className="absolute right-1/4 top-1/3 h-40 w-40 rounded-full bg-white/[0.04]" />

        <div className="relative z-10 flex w-full flex-col justify-center px-16 py-16 text-primary-foreground xl:px-24">
          <p className="text-[13px] font-medium uppercase tracking-[0.2em] text-primary-foreground/70">
            Trilli Workspace
          </p>
          <h2 className="mt-5 max-w-xl text-4xl font-semibold leading-[1.12] xl:text-[2.75rem]">
            Your files. Encrypted,
            <br />
            private, and yours.
          </h2>
          <p className="mt-5 max-w-md text-[15px] leading-relaxed text-primary-foreground/80">
            Every file is sealed with keys Trilli holds — the cloud underneath
            stores only ciphertext. Organized by workspace, locked down by
            default, always a click away.
          </p>

          <ul className="mt-12 space-y-6 max-w-md">
            <Feature
              icon={<Lock className="h-[18px] w-[18px]" />}
              title="Encrypted before the cloud"
              body="AES-256 with per-customer keys — never mined, profiled, or fed to AI."
            />
            <Feature
              icon={<ShieldCheck className="h-[18px] w-[18px]" />}
              title="Secure by default"
              body="Passkeys, two-factor, trusted devices, and an immutable audit log."
            />
            <Feature
              icon={<FolderTree className="h-[18px] w-[18px]" />}
              title="Access you control"
              body="Grant a whole workspace or a single folder, per person."
            />
          </ul>
        </div>
      </div>
    </div>
  );
}

function Feature({ icon, title, body }: { icon: ReactNode; title: string; body: string }) {
  return (
    <li className="flex items-start gap-4">
      <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-white/15 ring-1 ring-white/20">
        {icon}
      </span>
      <div>
        <p className="text-[15px] font-semibold">{title}</p>
        <p className="mt-0.5 text-[13px] leading-relaxed text-primary-foreground/75">{body}</p>
      </div>
    </li>
  );
}
