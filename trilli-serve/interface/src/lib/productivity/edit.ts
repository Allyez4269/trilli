// "Edit" action for Office/text files in the file browser (Files, Starred,
// Recent, Shared). Determines which Trilli Productivity app edits a file and
// builds/ navigates to the editor route with the file id passed via router
// state, so the Editor page opens that file directly (instead of a new doc).
import type { NavigateFunction } from "react-router-dom";

import { productivityAppKeyForFile } from "@/lib/files-meta";

// Editable extensions, grouped by app, for the Open dialog's filter too.
// (Kept here so the "Edit" affordance and the open-from-Trilli filter agree.)
export const EDITABLE_EXTS: Record<"docs" | "sheets" | "slides", string[]> = {
  docs: ["docx", "doc", "odt", "rtf", "txt", "md", "markdown", "log", "rst", "asciidoc"],
  sheets: ["xlsx", "xls", "xlsm", "csv", "tsv", "ods"],
  slides: ["pptx", "ppt", "pps", "ppsx", "odp"],
};

// appKeyForFile is a re-export of the files-meta lookup so callers can import
// the edit affordance and the lookup from one place.
export function appKeyForFile(name?: string): "docs" | "sheets" | "slides" | null {
  return productivityAppKeyForFile(name);
}

// isEditable reports whether a file can be opened in a Trilli Productivity app
// for editing (Word/Excel/PowerPoint + plain text/markdown).
export function isEditable(name?: string): boolean {
  return appKeyForFile(name) !== null;
}

// editFileRoute returns the editor route + state for a file, or null when the
// file isn't editable.
export function editFileRoute(
  fileId: number,
  name?: string,
): { to: string; state: { fileId: number } } | null {
  const appKey = appKeyForFile(name);
  if (!appKey) return null;
  return { to: `/apps/productivity/${appKey}`, state: { fileId } };
}

// editFile navigates to the editor for the given file. No-op (returns false)
// when the file isn't editable — callers gate the menu item on isEditable().
export function editFile(navigate: NavigateFunction, fileId: number, name?: string): boolean {
  const route = editFileRoute(fileId, name);
  if (!route) return false;
  navigate(route.to, { state: route.state });
  return true;
}
