import { useRef, type ClipboardEvent, type KeyboardEvent } from "react";
import { cn } from "@/lib/utils";

interface OtpInputProps {
  value: string;
  onChange: (next: string) => void;
  length?: number;
  autoFocus?: boolean;
  disabled?: boolean;
  /** Fired when the last box is filled and the value reaches `length` digits. */
  onComplete?: (value: string) => void;
}

/**
 * A row of individual single-digit input boxes for entering a numeric code
 * (TOTP / 2FA). Supports type-to-advance, backspace-to-retreat, arrow-key
 * navigation, and pasting a full code into any box. Mirrors app.trilli.com.
 */
export default function OtpInput({
  value,
  onChange,
  length = 6,
  autoFocus,
  disabled,
  onComplete,
}: OtpInputProps) {
  const inputs = useRef<Array<HTMLInputElement | null>>([]);
  const digits = value.split("");

  function focusBox(i: number) {
    const el = inputs.current[Math.max(0, Math.min(length - 1, i))];
    el?.focus();
    el?.select();
  }

  function setDigit(i: number, d: string) {
    const arr = value.padEnd(length, " ").split("");
    arr[i] = d;
    const next = arr.join("").replace(/\s/g, "").slice(0, length);
    onChange(next);
    return next;
  }

  function handleChange(i: number, raw: string) {
    const d = raw.replace(/[^0-9]/g, "");
    if (!d) {
      setDigit(i, " ");
      return;
    }
    if (d.length > 1) {
      const next = (value.slice(0, i) + d).replace(/[^0-9]/g, "").slice(0, length);
      onChange(next);
      focusBox(next.length >= length ? length - 1 : next.length);
      if (next.length >= length) onComplete?.(next);
      return;
    }
    const next = setDigit(i, d);
    if (i < length - 1) focusBox(i + 1);
    if (next.length >= length) onComplete?.(next);
  }

  function handleKeyDown(i: number, e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Backspace") {
      if (digits[i]) {
        setDigit(i, " ");
      } else if (i > 0) {
        e.preventDefault();
        setDigit(i - 1, " ");
        focusBox(i - 1);
      }
    } else if (e.key === "ArrowLeft" && i > 0) {
      e.preventDefault();
      focusBox(i - 1);
    } else if (e.key === "ArrowRight" && i < length - 1) {
      e.preventDefault();
      focusBox(i + 1);
    }
  }

  function handlePaste(e: ClipboardEvent<HTMLInputElement>) {
    e.preventDefault();
    const pasted = e.clipboardData.getData("text").replace(/[^0-9]/g, "").slice(0, length);
    if (!pasted) return;
    onChange(pasted);
    focusBox(pasted.length >= length ? length - 1 : pasted.length);
    if (pasted.length >= length) onComplete?.(pasted);
  }

  return (
    <div className="flex items-center justify-center gap-2" onPaste={handlePaste}>
      {Array.from({ length }).map((_, i) => (
        <input
          key={i}
          ref={(el) => {
            inputs.current[i] = el;
          }}
          type="text"
          inputMode="numeric"
          autoComplete={i === 0 ? "one-time-code" : "off"}
          autoFocus={autoFocus && i === 0}
          disabled={disabled}
          maxLength={1}
          value={digits[i] ?? ""}
          onChange={(e) => handleChange(i, e.target.value)}
          onKeyDown={(e) => handleKeyDown(i, e)}
          onFocus={(e) => e.target.select()}
          className={cn(
            "h-12 w-11 rounded-lg border border-foreground/25 bg-background text-center font-mono text-xl",
            "outline-none transition focus:border-primary focus:ring-1 focus:ring-primary/40",
            "disabled:cursor-not-allowed disabled:opacity-50",
            digits[i] && "border-primary/60",
          )}
        />
      ))}
    </div>
  );
}
