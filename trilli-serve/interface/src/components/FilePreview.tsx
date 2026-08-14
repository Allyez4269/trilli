import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Braces, Check, ChevronLeft, ChevronRight, Download, FileQuestion, FileText, Loader2, Moon, Pencil, Sun, X } from "lucide-react";

import { downloadUrl, previewUrl, previewPdfUrl, saveFileContent } from "@/lib/api";
import { fileExtension, formatBytes } from "@/lib/files-meta";
import { cn } from "@/lib/utils";
import SheetPreview from "@/components/SheetPreview";

// Minimal shape the viewer needs from a file row.
export interface PreviewFile {
  id: number;
  name: string;
  content_type?: string;
  size_bytes?: number;
  updated_at?: string;
}

type Kind = "image" | "pdf" | "office" | "sheet" | "video" | "audio" | "json" | "txt" | "text" | "none";

// Spreadsheets (and delimited text like CSV/TSV) render as an interactive
// in-browser grid — see SheetPreview — rather than a PDF or raw text.
const SHEET_EXT = new Set(["xls", "xlsx", "xlsm", "xlsb", "ods", "csv", "tsv"]);
const SHEET_TYPE_HINTS = ["spreadsheet", "ms-excel", "excel", "csv"];

// Word + presentation documents we preview by converting to PDF server-side.
const OFFICE_EXT = new Set(["doc", "docx", "odt", "rtf", "ppt", "pptx", "odp"]);
// Office MIME types (substring match against the file's content type). Kept
// narrow so spreadsheet/ODS types don't fall through to the PDF path.
const OFFICE_TYPE_HINTS = [
  "wordprocessing", "msword", "presentation", "ms-powerpoint", "opendocument.text",
];

// Text-ish extensions we render inline as plain/preformatted text.
const TEXT_EXT = new Set([
  "txt", "md", "markdown", "rst", "log", "json", "xml", "yml",
  "yaml", "toml", "ini", "env", "html", "htm", "css", "scss", "js", "jsx",
  "ts", "tsx", "py", "go", "rs", "java", "c", "h", "cpp", "cs", "rb", "php",
  "sh", "bash", "sql", "svg",
]);
// Don't try to render enormous text files inline.
const TEXT_MAX_BYTES = 2 * 1024 * 1024;

function kindOf(f: PreviewFile): Kind {
  const t = (f.content_type ?? "").toLowerCase();
  const e = fileExtension(f.name);
  if (t.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "bmp", "avif"].includes(e))
    return "image";
  if (t.includes("pdf") || e === "pdf") return "pdf";
  if (SHEET_EXT.has(e) || SHEET_TYPE_HINTS.some((h) => t.includes(h))) return "sheet";
  if (OFFICE_EXT.has(e) || OFFICE_TYPE_HINTS.some((h) => t.includes(h))) return "office";
  // Only formats a browser can actually play inline — NOT any video/* or audio/*.
  // e.g. .mkv (video/x-matroska) has no in-browser player, so it isn't previewable.
  if (["video/mp4", "video/webm", "video/ogg"].includes(t) || ["mp4", "webm", "mov", "m4v", "ogv"].includes(e)) return "video";
  if (
    ["audio/mpeg", "audio/mp3", "audio/wav", "audio/x-wav", "audio/ogg", "audio/webm", "audio/mp4", "audio/aac", "audio/flac", "audio/x-m4a"].includes(t) ||
    ["mp3", "wav", "ogg", "m4a", "flac", "aac"].includes(e)
  )
    return "audio";
  if (e === "json" || t.includes("json")) return "json";
  if (e === "txt") return "txt";
  if (t.startsWith("text/") || TEXT_EXT.has(e)) return "text";
  return "none";
}

