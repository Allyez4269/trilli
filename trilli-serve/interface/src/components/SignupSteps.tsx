import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

// SignupSteps is the funnel progress header. `current` is the 1-based active
// step; earlier steps render as completed (check), later steps as upcoming.
// Shown on the checkout (2) and account-creation (3) pages so the user always
// knows what's done and what comes next. When the signup came through a social
// provider (`oauth`), step 1 is the provider authentication rather than an
// email confirmation, so it's relabelled accordingly.
export function SignupSteps({ current, oauth = false }: { current: 1 | 2 | 3; oauth?: boolean }) {
  const STEPS = [oauth ? "User authentication" : "Confirm email", "Payment", "Create account"];
  return (
    <ol className="mx-auto flex w-full max-w-xl items-start">
      {STEPS.map((label, i) => {
        const step = i + 1;
        const done = step < current;
        const active = step === current;
        const isFirst = i === 0;
        const isLast = i === STEPS.length - 1;
        return (
          <li key={label} className="flex flex-1 flex-col items-center">
            <div className="flex w-full items-center">
              <span
                className={cn(
                  "h-[3px] flex-1 rounded-full transition-colors",
                  isFirst ? "opacity-0" : step <= current ? "bg-primary" : "bg-border",
                )}
              />
              <span
                className={cn(
                  "flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-full border-2 text-[15px] font-bold transition-all",
                  done && "border-primary bg-primary text-primary-foreground",
                  active && "border-primary bg-primary/10 text-primary ring-4 ring-primary/15",
                  !done && !active && "border-border bg-card text-muted-foreground",
                )}
              >
                {done ? <Check className="h-5 w-5" strokeWidth={3} /> : step}
              </span>
              <span
                className={cn(
                  "h-[3px] flex-1 rounded-full transition-colors",
                  isLast ? "opacity-0" : step < current ? "bg-primary" : "bg-border",
                )}
              />
            </div>
            <span
              className={cn(
                "mt-2.5 whitespace-nowrap text-[12.5px] font-semibold",
                active ? "text-foreground" : done ? "text-foreground/70" : "text-muted-foreground",
              )}
            >
              {label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}
