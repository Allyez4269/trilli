import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// The single, app-wide checkbox control style. The min/max locks pin the
// native control to exactly 10.5px so it can't render larger on any browser;
// `flex-none align-middle` keeps it from stretching inside flex/table rows.
// Use this everywhere a checkbox appears (table select-all + rows, forms) so
// every checkbox in the app is visually identical.
export const checkboxCls =
  "h-[10.5px] w-[10.5px] min-h-[10.5px] min-w-[10.5px] max-h-[10.5px] max-w-[10.5px] flex-none align-middle rounded-[3px] border-border text-primary outline-none focus:outline-none focus-visible:ring-1 focus-visible:ring-primary/40 disabled:cursor-not-allowed disabled:border-muted-foreground/70 disabled:bg-muted";

// Larger variant (+20%, 10.5px → 12.6px) for the Files and Trash list checkboxes.
export const checkboxClsLg =
  "h-[12.6px] w-[12.6px] min-h-[12.6px] min-w-[12.6px] max-h-[12.6px] max-w-[12.6px] flex-none align-middle rounded-[3px] border-border text-primary outline-none focus:outline-none focus-visible:ring-1 focus-visible:ring-primary/40 disabled:cursor-not-allowed disabled:border-muted-foreground/70 disabled:bg-muted";
