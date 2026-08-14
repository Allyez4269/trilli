import { useMemo } from "react";
import { cn } from "@/lib/utils";
import { scorePassword } from "@/lib/passwordStrength";

// Segment fill + label color per score (1–4). Index 0 is unused (empty/too short).
const BAR = ["bg-foreground/15", "bg-red-500", "bg-amber-500", "bg-lime-500", "bg-emerald-500"];
const TEXT = ["text-muted-foreground", "text-red-600", "text-amber-600", "text-lime-600", "text-emerald-600"];

interface PasswordStrengthProps {
  password: string;
  className?: string;
}

/**
 * A live, segmented password-strength meter. Four bars fill and shift color
 * (red → amber → lime → emerald) as the password gets stronger, with a label
 * and a single actionable hint. Renders nothing for an empty field.
 */
export default function PasswordStrength({ password, className }: PasswordStrengthProps) {
  const { score, label, suggestion } = useMemo(() => scorePassword(password), [password]);
  if (!password) return null;

  // "Too short" reads as score 0 but we still tint the first segment red so the
  // bar reacts immediately as the user types.
  const fillLevel = score === 0 ? 1 : score;
  const tone = score === 0 ? 1 : score;

  return (
    <div className={cn("space-y-1", className)}>
      <div className="flex gap-1" aria-hidden="true">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className={cn(
              "h-1.5 flex-1 rounded-full transition-colors duration-300",
              i <= fillLevel ? BAR[tone] : "bg-foreground/10",
            )}
          />
        ))}
      </div>
      <div className="flex items-center justify-between gap-3">
        <span className={cn("text-[11px] font-medium", TEXT[tone])}>{label}</span>
        {suggestion && (
          <span className="truncate text-right text-[11px] text-muted-foreground">{suggestion}</span>
        )}
      </div>
    </div>
  );
}
