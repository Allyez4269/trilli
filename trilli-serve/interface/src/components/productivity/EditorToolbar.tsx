// The Trilli-branded toolbar that sits above the embedded editor canvas — the
// "native" chrome that replaces Collabora's. Phase 1: Back, app identity, file
// name, live save state, and Save / Download actions. The export-format picker
// lives in the Save to Trilli modal (and drives Download too). Share/Rename
// come in P3.
import { ArrowLeft, Check, Download, Loader2, Save } from "lucide-react";

import type { ProductivityApp } from "@/lib/productivity/apps";
import type { SaveState } from "@/components/productivity/TrilliEditor";
import { cn } from "@/lib/utils";

function StateBadge({ state }: { state: SaveState }) {
  if (state === "loading")
    return (
      <span className="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" /> Opening…
      </span>
    );
  if (state === "saving")
    return (
      <span className="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" /> Saving…
      </span>
    );
  if (state === "modified")
    return (
      <span className="inline-flex items-center gap-1.5 text-[12px] text-amber-600">
        <span className="h-1.5 w-1.5 rounded-full bg-amber-500" /> Unsaved changes
      </span>
    );
  // ready | saved
  return (
    <span className="inline-flex items-center gap-1.5 text-[12px] text-muted-foreground">
      <Check className="h-3.5 w-3.5 text-emerald-600" /> Saved
    </span>
  );
}

export function EditorToolbar({
  app,
  fileName,
  state,
  format,
  onSave,
  onDownload,
  downloading,
  onBack,
}: {
  app: ProductivityApp;
  fileName: string;
  state: SaveState;
  format: { ext: string };
  onSave: () => void;
  onDownload: () => void;
  downloading: boolean;
  onBack: () => void;
}) {
  const busy = state === "loading";
  return (
    <div className="flex h-12 flex-shrink-0 items-center gap-3 border-b border-border bg-card px-3">
      <button
        type="button"
        onClick={onBack}
        title="Back to Productivity"
        className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" />
      </button>

      <span className="flex min-w-0 items-center gap-2">
        <app.Icon className="h-6 w-6 flex-shrink-0" />
        <span className="hidden text-[13px] font-semibold text-foreground sm:block">{app.name}</span>
        <span className="text-muted-foreground/50">/</span>
        <span className="truncate text-[13px] text-foreground" title={fileName}>{fileName}</span>
      </span>

      <div className="ml-auto flex items-center gap-3">
        <StateBadge state={state} />
        <button
          type="button"
          onClick={onDownload}
          disabled={busy || downloading}
          title={`Download as .${format.ext}`}
          className={cn(
            "inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[13px] font-medium text-foreground transition-colors hover:bg-muted disabled:opacity-60",
          )}
        >
          {downloading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
          Download
        </button>
        <button
          type="button"
          onClick={onSave}
          disabled={busy}
          className={cn(
            "inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60",
          )}
        >
          <Save className="h-3.5 w-3.5" />
          Save
        </button>
      </div>
    </div>
  );
}
