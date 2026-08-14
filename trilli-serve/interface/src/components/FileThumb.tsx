import { useState } from "react";

import { cn } from "@/lib/utils";
import { fileExtension, fileIconColorFor, fileIconFor } from "@/lib/files-meta";

// Extensions the backend can thumbnail (images in-process, PDFs via
// pdftoppm, videos via ffmpeg). Kept in sync with system/thumbs — gating
// client-side means rows with un-thumbable files never even issue a request.
const THUMB_EXTS = new Set([
  "jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff",
  "pdf",
  "mp4", "mov", "m4v", "webm", "mkv", "avi", "mpg", "mpeg",
]);

/**
 * FileThumb is the leading visual for a file row: a real thumbnail for
 * images / PDFs / videos, the colored type icon for everything else (and as
 * the graceful fallback whenever the thumbnail endpoint declines — 204 — or
 * fails). Every row gets the same fixed-size tile so names align regardless
 * of which variant renders.
 *
 * Loading sequence: the type icon is painted IMMEDIATELY (it's a tiny inline
 * SVG, no network), and when a thumbnail IS available it's layered ON TOP and
 * faded in only once its bytes have fully loaded. This means every row in a
 * folder appears together with its icon first; slower thumbnails (PDFs shell
 * out to pdftoppm on a cache miss) never leave an empty tile or cause a late
 * swap — the icon just sits underneath until the thumbnail is ready.
 */
export default function FileThumb({
  id,
  name,
  contentType,
  version,
  className,
}: {
  id: number;
  name: string;
  contentType: string;
  /** Cache-buster that changes with the file's bytes (updated_at). */
  version?: string;
  className?: string;
}) {
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const thumbable = THUMB_EXTS.has(fileExtension(name));

  const Icon = fileIconFor(contentType, name);
  const color = fileIconColorFor(contentType, name);

  return (
    <span
      className={cn(
        "relative flex h-8 w-8 flex-shrink-0 items-center justify-center overflow-hidden rounded-md",
        className,
      )}
    >
      {/* Type icon — always painted first (inline SVG, instant), and kept as
          the visible placeholder until/unless the thumbnail loads on top. */}
      <Icon className={cn("h-5 w-5", color, loaded && !failed && "opacity-0")} />

      {/* Thumbnail layered on top; revealed only once decoded so there's never
          a flash of empty tile. On error we stay on the icon (loaded stays
          false). loading="lazy" keeps off-screen rows cheap. */}
      {thumbable && !failed && (
        <img
          src={`/api/files/${id}/thumb${version ? `?v=${encodeURIComponent(version)}` : ""}`}
          alt=""
          aria-hidden="true"
          loading="lazy"
          decoding="async"
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          className={cn(
            "absolute inset-0 h-full w-full rounded-md bg-muted object-cover ring-1 ring-border transition-opacity duration-150",
            loaded ? "opacity-100" : "opacity-0",
          )}
        />
      )}
    </span>
  );
}
