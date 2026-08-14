import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ImagePlus, Loader2, Trash2, X, ZoomIn, ZoomOut } from "lucide-react";

import { api, ApiError, type Identity } from "@/lib/api";
import { cn } from "@/lib/utils";

// Editor geometry. VIEWPORT is the on-screen square the circle is inscribed in;
// OUTPUT is the saved PNG resolution (2× for crispness on retina avatars).
const VIEWPORT = 256;
const OUTPUT = 512;
const MAX_ZOOM = 3;

// AvatarUploadModal: pick an image, then pan + zoom it inside a circular frame
// so the user controls exactly what lands in the avatar. On save we render the
// visible square to a canvas and upload the cropped PNG. Display everywhere
// masks that square to a circle, so what they frame is what they get.
export function AvatarUploadModal({
  open,
  hasAvatar,
  onClose,
  onSaved,
}: {
  open: boolean;
  hasAvatar: boolean;
  onClose: () => void;
  onSaved: (identity: Identity) => void;
}) {
  const [src, setSrc] = useState<string | null>(null);
  const [zoom, setZoom] = useState(1);
  // `center` is the image point (in natural pixels) shown at the frame center.
  // Anchoring on it makes zoom-around-center and edge-clamping straightforward.
  const [center, setCenter] = useState({ x: 0, y: 0 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const imgRef = useRef<HTMLImageElement | null>(null);
  const natural = useRef({ w: 0, h: 0 });
  const baseScale = useRef(1); // cover-fit scale at zoom = 1
  const drag = useRef<{ x: number; y: number } | null>(null);
  const objectUrl = useRef<string | null>(null);

  const reset = useCallback(() => {
    if (objectUrl.current) {
      URL.revokeObjectURL(objectUrl.current);
      objectUrl.current = null;
    }
    setSrc(null);
    setZoom(1);
    setCenter({ x: 0, y: 0 });
    setError(null);
    setBusy(false);
    drag.current = null;
  }, []);

  // Clean up the object URL when unmounting / closing.
  useEffect(() => {
    if (!open) reset();
  }, [open, reset]);

  // Close on Escape.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, busy, onClose]);

  if (!open) return null;

  function scaleTotal() {
    return baseScale.current * zoom;
  }

  // Keep `center` within bounds so the circle is always fully covered.
  function clamp(c: { x: number; y: number }, z = zoom) {
    const s = baseScale.current * z;
    const halfX = VIEWPORT / 2 / s;
    const halfY = VIEWPORT / 2 / s;
    const { w, h } = natural.current;
    return {
      x: Math.min(Math.max(c.x, halfX), Math.max(halfX, w - halfX)),
      y: Math.min(Math.max(c.y, halfY), Math.max(halfY, h - halfY)),
    };
  }

  function pickFile(file: File | undefined) {
    if (!file) return;
    if (!file.type.startsWith("image/")) {
      setError("Please choose an image file.");
      return;
    }
    if (file.size > 4 * 1024 * 1024) {
      setError("Image is too large (max 4 MB).");
      return;
    }
    setError(null);
    if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
    const url = URL.createObjectURL(file);
    objectUrl.current = url;
    setSrc(url);
  }

  function onImageLoad(e: React.SyntheticEvent<HTMLImageElement>) {
    const img = e.currentTarget;
    const w = img.naturalWidth;
    const h = img.naturalHeight;
    natural.current = { w, h };
    baseScale.current = Math.max(VIEWPORT / w, VIEWPORT / h); // cover
    setZoom(1);
    setCenter({ x: w / 2, y: h / 2 }); // start centered
  }

  function onPointerDown(e: React.PointerEvent) {
    if (!src) return;
    (e.target as Element).setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY };
  }
  function onPointerMove(e: React.PointerEvent) {
    if (!drag.current) return;
    const s = scaleTotal();
    const dx = (e.clientX - drag.current.x) / s;
    const dy = (e.clientY - drag.current.y) / s;
    drag.current = { x: e.clientX, y: e.clientY };
    setCenter((c) => clamp({ x: c.x - dx, y: c.y - dy }));
  }
  function onPointerUp() {
    drag.current = null;
  }

  function onZoom(z: number) {
    const nz = Math.min(Math.max(z, 1), MAX_ZOOM);
    setZoom(nz);
    setCenter((c) => clamp(c, nz)); // re-clamp; center anchor keeps it framed
  }

  async function save() {
    const img = imgRef.current;
    if (!img || !src) return;
    setBusy(true);
    setError(null);
    try {
      const s = scaleTotal();
      const size = VIEWPORT / s; // visible square in natural pixels
      const left = center.x - size / 2;
      const top = center.y - size / 2;

      const canvas = document.createElement("canvas");
      canvas.width = OUTPUT;
      canvas.height = OUTPUT;
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("Canvas unavailable");
      ctx.imageSmoothingQuality = "high";
      ctx.drawImage(img, left, top, size, size, 0, 0, OUTPUT, OUTPUT);

      const blob: Blob = await new Promise((resolve, reject) =>
        canvas.toBlob(
          (b) => (b ? resolve(b) : reject(new Error("Could not render image"))),
          "image/png",
        ),
      );

      const fd = new FormData();
      fd.append("avatar", blob, "avatar.png");
      const res = await fetch("/api/me/avatar", {
        method: "POST",
        credentials: "include",
        body: fd,
      });
      if (!res.ok) {
        let msg = "Upload failed";
        try {
          const j = await res.json();
          if (j?.error) msg = j.error;
        } catch {
          /* keep default */
        }
        throw new ApiError(res.status, msg);
      }
      const identity = (await res.json()) as Identity;
      onSaved(identity);
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't save — try again.");
      setBusy(false);
    }
  }

  async function removePhoto() {
    setBusy(true);
    setError(null);
    try {
      const identity = await api.delete<Identity>("/api/me/avatar");
      onSaved(identity);
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't remove — try again.");
      setBusy(false);
    }
  }

  // Derived CSS placement of the <img> inside the frame.
  const s = scaleTotal();
  const dispW = natural.current.w * s;
  const dispH = natural.current.h * s;
  const offX = VIEWPORT / 2 - center.x * s;
  const offY = VIEWPORT / 2 - center.y * s;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div className="w-full max-w-sm overflow-hidden rounded-xl border border-border bg-card shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-foreground">
            {src ? "Position your photo" : "Update photo"}
          </h2>
          <button
            type="button"
            onClick={() => !busy && onClose()}
            className="rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div className="p-4">
          {!src ? (
            <Dropzone onPick={pickFile} />
          ) : (
            <div className="flex flex-col items-center gap-4">
              {/* Circular framing viewport */}
              <div
                className="relative cursor-grab touch-none overflow-hidden rounded-full border border-border bg-muted active:cursor-grabbing"
                style={{ width: VIEWPORT, height: VIEWPORT }}
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
                onPointerCancel={onPointerUp}
              >
                {/* eslint-disable-next-line jsx-a11y/alt-text */}
                <img
                  ref={imgRef}
                  src={src}
                  onLoad={onImageLoad}
                  draggable={false}
                  className="pointer-events-none max-w-none select-none"
                  style={{
                    position: "absolute",
                    left: offX,
                    top: offY,
                    width: dispW,
                    height: dispH,
                  }}
                />
                {/* Subtle inner ring so the crop edge reads as the avatar circle */}
                <div className="pointer-events-none absolute inset-0 rounded-full ring-1 ring-inset ring-black/10" />
              </div>

              {/* Zoom control */}
              <div className="flex w-full items-center gap-2">
                <ZoomOut className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                <input
                  type="range"
                  min={1}
                  max={MAX_ZOOM}
                  step={0.01}
                  value={zoom}
                  onChange={(e) => onZoom(Number(e.target.value))}
                  className="h-1.5 w-full cursor-pointer appearance-none rounded-full bg-muted accent-primary"
                />
                <ZoomIn className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
              </div>

              <p className="text-center text-[11px] text-muted-foreground">
                Drag to reposition · pinch or slide to zoom
              </p>

              <button
                type="button"
                onClick={() => {
                  if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
                  objectUrl.current = null;
                  setSrc(null);
                }}
                className="text-[11px] font-medium text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
              >
                Choose a different photo
              </button>
            </div>
          )}

          {error && (
            <p className="mt-3 text-center text-[11px] text-destructive" role="alert">
              {error}
            </p>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center gap-2 border-t border-border px-4 py-3">
          {hasAvatar && !src && (
            <button
              type="button"
              onClick={removePhoto}
              disabled={busy}
              className="mr-auto inline-flex h-7 items-center gap-1.5 rounded-md border border-destructive/40 bg-background px-3 text-[11px] font-medium text-destructive shadow-sm transition-colors hover:bg-destructive/10 disabled:opacity-50"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Remove photo
            </button>
          )}
          <button
            type="button"
            onClick={() => !busy && onClose()}
            disabled={busy}
            className={cn(
              "inline-flex h-7 min-w-[5.5rem] items-center justify-center rounded-md border border-foreground/30 bg-background px-3 text-[11px] font-medium text-foreground shadow-sm transition-colors hover:bg-muted disabled:opacity-50",
              !hasAvatar || src ? "ml-auto" : "",
            )}
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={save}
            disabled={!src || busy}
            className="inline-flex h-7 min-w-[5.5rem] items-center justify-center gap-1.5 rounded-md bg-primary px-3 text-[11px] font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            {busy && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {busy ? "Saving…" : "Save photo"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

// Dropzone: the initial "no image yet" state — click or drop to pick a file.
function Dropzone({ onPick }: { onPick: (f: File | undefined) => void }) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [over, setOver] = useState(false);
  return (
    <div
      onClick={() => inputRef.current?.click()}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        onPick(e.dataTransfer.files?.[0]);
      }}
      className={cn(
        "flex cursor-pointer flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-6 py-10 text-center transition-colors",
        over ? "border-primary bg-primary/5" : "border-border hover:bg-muted/40",
      )}
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-secondary">
        <ImagePlus className="h-5 w-5 text-foreground/70" />
      </div>
      <div>
        <p className="text-sm font-medium text-foreground">Click to choose a photo</p>
        <p className="text-[11px] text-muted-foreground">or drag an image here · PNG/JPG up to 4 MB</p>
      </div>
      <input
        ref={inputRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(e) => onPick(e.target.files?.[0])}
      />
    </div>
  );
}
