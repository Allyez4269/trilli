import { AlertCircle, Inbox } from "lucide-react";

// Shared loading / error / empty placeholders for data views.

export function LoadingBlock({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-16">
      <div className="relative h-7 w-7">
        <div className="absolute inset-0 rounded-full border-2 border-muted" />
        <div className="absolute inset-0 animate-spin rounded-full border-2 border-transparent border-t-primary/50" />
      </div>
      <span className="text-sm text-muted-foreground">{label}</span>
    </div>
  );
}

// SkeletonRows renders N shimmer rows that mimic table row density.
// Use as the loading state inside table cards — preserves perceived layout height.
export function SkeletonRows({ rows = 5 }: { rows?: number }) {
  return (
    <div className="divide-y divide-border/60">
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-4 px-4 py-3"
          style={{ opacity: Math.max(0.25, 1 - i * 0.15) }}
        >
          <div className="skeleton h-3 w-[32%]" />
          <div className="skeleton h-3 w-[16%]" />
          <div className="skeleton h-3 w-[20%]" />
          <div className="skeleton ml-auto h-3 w-[10%]" />
        </div>
      ))}
    </div>
  );
}

export function ErrorBlock({ message }: { message: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-16">
      <div className="flex h-9 w-9 items-center justify-center rounded-full bg-destructive/[0.08]">
        <AlertCircle className="h-4 w-4 text-destructive" />
      </div>
      <div className="text-center">
        <p className="text-sm font-medium text-foreground/70">Something went wrong</p>
        <p className="mt-0.5 max-w-xs text-xs text-muted-foreground">{message}</p>
      </div>
    </div>
  );
}

export function EmptyBlock({
  label,
  description,
}: {
  label: string;
  description?: string;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-16">
      <Inbox className="h-7 w-7 text-muted-foreground/30" />
      <div className="text-center">
        <p className="text-sm text-muted-foreground/70">{label}</p>
        {description && (
          <p className="mt-0.5 text-xs text-muted-foreground/50">{description}</p>
        )}
      </div>
    </div>
  );
}