// canPreview reports whether a file can be shown in the preview modal — i.e.
// kindOf classifies it as something we render or convert to PDF. Files we can't
// view/convert ("none", e.g. archives or arbitrary binaries) have no preview.
// Callers that lack a MIME type may pass just the name (extension is enough).
export function canPreview(name: string, contentType = ""): boolean {
  return kindOf({ id: 0, name, content_type: contentType }) !== "none";
}

// printUrlFor returns the URL of a printable representation of the file, or null
// when the type can't be printed (video/audio/unknown). Office docs and
// spreadsheets print via their server-converted PDF; native PDFs, images, and
// text print from their inline bytes.
export function printUrlFor(file: PreviewFile): string | null {
  switch (kindOf(file)) {
    case "pdf":
    case "image":
    case "text":
      return previewUrl(file.id, file.updated_at);
    case "office":
    case "sheet":
      return previewPdfUrl(file.id);
    default:
      return null;
  }
}

// canPrint reports whether a Print action should be offered for this file.
export function canPrint(name: string, contentType = ""): boolean {
  return printUrlFor({ id: 0, name, content_type: contentType }) !== null;
}

// FilePreview is the full-screen in-app viewer. It renders images, PDFs,
// video, audio, and text/markdown/code inline (bytes streamed through the
// authenticated session); anything else gets a clean download fallback.
// Arrow keys / on-screen chevrons move through `files`.
export interface SavedFileInfo {
  id: number;
  size_bytes: number;
  updated_at?: string;
  content_type?: string;
}

