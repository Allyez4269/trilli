// Trilli PDF — the standardized tool shell. Every sub-app uses this same frame:
// choose input file(s) (from your Trilli space via the shared picker, or upload)
// → set params → Run → result with Download / Save back to Trilli. Read-only
// "info" renders the inspected metadata instead. Driven entirely by the tool's
// registry entry, so each new tool inherits the whole UX for free.
import { useRef, useState } from "react";
import { Link, Navigate, useParams } from "react-router-dom";
import { ArrowLeft, ChevronDown, Download, FileText, FolderOpen, Loader2, Save, Upload, X } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { FileDropOverlay } from "@/components/FileDropOverlay";
import { OpenFromTrilliModal } from "@/components/productivity/OpenFromTrilliModal";
import { SaveToTrilliModal } from "@/components/productivity/SaveToTrilliModal";
import { PdfToolIcon } from "@/lib/pdf/icons";
import { pdfToolByKey } from "@/lib/pdf/tools";
import { runPdfTool, type ToolInput } from "@/lib/pdf/runTool";
import type { FileRecord } from "@/lib/api";
import { cn } from "@/lib/utils";

type Result =
  | { kind: "pdf"; blob: Blob; url: string; name: string }
  | { kind: "info"; data: Record<string, unknown> }
  | null;

