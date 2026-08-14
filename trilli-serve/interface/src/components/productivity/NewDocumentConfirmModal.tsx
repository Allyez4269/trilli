// Confirmation modal shown when the user starts a New document (or opens a
// different file) while the current document has unsaved changes — mirrors the
// Word pattern: "Do you want to save the changes?" → Save (opens Save As) /
// Don't Save (discard) / Cancel (go back).
import { createPortal } from "react-dom";
import { AlertTriangle, Loader2 } from "lucide-react";

export function NewDocumentConfirmModal({
  fileName,
  onClose,
  onSave,
  onDiscard,
  saving,
}: {
  fileName: string;
  onClose: () => void;
  onSave: () => void;
  onDiscard: () => void;
  saving?: boolean;
}) {
  return createPortal(
    <div
      className="fixed inset-0 z-[85] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm animate-in fade-in duration-150"
      onMouseDown={(e) => e.target === e.currentTarget && !saving && onClose()}
    >
      <div className="w-full max-w-sm overflow-hidden rounded-xl border border-border bg-card shadow-2xl animate-in zoom-in-95 fade-in duration-200">
        {/* Header + body */}
        <div className="flex items-start gap-3 px-5 py-4">
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-amber-500/10">
            <AlertTriangle className="h-5 w-5 text-amber-600" />
          </div>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-foreground">
              Do you want to save your changes?
            </h2>
            <p className="mt-1 text-[13px] text-muted-foreground">
              {fileName ? `"${fileName}"` : "Your document"} has unsaved changes. If
              you don't save, your work will be discarded.
            </p>
          </div>
        </div>
        {/* Footer — Cancel left, Save (primary) + Don't Save (destructive) right */}
        <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={saving}
            className="h-8 rounded-md border border-border px-3 text-[13px] text-foreground transition-colors hover:bg-muted disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onDiscard}
            disabled={saving}
            className="h-8 rounded-md border border-border px-3 text-[13px] text-destructive transition-colors hover:bg-destructive/5 disabled:opacity-50"
          >
            Don't Save
          </button>
          <button
            type="button"
            onClick={onSave}
            disabled={saving}
            className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3.5 text-[13px] font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-60"
          >
            {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Save
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