export default function FilePreview({
  files,
  index,
  onIndexChange,
  onClose,
  startInEdit = false,
  onFileSaved,
}: {
  files: PreviewFile[];
  index: number;
  onIndexChange: (i: number) => void;
  onClose: () => void;
  // Open straight into the editor (right-click → Edit on txt/json).
  startInEdit?: boolean;
  // Fires after an in-modal save so the list can refresh size/updated live.
  onFileSaved?: (f: SavedFileInfo) => void;
}) {
  const file = files[index];

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // The JSON coding pad (and any future inline input) owns its keys.
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "TEXTAREA" || t.tagName === "INPUT" || t.isContentEditable)) return;
      if (e.key === "Escape") onClose();
      else if (e.key === "ArrowLeft" && index > 0) onIndexChange(index - 1);
      else if (e.key === "ArrowRight" && index < files.length - 1) onIndexChange(index + 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [index, files.length, onIndexChange, onClose]);

  if (!file) return null;
  const kind = kindOf(file);

  return createPortal(
    <div className="fixed inset-0 z-[70] flex flex-col bg-black/80 backdrop-blur-sm animate-in fade-in duration-150">
      {/* Top bar */}
      <div className="flex items-center justify-between gap-3 px-4 py-3 text-white">
        <div className="flex min-w-0 items-center gap-3">
          <span className="truncate text-sm font-medium" title={file.name}>
            {file.name}
          </span>
          {files.length > 1 && (
            <span className="flex-shrink-0 text-xs text-white/50 tabular-nums">
              {index + 1} / {files.length}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <a
            href={downloadUrl(file.id, file.updated_at)}
            className="inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-[13px] font-medium text-white/90 hover:bg-white/10"
            title="Download"
          >
            <Download className="h-4 w-4" /> Download
          </a>
          <button
            onClick={onClose}
            className="flex h-9 w-9 items-center justify-center rounded-md text-white/80 hover:bg-white/10 hover:text-white"
            aria-label="Close"
          >
            <X className="h-5 w-5" />
          </button>
        </div>
      </div>

      {/* Body */}
      <div className="relative flex flex-1 items-center justify-center overflow-hidden px-4 pb-4">
        {index > 0 && (
          <NavArrow side="left" onClick={() => onIndexChange(index - 1)} />
        )}
        <div
          className={cn(
            "flex h-full w-full items-center justify-center",
            // Paged documents get the whole modal width — capping them at
            // max-w-6xl left wide pages clipped with no way to scroll.
            kind === "pdf" || kind === "office" || kind === "sheet"
              ? "max-w-none"
              : "max-w-6xl",
          )}
          onMouseDown={(e) => e.target === e.currentTarget && onClose()}
        >
          <PreviewBody key={file.id} file={file} kind={kind} onClose={onClose} startInEdit={startInEdit} onFileSaved={onFileSaved} />
        </div>
        {index < files.length - 1 && (
          <NavArrow side="right" onClick={() => onIndexChange(index + 1)} />
        )}
      </div>
    </div>,
    document.body,
  );
}

function NavArrow({ side, onClick }: { side: "left" | "right"; onClick: () => void }) {
  const Icon = side === "left" ? ChevronLeft : ChevronRight;
  return (
    <button
      onClick={onClick}
      className={cn(
        "absolute top-1/2 z-10 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full bg-white/10 text-white backdrop-blur transition-colors hover:bg-white/20",
        side === "left" ? "left-3" : "right-3",
      )}
      aria-label={side === "left" ? "Previous" : "Next"}
    >
      <Icon className="h-6 w-6" />
    </button>
  );
}

function PreviewBody({ file, kind, onClose, startInEdit = false, onFileSaved }: {
  file: PreviewFile; kind: Kind; onClose: () => void;
  startInEdit?: boolean; onFileSaved?: (f: SavedFileInfo) => void;
}) {
  const url = previewUrl(file.id, file.updated_at);
  if (kind === "image") {
    return (
      <img
        src={url}
        alt={file.name}
        className="max-h-full max-w-full rounded-lg object-contain shadow-2xl"
      />
    );
  }
  if (kind === "pdf") {
    return (
      <iframe
        // #zoom=90 opens the browser's PDF viewer at 90% (owner-chosen default
        // — whole page comfortably in view); #navpanes=0 keeps the thumbnail
        // sidebar closed so the page gets the full pane. Zooming past fit
        // brings up the viewer's own scrollbars as usual.
        src={`${url}#zoom=90&navpanes=0`}
        title={file.name}
        className="h-full w-full rounded-lg border-0 bg-white shadow-2xl"
      />
    );
  }
  if (kind === "office") {
    return <OfficePreview file={file} onClose={onClose} />;
  }
  if (kind === "sheet") {
    return <SheetPreview fileId={file.id} name={file.name} />;
  }
  if (kind === "video") {
    return (
      <video src={url} controls className="max-h-full max-w-full rounded-lg shadow-2xl" />
    );
  }
  if (kind === "audio") {
    return (
      <div className="w-full max-w-lg rounded-xl bg-card p-6 shadow-2xl">
        <p className="mb-3 truncate text-sm font-medium text-foreground">{file.name}</p>
        <audio src={url} controls className="w-full" />
      </div>
    );
  }
  if (kind === "json") {
    return <JsonPreview file={file} onClose={onClose} startInEdit={startInEdit} onSaved={onFileSaved} />;
  }
  if (kind === "txt") {
    return <TxtPreview file={file} onClose={onClose} startInEdit={startInEdit} onSaved={onFileSaved} />;
  }
  if (kind === "text") {
    return <TextPreview file={file} onClose={onClose} />;
  }
  return <Unsupported file={file} onClose={onClose} />;
}

// OfficePreview renders Word/Excel/PowerPoint documents by asking the server
// for a PDF rendering (converted + cached server-side; the original is never
// touched). It fetches through the authenticated session into a blob URL so we
// can show a converting spinner and fall back cleanly if rendering fails.
function OfficePreview({ file, onClose }: { file: PreviewFile; onClose: () => void }) {
  const [state, setState] = useState<{ url?: string; error?: string; loading: boolean }>({
    loading: true,
  });
  useEffect(() => {
    let cancelled = false;
    let objectUrl: string | undefined;
    fetch(previewPdfUrl(file.id), { credentials: "same-origin" })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((blob) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setState({ loading: false, url: objectUrl });
      })
      .catch(() =>
        !cancelled &&
        setState({ loading: false, error: "We couldn't render a preview for this document." }),
      );
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [file.id]);

  if (state.loading) {
    return (
      <div className="flex flex-col items-center gap-3 text-white/70">
        <Loader2 className="h-6 w-6 animate-spin" />
        <p className="text-sm">Preparing preview…</p>
      </div>
    );
  }
  if (state.error || !state.url) {
    return <Unsupported file={file} message={state.error} onClose={onClose} />;
  }
  return (
    <iframe
      // Same 90%-zoom + closed-sidebar treatment as the direct PDF path —
      // converted Office docs are rendered by the same browser PDF viewer.
      src={`${state.url}#zoom=90&navpanes=0`}
      title={file.name}
      className="h-full w-full rounded-lg border-0 bg-white shadow-2xl"
    />
  );
}

// ---------------------------------------------------------------- JSON -----
// Pretty JSON viewer + editor on a subtle midnight theme. View mode shows the
// document prettified with syntax highlighting and line numbers; Edit opens a
// dark coding pad (same theme, live validity, Tab = 2 spaces) that saves back
// in place via PUT /api/files/{id}/content.
const MID = {
  bg: "#0B1220",       // canvas
  panel: "#0E172A",    // header / footer bars
  border: "#1D2A44",
  gutter: "#3E4E6E",
  gutterLine: "#16203A",
  text: "#C7D4EA",
  key: "#8FB6FF",
  str: "#9DD0A5",
  num: "#E2B36B",
  lit: "#C09AE8",      // true / false / null
  punct: "#546890",
};

const JSON_TOKEN =
  /("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|([{}[\],])/g;

// Highlight one line of pretty-printed JSON into colored spans.
function highlightJsonLine(line: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let k = 0;
  for (const m of line.matchAll(JSON_TOKEN)) {
    const i = m.index ?? 0;
    if (i > last) out.push(<span key={k++} style={{ color: MID.text }}>{line.slice(last, i)}</span>);
    if (m[1] !== undefined) {
      const isKey = m[2] !== undefined;
      out.push(<span key={k++} style={{ color: isKey ? MID.key : MID.str }}>{m[1]}</span>);
      if (m[2]) out.push(<span key={k++} style={{ color: MID.punct }}>{m[2]}</span>);
    } else if (m[3] !== undefined) {
      out.push(<span key={k++} style={{ color: MID.lit }}>{m[3]}</span>);
    } else if (m[4] !== undefined) {
      out.push(<span key={k++} style={{ color: MID.num }}>{m[4]}</span>);
    } else if (m[5] !== undefined) {
      out.push(<span key={k++} style={{ color: MID.punct }}>{m[5]}</span>);
    }
    last = i + m[0].length;
  }
  if (last < line.length) out.push(<span key={k++} style={{ color: MID.text }}>{line.slice(last)}</span>);
  return out;
}

// Past this many lines we skip token coloring to keep the DOM light.
const HIGHLIGHT_MAX_LINES = 6000;

function JsonPreview({ file, onClose, startInEdit = false, onSaved }: {
  file: PreviewFile; onClose: () => void; startInEdit?: boolean; onSaved?: (f: SavedFileInfo) => void;
}) {
  // Enter edit mode at most once per opened file (not again on post-save refetch).
  const editConsumedFor = useRef<number | null>(null);
  const [state, setState] = useState<{ text?: string; error?: string; loading: boolean }>({ loading: true });
  const [mode, setMode] = useState<"view" | "edit">("view");
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const gutterRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setMode("view");
    setSaveError(null);
    if ((file.size_bytes ?? 0) > TEXT_MAX_BYTES) {
      setState({ loading: false, error: "This file is too large to preview." });
      return;
    }
    let cancelled = false;
    setState({ loading: true });
    fetch(previewUrl(file.id, file.updated_at), { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((text) => {
        if (cancelled) return;
        setState({ loading: false, text });
        if (startInEdit && editConsumedFor.current !== file.id) {
          editConsumedFor.current = file.id;
          let pretty = text;
          try { pretty = JSON.stringify(JSON.parse(text), null, 2); } catch { /* raw */ }
          setDraft(pretty);
          setMode("edit");
        }
      })
      .catch(() => !cancelled && setState({ loading: false, error: "Couldn't load this file." }));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [file.id, file.size_bytes, file.updated_at]);

  // Pretty-print when the source parses; fall back to the raw text when not.
  const { pretty, valid } = useMemo(() => {
    const raw = state.text ?? "";
    try {
      return { pretty: JSON.stringify(JSON.parse(raw), null, 2), valid: true };
    } catch {
      return { pretty: raw, valid: false };
    }
  }, [state.text]);

  // Live validity of the coding-pad draft.
  const draftError = useMemo(() => {
    if (mode !== "edit") return null;
    try {
      JSON.parse(draft);
      return null;
    } catch (e) {
      return e instanceof Error ? e.message.replace(/^JSON\.parse: /, "") : "Invalid JSON";
    }
  }, [draft, mode]);

  const startEdit = () => {
    setDraft(pretty);
    setSaveError(null);
    setMode("edit");
  };

  const formatDraft = () => {
    try {
      setDraft(JSON.stringify(JSON.parse(draft), null, 2));
    } catch {
      /* leave as-is; validity chip already explains */
    }
  };

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const rec = await saveFileContent(file.id, draft, "application/json");
      setState({ loading: false, text: draft });
      setMode("view");
      onSaved?.(rec);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : "Couldn't save the file.");
    } finally {
      setSaving(false);
    }
  };

  if (state.loading) return <Loader2 className="h-6 w-6 animate-spin text-white/70" />;
  if (state.error) return <Unsupported file={file} message={state.error} onClose={onClose} />;

  const lines = (mode === "edit" ? draft : pretty).split("\n");
  const gutterW = Math.max(String(lines.length).length, 2);

  return (
    <div
      className="flex h-full w-full flex-col overflow-hidden rounded-lg shadow-2xl ring-1"
      style={{ backgroundColor: MID.bg, borderColor: MID.border, ["--tw-ring-color" as string]: MID.border }}
    >
      {/* header bar */}
      <div
        className="flex flex-shrink-0 items-center gap-2.5 border-b px-4 py-2"
        style={{ backgroundColor: MID.panel, borderColor: MID.border }}
      >
        <Braces className="h-4 w-4" style={{ color: MID.key }} />
        <span className="text-[12.5px] font-semibold" style={{ color: MID.text }}>JSON</span>
        <span className="text-[11.5px] tabular-nums" style={{ color: MID.gutter }}>
          {lines.length.toLocaleString()} lines
        </span>
        {mode === "view" ? (
          valid ? (
            <span className="inline-flex items-center gap-1 rounded px-1.5 py-px text-[10px] font-bold uppercase tracking-wide"
              style={{ color: "#7FCB8B", backgroundColor: "#12291C" }}>
              <Check className="h-3 w-3" /> Valid
            </span>
          ) : (
            <span className="rounded px-1.5 py-px text-[10px] font-bold uppercase tracking-wide"
              style={{ color: "#E2B36B", backgroundColor: "#2C2214" }}>
              Not valid JSON — showing raw
            </span>
          )
        ) : draftError ? (
          <span className="max-w-[40%] truncate rounded px-1.5 py-px text-[10px] font-bold uppercase tracking-wide"
            title={draftError} style={{ color: "#F0A3A3", backgroundColor: "#2E1620" }}>
            {draftError}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 rounded px-1.5 py-px text-[10px] font-bold uppercase tracking-wide"
            style={{ color: "#7FCB8B", backgroundColor: "#12291C" }}>
            <Check className="h-3 w-3" /> Valid
          </span>
        )}
        <span className="flex-1" />
        {mode === "view" ? (
          <button type="button" onClick={startEdit}
            className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-semibold transition-colors"
            style={{ color: MID.text, backgroundColor: "#182338" }}>
            <Pencil className="h-3.5 w-3.5" /> Edit
          </button>
        ) : (
          <>
            <button type="button" onClick={formatDraft} disabled={!!draftError}
              className="rounded-md px-2.5 py-1 text-[12px] font-semibold transition-colors disabled:opacity-40"
              style={{ color: MID.text, backgroundColor: "#182338" }}>
              Format
            </button>
            <button type="button" onClick={() => setMode("view")}
              className="rounded-md px-2.5 py-1 text-[12px] font-semibold"
              style={{ color: MID.gutter }}>
              Cancel
            </button>
            <button type="button" onClick={() => void save()} disabled={!!draftError || saving}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1 text-[12px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-40">
              {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
              Save
            </button>
          </>
        )}
      </div>

      {saveError && (
        <p className="border-b px-4 py-1.5 text-[12px]" style={{ color: "#F0A3A3", backgroundColor: "#1C1220", borderColor: MID.border }}>
          {saveError}
        </p>
      )}

      {/* body */}
      {mode === "view" ? (
        <div className="min-h-0 flex-1 overflow-auto font-mono text-[12.5px] leading-[1.6]">
          <div className="min-w-max py-3">
            {lines.map((line, i) => (
              <div key={i} className="flex">
                <span
                  className="sticky left-0 select-none border-r pr-3 text-right tabular-nums"
                  style={{
                    color: MID.gutter, borderColor: MID.gutterLine, backgroundColor: MID.bg,
                    minWidth: `${gutterW + 2.2}ch`,
                  }}
                >
                  {i + 1}
                </span>
                <span className="whitespace-pre pl-4 pr-6">
                  {lines.length <= HIGHLIGHT_MAX_LINES
                    ? highlightJsonLine(line)
                    : <span style={{ color: MID.text }}>{line}</span>}
                </span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 font-mono text-[12.5px] leading-[1.6]">
          {/* line-number gutter, scroll-synced to the pad */}
          <div
            ref={gutterRef}
            className="flex-shrink-0 select-none overflow-hidden border-r py-3 pr-3 text-right tabular-nums"
            style={{ color: MID.gutter, borderColor: MID.gutterLine, minWidth: `${gutterW + 2.2}ch` }}
            aria-hidden="true"
          >
            {lines.map((_, i) => (
              <div key={i}>{i + 1}</div>
            ))}
          </div>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onScroll={(e) => {
              if (gutterRef.current) gutterRef.current.scrollTop = e.currentTarget.scrollTop;
            }}
            onKeyDown={(e) => {
              if (e.key === "Tab") {
                e.preventDefault();
                const el = e.currentTarget;
                const { selectionStart: s0, selectionEnd: s1 } = el;
                setDraft(draft.slice(0, s0) + "  " + draft.slice(s1));
                requestAnimationFrame(() => el.setSelectionRange(s0 + 2, s0 + 2));
              } else if (e.key === "Escape") {
                e.stopPropagation();
                setMode("view");
              }
            }}
            spellCheck={false}
            autoFocus
            className="min-h-0 flex-1 resize-none overflow-auto whitespace-pre bg-transparent py-3 pl-4 pr-6 outline-none"
            style={{ color: MID.text, caretColor: MID.key }}
          />
        </div>
      )}
    </div>
  );
}

// ----------------------------------------------------------------- TXT -----
// Plain-text viewer + editor. Night theme by default with a daylight
// secondary (toggle persists), no line numbers — just a clean reading/writing
// surface with Edit and Save.
const TXT_THEMES = {
  night: {
    bg: "#0B1220", panel: "#0E172A", border: "#1D2A44",
    text: "#C9D6EC", dim: "#5A6B8C", chip: "#182338",
  },
  day: {
    bg: "#FFFFFF", panel: "#F6F8FB", border: "#E3E8EE",
    text: "#2A3446", dim: "#7C8AA0", chip: "#EDF1F7",
  },
} as const;
type TxtTheme = keyof typeof TXT_THEMES;

const TXT_THEME_KEY = "trilli.txtPreviewTheme";

function TxtPreview({ file, onClose, startInEdit = false, onSaved }: {
  file: PreviewFile; onClose: () => void; startInEdit?: boolean; onSaved?: (f: SavedFileInfo) => void;
}) {
  const editConsumedFor = useRef<number | null>(null);
  const [state, setState] = useState<{ text?: string; error?: string; loading: boolean }>({ loading: true });
  const [mode, setMode] = useState<"view" | "edit">("view");
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [theme, setTheme] = useState<TxtTheme>(() =>
    localStorage.getItem(TXT_THEME_KEY) === "day" ? "day" : "night",
  );
  const T = TXT_THEMES[theme];

  useEffect(() => {
    setMode("view");
    setSaveError(null);
    if ((file.size_bytes ?? 0) > TEXT_MAX_BYTES) {
      setState({ loading: false, error: "This file is too large to preview." });
      return;
    }
    let cancelled = false;
    setState({ loading: true });
    fetch(previewUrl(file.id, file.updated_at), { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((text) => {
        if (cancelled) return;
        setState({ loading: false, text });
        if (startInEdit && editConsumedFor.current !== file.id) {
          editConsumedFor.current = file.id;
          setDraft(text);
          setMode("edit");
        }
      })
      .catch(() => !cancelled && setState({ loading: false, error: "Couldn't load this file." }));
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [file.id, file.size_bytes, file.updated_at]);

  const flipTheme = () => {
    const next: TxtTheme = theme === "night" ? "day" : "night";
    setTheme(next);
    localStorage.setItem(TXT_THEME_KEY, next);
  };

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const rec = await saveFileContent(file.id, draft, "text/plain; charset=utf-8");
      setState({ loading: false, text: draft });
      setMode("view");
      onSaved?.(rec);
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : "Couldn't save the file.");
    } finally {
      setSaving(false);
    }
  };

  if (state.loading) return <Loader2 className="h-6 w-6 animate-spin text-white/70" />;
  if (state.error) return <Unsupported file={file} message={state.error} onClose={onClose} />;

  return (
    <div
      className="flex h-full w-full flex-col overflow-hidden rounded-lg shadow-2xl ring-1"
      style={{ backgroundColor: T.bg, ["--tw-ring-color" as string]: T.border }}
    >
      {/* header bar */}
      <div
        className="flex flex-shrink-0 items-center gap-2.5 border-b px-4 py-2"
        style={{ backgroundColor: T.panel, borderColor: T.border }}
      >
        <FileText className="h-4 w-4" style={{ color: T.dim }} />
        <span className="text-[12.5px] font-semibold" style={{ color: T.text }}>Text</span>
        <span className="text-[11.5px]" style={{ color: T.dim }}>
          {formatBytes(file.size_bytes ?? 0)}
        </span>
        <span className="flex-1" />
        <button
          type="button"
          onClick={flipTheme}
          title={theme === "night" ? "Switch to daylight" : "Switch to night"}
          className="rounded-md p-1.5 transition-colors"
          style={{ color: T.dim, backgroundColor: T.chip }}
        >
          {theme === "night" ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
        </button>
        {mode === "view" ? (
          <button
            type="button"
            onClick={() => { setDraft(state.text ?? ""); setSaveError(null); setMode("edit"); }}
            className="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-semibold transition-colors"
            style={{ color: T.text, backgroundColor: T.chip }}
          >
            <Pencil className="h-3.5 w-3.5" /> Edit
          </button>
        ) : (
          <>
            <button type="button" onClick={() => setMode("view")}
              className="rounded-md px-2.5 py-1 text-[12px] font-semibold" style={{ color: T.dim }}>
              Cancel
            </button>
            <button type="button" onClick={() => void save()} disabled={saving}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1 text-[12px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-40">
              {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
              Save
            </button>
          </>
        )}
      </div>

      {saveError && (
        <p className="border-b px-4 py-1.5 text-[12px] text-destructive" style={{ borderColor: T.border }}>
          {saveError}
        </p>
      )}

      {/* body — no line numbers, just a clean page */}
      {mode === "view" ? (
        <div className="min-h-0 flex-1 overflow-auto">
          <pre
            className="whitespace-pre-wrap break-words px-8 py-6 font-mono text-[13px] leading-[1.75]"
            style={{ color: T.text }}
          >
            {state.text}
          </pre>
        </div>
      ) : (
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.stopPropagation();
              setMode("view");
            }
          }}
          spellCheck={false}
          autoFocus
          className="min-h-0 flex-1 resize-none overflow-auto whitespace-pre-wrap break-words bg-transparent px-8 py-6 font-mono text-[13px] leading-[1.75] outline-none"
          style={{ color: T.text, caretColor: T.text }}
        />
      )}
    </div>
  );
}

function TextPreview({ file, onClose }: { file: PreviewFile; onClose: () => void }) {
  const [state, setState] = useState<{ text?: string; error?: string; loading: boolean }>({
    loading: true,
  });
  useEffect(() => {
    if ((file.size_bytes ?? 0) > TEXT_MAX_BYTES) {
      setState({ loading: false, error: "This file is too large to preview." });
      return;
    }
    let cancelled = false;
    fetch(previewUrl(file.id, file.updated_at), { credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((text) => !cancelled && setState({ loading: false, text }))
      .catch(() => !cancelled && setState({ loading: false, error: "Couldn't load this file." }));
    return () => {
      cancelled = true;
    };
  }, [file.id, file.size_bytes]);

  if (state.loading) {
    return <Loader2 className="h-6 w-6 animate-spin text-white/70" />;
  }
  if (state.error) {
    return <Unsupported file={file} message={state.error} onClose={onClose} />;
  }
  return (
    <div className="h-full w-full overflow-auto rounded-lg bg-card shadow-2xl">
      <pre className="whitespace-pre-wrap break-words p-5 text-[12.5px] leading-relaxed text-foreground">
        {state.text}
      </pre>
    </div>
  );
}

function Unsupported({
  file,
  message,
  onClose,
}: {
  file: PreviewFile;
  message?: string;
  onClose: () => void;
}) {
  return (
    <div className="relative flex flex-col items-center justify-center rounded-xl bg-card px-10 py-12 text-center shadow-2xl">
      <button
        onClick={onClose}
        className="absolute right-3 top-3 flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label="Close"
      >
        <X className="h-4 w-4" />
      </button>
      <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-secondary">
        <FileQuestion className="h-7 w-7 text-muted-foreground" />
      </div>
      <h3 className="mb-1 text-base font-semibold text-foreground">
        {message ? "Can't preview this file" : "Preview not available"}
      </h3>
      <p className="mb-4 max-w-xs text-sm text-muted-foreground">
        {message ??
          `${(fileExtension(file.name) || "This").toUpperCase()} files can't be previewed yet${
            file.size_bytes ? ` · ${formatBytes(file.size_bytes)}` : ""
          }. Download it to open in a desktop app.`}
      </p>
      <div className="flex items-center gap-2">
        <a
          href={downloadUrl(file.id, file.updated_at)}
          className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-4 text-[13px] font-semibold text-primary-foreground hover:bg-primary/90"
        >
          <Download className="h-4 w-4" /> Download
        </a>
        <button
          onClick={onClose}
          className="inline-flex h-9 items-center rounded-md border border-border bg-background px-4 text-[13px] font-medium text-foreground hover:bg-muted"
        >
          Close
        </button>
      </div>
    </div>
  );
}
