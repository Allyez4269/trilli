// Trilli Productivity — the suite's three apps (owner-approved naming).
// TrilliDocs / TrilliSheets / TrilliSlides map onto the Collabora engine's
// writer/calc/impress. Icons reuse the SAME lucide file-type glyphs + colors the
// file browser uses for .docx / .xlsx / .pptx (see lib/files-meta.ts), so the
// app icons match how those files appear everywhere else in Trilli.
import type { FC, SVGProps } from "react";
import { FileChartColumn, FileSpreadsheet, FileText } from "lucide-react";

import { cn } from "@/lib/utils";

export type ProductivityAppKey = "docs" | "sheets" | "slides";
export type EngineApp = "writer" | "calc" | "impress";

// AppFormat is one export format the editor offers for Save As / Download.
// ext is lowercase without a dot ("docx", "doc"). formats[0] is the app's
// native format (no conversion needed); the rest are produced via headless
// LibreOffice on save/download.
export interface AppFormat {
  ext: string;
  label: string;
}

export interface ProductivityApp {
  key: ProductivityAppKey;
  name: string; // "TrilliDocs"
  short: string; // "Docs"
  blurb: string; // one-liner for tiles
  engine: EngineApp;
  color: string; // brand accent (hex) — used for launcher tiles
  Icon: FC<SVGProps<SVGSVGElement>>;
  formats: AppFormat[];
  // Phase-1 only: which sample file on the test WOPI host this app opens.
  // Replaced by a real Trilli file_id in Phase 2.
  sampleWopiId: string;
}

// Same glyph + color the file browser uses for each office file type
// (files-meta.ts: docx→FileText/blue, xlsx→FileSpreadsheet/green,
// pptx→FileChartColumn/orange). Color is baked in so every call site that just
// passes a size class gets the right color automatically.
const DocsIcon: FC<SVGProps<SVGSVGElement>> = ({ className, ...p }) => (
  <FileText className={cn("text-blue-600", className)} {...p} />
);
const SheetsIcon: FC<SVGProps<SVGSVGElement>> = ({ className, ...p }) => (
  <FileSpreadsheet className={cn("text-green-600", className)} {...p} />
);
const SlidesIcon: FC<SVGProps<SVGSVGElement>> = ({ className, ...p }) => (
  <FileChartColumn className={cn("text-orange-600", className)} {...p} />
);

export const PRODUCTIVITY_APPS: ProductivityApp[] = [
  { key: "docs", name: "Trilli Docs", short: "Docs", blurb: "Documents", engine: "writer", color: "#2563eb", Icon: DocsIcon, formats: [{ ext: "docx", label: "DOCX" }, { ext: "doc", label: "DOC" }], sampleWopiId: "word" },
  { key: "sheets", name: "Trilli Sheets", short: "Sheets", blurb: "Spreadsheets", engine: "calc", color: "#16a34a", Icon: SheetsIcon, formats: [{ ext: "xlsx", label: "XLSX" }, { ext: "xls", label: "XLS" }], sampleWopiId: "excel" },
  { key: "slides", name: "Trilli Slides", short: "Slides", blurb: "Presentations", engine: "impress", color: "#ea580c", Icon: SlidesIcon, formats: [{ ext: "pptx", label: "PPTX" }, { ext: "ppt", label: "PPT" }], sampleWopiId: "impress" },
];;

export function appByKey(key: string | undefined): ProductivityApp | undefined {
  return PRODUCTIVITY_APPS.find((a) => a.key === key);
}
