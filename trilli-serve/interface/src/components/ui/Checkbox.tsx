import { forwardRef, type InputHTMLAttributes } from "react";

import { checkboxCls, checkboxClsLg, cn } from "@/lib/utils";

// A checkbox whose CLICKABLE area is larger than its visual box, so it's easier
// to tick (you don't have to aim precisely at the small square). The visual size
// is unchanged — a transparent padded <label> around the input extends the hit
// target, and an equal negative margin cancels the layout shift so nothing moves.
//
// Drop-in replacement for `<input type="checkbox" className={checkboxCls(Lg)} … />`.
// Forwards refs to the input (e.g. for `indeterminate`).
type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, "size" | "type"> & { size?: "md" | "lg" };

export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox(
  { size = "md", className, ...rest },
  ref,
) {
  return (
    <label className="-m-2 inline-flex cursor-pointer items-center justify-center p-2 align-middle">
      <input
        ref={ref}
        type="checkbox"
        className={cn(size === "lg" ? checkboxClsLg : checkboxCls, className)}
        {...rest}
      />
    </label>
  );
});
