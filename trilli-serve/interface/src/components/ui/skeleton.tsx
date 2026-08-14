import { cn } from "@/lib/utils";

// Shared loading placeholder — a soft pulsing block. Color matches the
// muted surface so skeletons feel like "future content" rather than
// a separate loading widget.
export function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}
