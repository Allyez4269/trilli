// A Google-Drive-style floating drop widget. When the user drags a file from the
// OS anywhere into the browser window, a centered "drop here" card appears. It's
// purely a visual cue (pointer-events-none) — the actual drop is handled by the
// page's own drop zone underneath, so this can be dropped into any page that
// accepts file uploads to get a consistent, app-wide drag affordance.
//
// It reacts ONLY to external file drags (dataTransfer has "Files"), so internal
// drags (e.g. moving files between folders in the Files area) never trigger it.
import { useEffect, useRef, useState } from "react";
import { UploadCloud } from "lucide-react";

export function FileDropOverlay({ label = "Drop files to upload", hint }: { label?: string; hint?: string }) {
  const [active, setActive] = useState(false);
  // Mirror `active` in a ref so the window listeners (bound once) can read the
  // current value without re-subscribing or capturing a stale closure.
  const activeRef = useRef(false);

  useEffect(() => {
    let depth = 0; // nested dragenter/leave fire per element — count to avoid flicker
    let watchdog: number | undefined;
    const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");

    const show = () => {
      if (!activeRef.current) {
        activeRef.current = true;
        setActive(true);
      }
    };
    // Single hide path — resets everything. Guarded so we don't churn state.
    const hide = () => {
      depth = 0;
      if (watchdog) {
        clearTimeout(watchdog);
        watchdog = undefined;
      }
      if (activeRef.current) {
        activeRef.current = false;
        setActive(false);
      }
    };
    // Safety net: while a drag is over the window, `dragover` fires continuously.
    // If those heartbeats STOP without a drop (drag left the window, was cancelled
    // with Escape, or dropped on the desktop — none of which reliably fire
    // drop/dragleave on macOS), force the overlay off so the blur can't get stuck.
    const armWatchdog = () => {
      if (watchdog) clearTimeout(watchdog);
      watchdog = window.setTimeout(hide, 1200);
    };

    const onEnter = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      depth += 1;
      show();
      armWatchdog();
    };
    const onLeave = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) hide();
    };
    // preventDefault so the browser doesn't navigate to a file dropped outside a
    // zone; also the heartbeat that keeps the watchdog from firing mid-drag.
    const onOver = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
      show();
      armWatchdog();
    };
    const onDrop = (e: DragEvent) => {
      if (hasFiles(e)) e.preventDefault();
      hide();
    };
    // A real OS drag suppresses normal mouse events; the first mousemove AFTER the
    // drag ends is a reliable "it's over" signal, so it clears an overlay stranded
    // by a cancelled/aborted drag the instant the user moves the pointer.
    const onMouseMove = () => {
      if (activeRef.current) hide();
    };

    window.addEventListener("dragenter", onEnter);
    window.addEventListener("dragleave", onLeave);
    window.addEventListener("dragover", onOver);
    // capture:true — a child drop zone calling stopPropagation() (to prevent a
    // double-upload) mustn't stop us from clearing the overlay on drop.
    window.addEventListener("drop", onDrop, true);
    window.addEventListener("dragend", hide);
    window.addEventListener("mousemove", onMouseMove);
    return () => {
      window.removeEventListener("dragenter", onEnter);
      window.removeEventListener("dragleave", onLeave);
      window.removeEventListener("dragover", onOver);
      window.removeEventListener("drop", onDrop, true);
      window.removeEventListener("dragend", hide);
      window.removeEventListener("mousemove", onMouseMove);
      if (watchdog) clearTimeout(watchdog);
    };
  }, []);

  if (!active) return null;

  return (
    <div className="pointer-events-none absolute inset-0 z-[200] flex items-end justify-center pb-12">
      {/* soft, lightly-blurred backdrop — keeps the page visible but out of focus */}
      <div className="absolute inset-0 bg-foreground/[0.03] backdrop-blur-sm" />
      <div className="relative flex translate-x-[10px] flex-col items-center gap-3">
        {/* little cloud-up badge, Google-Drive style */}
        <div className="flex h-14 w-14 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg ring-8 ring-primary/15">
          <UploadCloud className="h-7 w-7" />
        </div>
        <div className="flex flex-col items-center rounded-2xl bg-primary px-7 py-3 text-center text-primary-foreground shadow-xl">
          <span className="text-[14px] font-semibold">{label}</span>
          {hint && <span className="text-[11.5px] text-primary-foreground/80">{hint}</span>}
        </div>
      </div>
    </div>
  );
}
