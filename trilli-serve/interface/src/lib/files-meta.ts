import {
  File as FileIcon,
  FileArchive,
  FileAudio,
  FileChartColumn,
  FileCode,
  FileImage,
  FileJson,
  FileSpreadsheet,
  FileText,
  FileVideo,
} from "lucide-react";

export type LucideIcon = typeof FileIcon;

// fileExtension returns the lowercased extension (without dot) of a filename,
// e.g. "report.PDF" -> "pdf". Returns "" if there is no extension.
export function fileExtension(name?: string): string {
  if (!name) return "";
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : "";
}

// Internal alias kept for the rules below — exported name is fileExtension.
const ext = fileExtension;

const archiveExt = new Set(["zip", "rar", "tar", "gz", "tgz", "7z", "bz2", "xz"]);
const sheetExt = new Set(["xls", "xlsx", "xlsm", "xlsb", "csv", "tsv", "ods", "numbers"]);
const slideExt = new Set(["ppt", "pptx", "pps", "ppsx", "odp", "key"]);
const docExt = new Set(["doc", "docx", "odt", "rtf", "pages"]);
const codeExt = new Set([
  "js",
  "jsx",
  "ts",
  "tsx",
  "mjs",
  "cjs",
  "py",
  "go",
  "rs",
  "c",
  "cpp",
  "cc",
  "h",
  "hpp",
  "java",
  "kt",
  "swift",
  "rb",
  "php",
  "sh",
  "bash",
  "zsh",
  "html",
  "htm",
  "css",
  "scss",
  "sass",
  "less",
  "yml",
  "yaml",
  "toml",
  "xml",
  "sql",
  "ipynb",
]);
const textExt = new Set(["txt", "log", "md", "markdown", "rst", "asciidoc"]);

// productivityAppKeyForFile maps a file to the Trilli Productivity app that
// can edit it, or null when the file isn't an editable Office/text document.
// The extension sets above are the single source of truth for type detection,
// so this stays in sync with the file icons everywhere. Text files (txt/md/log)
// open in Trilli Docs (Writer handles them). Office formats map to their native
// app; CSV opens in Sheets.
export function productivityAppKeyForFile(name?: string): "docs" | "sheets" | "slides" | null {
  const e = ext(name);
  if (docExt.has(e) || textExt.has(e)) return "docs";
  if (sheetExt.has(e)) return "sheets";
  if (slideExt.has(e)) return "slides";
  return null;
}

// fileIconFor returns the most specific Lucide icon for a file. We pair MIME
// type with the filename extension because the browser-supplied content_type
// is unreliable (e.g. CSVs sometimes arrive as text/plain or application/octet-stream).
export function fileIconFor(type: string, name?: string): LucideIcon {
  const t = (type ?? "").toLowerCase();
  const e = ext(name);

  if (t.startsWith("image/")) return FileImage;
  if (t.startsWith("video/")) return FileVideo;
  if (t.startsWith("audio/")) return FileAudio;
  if (/zip|rar|tar|gzip|7z|bzip|compress/.test(t) || archiveExt.has(e)) return FileArchive;
  if (t.includes("pdf") || e === "pdf") return FileText;
  if (/spreadsheetml|excel|csv/.test(t) || sheetExt.has(e)) return FileSpreadsheet;
  if (/presentationml|powerpoint/.test(t) || slideExt.has(e)) return FileChartColumn;
  if (/wordprocessingml|msword/.test(t) || docExt.has(e)) return FileText;
  if (t.includes("json") || e === "json") return FileJson;
  if (codeExt.has(e) || /javascript|typescript|x-python|x-go|x-sh|x-c\+\+|x-java|html|css/.test(t)) return FileCode;
  if (t.startsWith("text/") || textExt.has(e)) return FileText;
  return FileIcon;
}

// fileIconColorFor returns the canonical brand-ish color for each file type:
// PDF red, Word blue, Excel green, PowerPoint orange, JSON yellow, etc.
export function fileIconColorFor(type: string, name?: string): string {
  const t = (type ?? "").toLowerCase();
  const e = ext(name);

  if (t.startsWith("image/")) return "text-pink-500";
  if (t.startsWith("video/")) return "text-rose-500";
  if (t.startsWith("audio/")) return "text-emerald-500";
  if (/zip|rar|tar|gzip|7z|bzip|compress/.test(t) || archiveExt.has(e)) return "text-amber-500";
  if (t.includes("pdf") || e === "pdf") return "text-red-600";
  if (/spreadsheetml|excel|csv/.test(t) || sheetExt.has(e)) return "text-green-600";
  if (/presentationml|powerpoint/.test(t) || slideExt.has(e)) return "text-orange-600";
  if (/wordprocessingml|msword/.test(t) || docExt.has(e)) return "text-blue-600";
  if (t.includes("json") || e === "json") return "text-yellow-600";
  if (codeExt.has(e) || /javascript|typescript|x-python|x-go|x-sh|x-c\+\+|x-java|html|css/.test(t)) return "text-violet-500";
  if (t.startsWith("text/") || textExt.has(e)) return "text-slate-500";
  return "text-muted-foreground";
}

// truncateName shortens a name to maxLen visible chars while preserving the
// file extension. "my_very_long_report.pdf" -> "my_very_long_report…pdf".
// For names without an extension (folders) it just appends an ellipsis.
export function truncateName(name: string, maxLen = 50): string {
  if (name.length <= maxLen) return name;
  const ellipsis = "…";
  const dot = name.lastIndexOf(".");
  // Treat the tail as an extension only when it's short and not at position 0.
  const ext = dot > 0 && name.length - dot <= 10 ? name.slice(dot) : "";
  const headBudget = maxLen - ellipsis.length - ext.length;
  if (headBudget <= 0) {
    return name.slice(0, Math.max(0, maxLen - ellipsis.length)) + ellipsis;
  }
  return name.slice(0, headBudget) + ellipsis + ext;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

// formatDateTime renders a "Date modified"-style stamp: under 1 day it
// returns a relative phrase ("3 mins ago", "5 hours ago"), otherwise the
// absolute "M/D/YYYY h:mm AM/PM" in the user's locale.
export function formatDateTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  const diffMs = Date.now() - d.getTime();
  if (diffMs < 60_000) return "just now";
  const min = Math.floor(diffMs / 60_000);
  if (min < 60) return `${min} ${min === 1 ? "min" : "mins"} ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} ${hr === 1 ? "hour" : "hours"} ago`;
  const date = d.toLocaleDateString("en-US");
  const time = d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
  return `${date} ${time}`;
}

export function formatRelative(iso?: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  const diff = Date.now() - then;
  const min = Math.floor(diff / 60000);
  if (min < 1) return "just now";
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  if (d < 30) return `${d}d ago`;
  return new Date(iso).toLocaleDateString();
}
