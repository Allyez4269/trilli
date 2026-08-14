import type { DragEvent } from "react";

import { downloadUrl } from "@/lib/api";

// setFileDownloadDrag enables dragging a file OUT of the browser onto the OS
// (desktop / Finder / Explorer) using Chromium's special "DownloadURL"
// DataTransfer entry. On drop, the browser fetches the absolute, same-origin
// download URL — which carries the session cookie — and saves the file locally.
//
// Format expected by Chromium: "<mime>:<filename>:<absolute-url>".
//
// This is additive and safe: Firefox and Safari ignore "DownloadURL", so the
// drag simply does nothing there. It coexists with an internal move drag on the
// same row (e.g. My Files drag-to-folder) — that payload uses its own MIME
// type, so dropping inside the app still moves while dropping on the OS
// downloads. effectAllowed is widened to "copyMove" so both drops are allowed.
export function setFileDownloadDrag(
  e: DragEvent,
  file: { id: number; name: string; content_type?: string },
): void {
  try {
    const mime =
      file.content_type && file.content_type.trim() ? file.content_type : "application/octet-stream";
    const url = `${window.location.origin}${downloadUrl(file.id)}`;
    e.dataTransfer.setData("DownloadURL", `${mime}:${cleanName(file.name)}:${url}`);
    // Allow copy (download to OS) in addition to any internal move semantics.
    e.dataTransfer.effectAllowed = "copyMove";
  } catch {
    // Older/locked-down browsers can throw on setData during dragstart — ignore.
  }
}

// cleanName strips characters that would corrupt the colon-delimited
// DownloadURL value (colons, newlines, control chars) so the filename segment
// can't bleed into the URL segment.
function cleanName(name: string): string {
  return name.replace(/[\r\n:]+/g, "-").trim() || "download";
}

// setFolderDownloadDrag: drag a whole Trilli folder out to the OS. The server
// streams the subtree as a zip (GET /api/folders/{id}/zip), so the drop lands
// as "<Folder>.zip" on the desktop. Chromium-only, same caveats as above.
export function setFolderDownloadDrag(e: DragEvent, folder: { id: number; name: string }): void {
  try {
    const url = `${window.location.origin}/api/folders/${folder.id}/zip`;
    e.dataTransfer.setData("DownloadURL", `application/zip:${cleanName(folder.name)}.zip:${url}`);
    e.dataTransfer.effectAllowed = "copyMove";
  } catch {
    /* setData can throw in locked-down browsers — the drag just stays internal */
  }
}