export default function PdfTool() {
  const { tool: toolKey } = useParams();
  const tool = pdfToolByKey(toolKey);

  const fileInput = useRef<HTMLInputElement>(null);
  const [trilliFiles, setTrilliFiles] = useState<FileRecord[]>([]);
  const [uploads, setUploads] = useState<File[]>([]);
  const [params, setParams] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    (tool?.params ?? []).forEach((p) => {
      if (p.kind === "select") init[p.name] = p.default;
    });
    return init;
  });
  const [pickerOpen, setPickerOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<Result>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState(false);

  if (!tool) return <Navigate to="/apps/pdf" replace />;

  const single = tool.input === "single";
  const accept = tool.accept ?? ["pdf"];
  const acceptRe = new RegExp(`\\.(${accept.join("|")})$`, "i");
  const noun = accept[0] === "pdf" ? "PDF" : "image";
  const outExt = tool.output ?? "pdf";
  const items = [
    ...trilliFiles.map((f, i) => ({
      id: `t${f.id}`,
      name: f.name,
      from: "Trilli" as const,
      remove: () => setTrilliFiles((a) => a.filter((_, j) => j !== i)),
    })),
    ...uploads.map((f, i) => ({
      id: `u${i}`,
      name: f.name,
      from: "Upload" as const,
      remove: () => setUploads((a) => a.filter((_, j) => j !== i)),
    })),
  ];

  const addUploads = (list: FileList | null) => {
    const files = Array.from(list ?? []).filter((f) => acceptRe.test(f.name));
    if (!files.length) return;
    setResult(null);
    setError(null);
    if (single) {
      setTrilliFiles([]);
      setUploads([files[0]]);
    } else {
      setUploads((a) => [...a, ...files]);
    }
  };

  // Drag & drop — mirrors the Files area: only react to OS file drags, show the
  // copy cursor + highlight, and clear the highlight only when the drag truly
  // leaves the zone (not when crossing child elements).
  const onDragOver = (e: React.DragEvent) => {
    if (!e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "copy";
    if (!dragOver) setDragOver(true);
  };
  const onDragLeave = (e: React.DragEvent) => {
    const next = e.relatedTarget as Node | null;
    if (next && e.currentTarget.contains(next)) return;
    setDragOver(false);
  };
  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    addUploads(e.dataTransfer.files);
  };

  const addTrilliFile = async (f: FileRecord) => {
    setResult(null);
    setError(null);
    if (single) {
      setUploads([]);
      setTrilliFiles([f]);
    } else {
      setTrilliFiles((a) => (a.some((x) => x.id === f.id) ? a : [...a, f]));
    }
    setPickerOpen(false);
  };

  const currentInput = (): ToolInput => ({ fileIds: trilliFiles.map((f) => f.id), uploads });

  const validate = (): string | null => {
    if (single && items.length !== 1) return `Choose one ${noun} file.`;
    if (!single) {
      const min = tool.key === "merge" ? 2 : 1;
      if (items.length < min) return min === 2 ? "Choose at least two PDF files." : `Add at least one ${noun}.`;
    }
    for (const p of tool.params) {
      if ((p.kind === "text" || p.kind === "password") && p.required && !params[p.name]?.trim()) {
        return `${p.label} is required.`;
      }
    }
    return null;
  };

  const run = async () => {
    const v = validate();
    if (v) {
      setError(v);
      return;
    }
    setError(null);
    setRunning(true);
    setResult(null);
    try {
      const out = await runPdfTool(tool.key, currentInput(), params, { mode: "download" });
      if (out.kind === "blob") {
        const base = items[0]?.name?.replace(/\.[^.]+$/, "") ?? "result";
        const name = `${base}-${tool.key}.${outExt}`;
        setResult({ kind: "pdf", blob: out.blob, url: URL.createObjectURL(out.blob), name });
      } else {
        setResult({ kind: "info", data: out.data as Record<string, unknown> });
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      setRunning(false);
    }
  };

  const flash = (msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 3000);
  };

  return (
    // Full main-area drag target (like the Files scroller): drag a file ANYWHERE
    // on the page — including the side margins — and the dropzone lights up; a drop
    // anywhere uploads it. The centered column lives inside.
    <div className="flex-1 overflow-y-auto" onDragOver={onDragOver} onDragLeave={onDragLeave} onDrop={onDrop}>
      <div className="mx-auto max-w-7xl px-6 py-8 lg:px-8">
        <Link to="/apps/pdf" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" /> All PDF tools
      </Link>

      <header className="mb-6 flex items-center gap-3">
        {/* the tool's own icon, framed as a miniature card (no hover) */}
        <div className="flex-shrink-0 rounded-xl border border-border bg-card p-1.5 shadow-sm">
          <div className="flex h-11 w-11 items-center justify-center rounded-md border border-border/70 bg-muted/20">
            <PdfToolIcon toolKey={tool.key} size={32} />
          </div>
        </div>
        <div>
          <h1 className="font-pdf text-[24px] font-bold tracking-[-0.01em] text-foreground/80">{tool.name}</h1>
          <p className="text-[13px] text-muted-foreground">{tool.blurb}</p>
        </div>
      </header>

      {/* 1 — input files */}
      <section className="mb-5">
        <div className="mb-2 flex items-center justify-between">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="inline-flex items-center gap-1 rounded-lg bg-primary px-2.5 py-1 text-[12.5px] font-medium text-primary-foreground outline-none transition-colors hover:bg-primary/90 focus:outline-none focus-visible:outline-none focus-visible:ring-0"
              >
                <FolderOpen className="h-3.5 w-3.5" /> Open {single ? "file" : "files"}
                <ChevronDown className="h-3.5 w-3.5 opacity-80" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-52">
              <DropdownMenuItem onSelect={() => setPickerOpen(true)} className="gap-2 text-[13px]">
                <FolderOpen className="h-4 w-4" /> From your Trilli files
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => fileInput.current?.click()} className="gap-2 text-[13px]">
                <Upload className="h-4 w-4" /> Upload
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <h2 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {single ? "File" : "Files"}
          </h2>
        </div>

        {items.length > 0 && (
          <ul className="mb-3 space-y-1.5">
            {items.map((it) => (
              <li key={it.id} className="flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 text-[13px]">
                <FileText className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate text-foreground">{it.name}</span>
                <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">{it.from}</span>
                <button type="button" onClick={it.remove} className="text-muted-foreground hover:text-foreground" aria-label="Remove">
                  <X className="h-4 w-4" />
                </button>
              </li>
            ))}
          </ul>
        )}

        {/* Clicking the zone opens the standardized Trilli file dialog; drag & drop
            (handled at the page level) and the device link cover ad-hoc uploads. */}
        <div
          onClick={() => setPickerOpen(true)}
          className={cn(
            "flex cursor-pointer flex-col items-center justify-center gap-3 rounded-xl border border-dashed px-6 py-10 text-center transition-colors",
            dragOver ? "border-primary bg-primary/5" : "border-border hover:bg-muted/40",
          )}
        >
          <div
            className={cn(
              "flex h-12 w-12 items-center justify-center rounded-full transition-colors",
              dragOver ? "bg-primary text-primary-foreground" : "bg-secondary text-foreground/70",
            )}
          >
            <FolderOpen className="h-5 w-5" />
          </div>
          <div>
            <p className="text-sm font-medium text-foreground">
              {dragOver
                ? `Drop ${single ? `the ${noun}` : `${noun}s`} to add`
                : `Open ${single ? `a ${noun}` : `${noun}s`} from your files`}
            </p>
            <p className="text-[11px] text-muted-foreground">
              or drag &amp; drop here ·{" "}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  fileInput.current?.click();
                }}
                className="font-medium text-primary hover:underline"
              >
                upload
              </button>{" "}
              · {accept.map((e) => e.toUpperCase()).join(", ")}
            </p>
          </div>
        </div>

        <input
          ref={fileInput}
          type="file"
          accept={accept.map((e) => "." + e).join(",")}
          multiple={!single}
          className="hidden"
          onChange={(e) => {
            addUploads(e.target.files);
            e.target.value = "";
          }}
        />
      </section>

      {/* 2 — params */}
      {tool.params.length > 0 && (
        <section className="mb-5">
          <h2 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Options</h2>
          <div className="space-y-3 rounded-xl border border-border bg-card p-4">
            {tool.params.map((p) => (
              <label key={p.name} className="block">
                <span className="mb-1 block text-[13px] font-medium text-foreground">{p.label}</span>
                {p.kind === "select" ? (
                  <select
                    value={params[p.name] ?? p.default}
                    onChange={(e) => setParams((s) => ({ ...s, [p.name]: e.target.value }))}
                    className="w-full rounded-lg border border-border bg-background px-3 py-1.5 text-[13px] text-foreground outline-none focus:ring-2 focus:ring-primary/30"
                  >
                    {p.options.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                ) : (
                  <input
                    type={p.kind === "password" ? "password" : "text"}
                    placeholder={p.placeholder}
                    value={params[p.name] ?? ""}
                    onChange={(e) => setParams((s) => ({ ...s, [p.name]: e.target.value }))}
                    className="w-full rounded-lg border border-border bg-background px-3 py-1.5 text-[13px] text-foreground outline-none focus:ring-2 focus:ring-primary/30"
                  />
                )}
              </label>
            ))}
          </div>
        </section>
      )}

      {error && <p className="mb-4 rounded-lg bg-destructive/10 px-3 py-2 text-[13px] text-destructive">{error}</p>}

      {/* 3 — run */}
      <button
        type="button"
        onClick={run}
        disabled={running}
        className="inline-flex min-w-[13rem] items-center justify-center gap-2 rounded-lg px-4 py-2 text-[14px] font-semibold text-white shadow-sm transition-colors disabled:opacity-60"
        style={{ backgroundColor: tool.color }}
      >
        {running ? <Loader2 className="h-4 w-4 animate-spin" /> : <tool.Icon className="h-4 w-4" />}
        {running ? "Working…" : tool.cta}
      </button>

      {/* 4 — result */}
      {result?.kind === "pdf" && (
        <section className="mt-6 rounded-xl border border-border bg-card p-5">
          <p className="text-[14px] font-semibold text-foreground">
            {outExt === "zip" ? "Your ZIP is ready" : "Your PDF is ready"}
          </p>
          <p className="mt-0.5 text-[13px] text-muted-foreground">{result.name}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <a
              href={result.url}
              download={result.name}
              className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-1.5 text-[13px] font-medium text-primary-foreground hover:bg-primary/90"
            >
              <Download className="h-4 w-4" /> Download
            </a>
            <button
              type="button"
              onClick={() => setSaveOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-[13px] font-medium text-foreground hover:bg-muted"
            >
              <Save className="h-4 w-4" /> Save to Trilli
            </button>
          </div>
        </section>
      )}

      {result?.kind === "info" && <InfoTable data={result.data} />}

      {pickerOpen && (
        <OpenFromTrilliModal
          extensions={accept}
          onClose={() => setPickerOpen(false)}
          onOpen={addTrilliFile}
        />
      )}

      {saveOpen && (
        <SaveToTrilliModal
          defaultName={result?.kind === "pdf" ? result.name : tool.defaultName}
          formats={[{ ext: outExt, label: outExt.toUpperCase() }]}
          format={{ ext: outExt, label: outExt.toUpperCase() }}
          onFormatChange={() => {}}
          onClose={() => setSaveOpen(false)}
          onSave={async (target) => {
            await runPdfTool(tool.key, currentInput(), params, {
              mode: "save",
              folderId: target.folderId,
              name: target.name,
            });
            setSaveOpen(false);
            flash(`Saved to ${target.location}`);
          }}
        />
      )}

      {toast && (
        <div className="fixed bottom-5 left-1/2 -translate-x-1/2 rounded-lg bg-foreground px-4 py-2 text-[13px] font-medium text-background shadow-lg">
          {toast}
        </div>
      )}

      <FileDropOverlay label={`Drop your ${noun}${single ? "" : "s"} here`} hint={`Release to add ${single ? "it" : "them"}`} />
      </div>
    </div>
  );
}

