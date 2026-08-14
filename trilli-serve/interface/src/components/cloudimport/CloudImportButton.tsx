// "Cloud Import" button — sits beside the + in the Files workspace bar, matching
// its styling. After a deliberate 3s hover it reveals a tip card explaining the
// feature with the three provider logos (only Google Drive in colour). There's no
// native title/alt-text tooltip — the delayed card is the only hover affordance.
import { useEffect, useRef, useState } from "react";
import { DownloadCloud } from "lucide-react";

import { cn } from "@/lib/utils";
import { DropboxLogo, GoogleDriveLogo, OneDriveLogo } from "./providers";

const TIP_DELAY_MS = 3000;

export function CloudImportButton({ onClick }: { onClick: () => void }) {
  const [showTip, setShowTip] = useState(false);
  const timer = useRef<number | null>(null);

  const clearTimer = () => {
    if (timer.current) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
  };
  const onEnter = () => {
    clearTimer();
    timer.current = window.setTimeout(() => setShowTip(true), TIP_DELAY_MS);
  };
  const onLeave = () => {
    clearTimer();
    setShowTip(false);
  };

  useEffect(() => clearTimer, []);

  return (
    <div className="relative" onMouseEnter={onEnter} onMouseLeave={onLeave}>
      <button
        type="button"
        onClick={onClick}
        className="inline-flex h-[34px] cursor-pointer items-center gap-1.5 rounded-md border border-primary/40 bg-primary/5 px-2.5 text-[13px] font-medium text-foreground outline-none transition-colors hover:bg-primary/10 focus:outline-none focus-visible:outline-none focus-visible:ring-0"
      >
        <DownloadCloud className="h-4 w-4" style={{ color: "#eb5a5c" }} />
        Cloud Import
      </button>

      {/* hover tip — only after a 3s hover; no native title tooltip */}
      <div
        className={cn(
          "pointer-events-none absolute left-0 top-[calc(100%+8px)] z-50 w-72 origin-top-left rounded-xl border border-border bg-card p-3.5 shadow-xl transition-all duration-150",
          showTip ? "scale-100 opacity-100" : "scale-95 opacity-0",
        )}
      >
        <p className="text-[13px] font-semibold text-foreground">Import from other clouds</p>
        <p className="mt-0.5 text-[12px] leading-snug text-muted-foreground">
          Bring your files straight into Trilli — connect a provider, pick what you want, and we copy it into your file
          space.
        </p>
        <div className="mt-3 grid grid-cols-3 gap-2 border-t border-border pt-3">
          <TipLogo name="Google Drive" status="Available" active>
            <GoogleDriveLogo className="h-7 w-7" />
          </TipLogo>
          <TipLogo name="OneDrive" status="Soon">
            <OneDriveLogo className="h-7 w-7" />
          </TipLogo>
          <TipLogo name="Dropbox" status="Soon">
            <DropboxLogo className="h-7 w-7" />
          </TipLogo>
        </div>
      </div>
    </div>
  );
}

function TipLogo({
  children,
  name,
  status,
  active = false,
}: {
  children: React.ReactNode;
  name: string;
  status: string;
  active?: boolean;
}) {
  return (
    <div className="flex flex-col items-center gap-1 text-center">
      <div className={active ? "" : "opacity-40 grayscale"}>{children}</div>
      <span className="text-[10.5px] font-medium leading-none text-foreground">{name}</span>
      <span className={active ? "text-[9.5px] font-medium leading-none text-emerald-600" : "text-[9.5px] leading-none text-muted-foreground"}>
        {status}
      </span>
    </div>
  );
}
