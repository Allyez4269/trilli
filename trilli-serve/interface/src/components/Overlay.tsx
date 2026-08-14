import { useEffect } from "react";
import { createPortal } from "react-dom";

import { cn } from "@/lib/utils";

// Shared modal chrome: dimmed, blurred backdrop with a centered card. Click
// outside and Escape both close. `className` overrides the panel (e.g. width).
// Mirrors the private Overlay in Dialogs.tsx so all app modals look identical.
export function Overlay({
  onClose,
  className,
  children,
}: {
  onClose: () => void;
  className?: string;
  children: React.ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return createPortal(
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        className={cn(
          "w-full max-w-md overflow-hidden rounded-xl border border-border bg-card shadow-2xl animate-in zoom-in-95 fade-in duration-200",
          className,
        )}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}