// InfoTable renders the most useful fields of a pdfcpu PDFInfo for the "info" tool.
function InfoTable({ data }: { data: Record<string, unknown> }) {
  const str = (v: unknown) => (v == null || v === "" ? "—" : String(v));
  const yn = (v: unknown) => (v ? "Yes" : "No");
  const sizes = Array.isArray(data.pageSizes)
    ? (data.pageSizes as Array<{ width?: number; height?: number }>)
        .map((d) => (d.width && d.height ? `${Math.round(d.width)} × ${Math.round(d.height)} pt` : null))
        .filter(Boolean)
        .join(", ")
    : "";
  const rows: [string, string][] = [
    ["Pages", str(data.pageCount)],
    ["PDF version", str(data.version)],
    ["Page size", sizes || "—"],
    ["Title", str(data.title)],
    ["Author", str(data.author)],
    ["Encrypted", yn(data.encrypted)],
    ["Has form", yn(data.form)],
    ["Signatures", yn(data.signatures)],
    ["Bookmarks", yn(data.bookmarks)],
    ["Tagged", yn(data.tagged)],
  ];
  return (
    <section className="mt-6 overflow-hidden rounded-xl border border-border bg-card">
      <p className="border-b border-border px-5 py-3 text-[14px] font-semibold text-foreground">Document details</p>
      <dl className="divide-y divide-border">
        {rows.map(([k, v]) => (
          <div key={k} className="flex items-center justify-between gap-4 px-5 py-2.5">
            <dt className="text-[13px] text-muted-foreground">{k}</dt>
            <dd className="min-w-0 truncate text-right text-[13px] font-medium text-foreground">{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
