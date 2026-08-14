// Public signing ceremony (/sign/:token). The recipient reviews the document,
// adopts a signature (drawn or typed), fills their fields, consents, and
// finishes. No session — the unguessable token is the access (same trust
// model as share links). Served noindex (system/web).
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import {
  Calendar, Check, CheckSquare, FileSignature, Fingerprint, Loader2, Lock, PenLine, PenTool,
  Square, Type, X,
} from "lucide-react";

import { TrilliLogo } from "@/components/Logo";
import { api } from "@/lib/api";
import { decodeId } from "@/lib/ids";
import { cn } from "@/lib/utils";

interface CeremonyField {
  id: number;
  kind:
    | "signature" | "initials" | "date" | "date_signed" | "title" | "name" | "email"
    | "company" | "text" | "checkbox" | "number" | "dropdown" | "radio" | "note"
    | "approve" | "decline" | "attachment" | "formula";
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
  required: boolean;
  meta?: { options?: string[]; group?: string; formula?: string };
  value?: string; // server-known value (e.g. uploaded attachment filename)
}

// Tiny safe evaluator for Formula fields: numbers, + - * / ( ), and {n}
// referencing the signer's Number fields in placement order. No eval().
function evalFormula(expr: string, refs: number[]): string {
  const src = expr.replace(/\{(\d+)\}/g, (_, n) => String(refs[Number(n) - 1] ?? 0));
  const tokens = src.match(/\d+\.?\d*|[-+*/()]/g);
  if (!tokens || /[^\d\s+\-*/().]/.test(src)) return "";
  let i = 0;
  const parseExpr = (): number => {
    let v = parseTerm();
    while (tokens[i] === "+" || tokens[i] === "-") { const op = tokens[i++]; const r = parseTerm(); v = op === "+" ? v + r : v - r; }
    return v;
  };
  const parseTerm = (): number => {
    let v = parseFactor();
    while (tokens[i] === "*" || tokens[i] === "/") { const op = tokens[i++]; const r = parseFactor(); v = op === "*" ? v * r : v / r; }
    return v;
  };
  const parseFactor = (): number => {
    if (tokens[i] === "(") { i++; const v = parseExpr(); i++; return v; }
    if (tokens[i] === "-") { i++; return -parseFactor(); }
    return parseFloat(tokens[i++] ?? "0");
  };
  try {
    const out = parseExpr();
    if (!Number.isFinite(out)) return "";
    return String(Math.round(out * 100) / 100);
  } catch { return ""; }
}

interface CeremonyView {
  title: string;
  message: string;
  sender_name: string;
  page_count: number;
  recipient_name: string;
  recipient_email: string;
  total_signers: number;
  signed_count: number;
  status: string;
  envelope_status: string;
  signed_at?: string;
  fields: CeremonyField[];
}

const FIELD_LABEL: Record<string, string> = {
  title: "Title", name: "Name", email: "Email", company: "Company", text: "Text",
  number: "Number", note: "Note", dropdown: "Select", formula: "Formula",
};
const todayStr = () =>
  new Date().toLocaleDateString(undefined, { year: "numeric", month: "2-digit", day: "2-digit" });

/* ---------------------------------------------- signature capture modal -- */
function SignatureModal({
  name,
  onAdopt,
  onClose,
}: {
  name: string;
  onAdopt: (kind: "drawn" | "typed", png: string, initialsPng: string) => void;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<"draw" | "type">("type");
  const [typed, setTyped] = useState(name);
  // scale-to-fit for the typed preview: long names shrink so the script never
  // spills out of the preview box (or the modal)
  const typedTextRef = useRef<HTMLSpanElement>(null);
  const [typedScale, setTypedScale] = useState(1);
  useEffect(() => {
    const el = typedTextRef.current;
    const box = el?.parentElement;
    if (!el || !box) return;
    // measure at natural size (transform doesn't affect scrollWidth)
    const fit = () => {
      const avail = box.clientWidth - 8;
      const w = el.scrollWidth;
      setTypedScale(w > avail && w > 0 ? Math.max(0.25, avail / w) : 1);
    };
    fit();
    // refit once the script font finishes loading (metrics change)
    if (document.fonts?.ready) void document.fonts.ready.then(fit);
  }, [typed]);
  const [hasInk, setHasInk] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const drawing = useRef(false);
  const last = useRef<{ x: number; y: number } | null>(null);

  const ctx2d = () => canvasRef.current?.getContext("2d") ?? null;

  const start = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const c = canvasRef.current;
    if (!c) return;
    c.setPointerCapture(e.pointerId);
    drawing.current = true;
    const r = c.getBoundingClientRect();
    last.current = { x: ((e.clientX - r.left) / r.width) * c.width, y: ((e.clientY - r.top) / r.height) * c.height };
  };
  const move = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!drawing.current) return;
    const c = canvasRef.current;
    const g = ctx2d();
    if (!c || !g || !last.current) return;
    const r = c.getBoundingClientRect();
    const p = { x: ((e.clientX - r.left) / r.width) * c.width, y: ((e.clientY - r.top) / r.height) * c.height };
    g.strokeStyle = "#14213B";
    g.lineWidth = 3;
    g.lineCap = "round";
    g.lineJoin = "round";
    g.beginPath();
    g.moveTo(last.current.x, last.current.y);
    g.lineTo(p.x, p.y);
    g.stroke();
    last.current = p;
    setHasInk(true);
  };
  const end = () => {
    drawing.current = false;
    last.current = null;
  };
  const clear = () => {
    const c = canvasRef.current;
    const g = ctx2d();
    if (c && g) g.clearRect(0, 0, c.width, c.height);
    setHasInk(false);
  };

  useEffect(() => {
    // ensure the script font is decoded before the canvas ever renders it
    void document.fonts?.load("92px 'Great Vibes'");
  }, []);

  const adopt = () => {
    if (tab === "draw") {
      const c = canvasRef.current;
      if (!c || !hasInk) return;
      // initials for a drawn signature still come from the name, in script
      onAdopt("drawn", cropToInk(c), makeScriptPNG(initialsOf(typed.trim() || name)));
      return;
    }
    const t = typed.trim();
    if (!t) return;
    onAdopt("typed", makeScriptPNG(t), makeScriptPNG(initialsOf(t)));
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/40 p-4">
      <div className="w-full max-w-lg rounded-2xl border border-border bg-card p-5 shadow-xl">
        <div className="flex items-center justify-between">
          <h2 className="text-[15px] font-semibold text-foreground">Adopt your signature</h2>
          <button type="button" onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-muted">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="mt-3 inline-flex rounded-lg border border-border p-0.5 text-[12.5px] font-semibold">
          <button
            type="button"
            onClick={() => setTab("type")}
            className={cn("rounded-md px-3.5 py-1.5", tab === "type" ? "bg-primary text-white" : "text-muted-foreground")}
          >
            Type
          </button>
          <button
            type="button"
            onClick={() => setTab("draw")}
            className={cn("rounded-md px-3.5 py-1.5", tab === "draw" ? "bg-primary text-white" : "text-muted-foreground")}
          >
            Draw
          </button>
        </div>

        {tab === "draw" ? (
          <>
            <canvas
              ref={canvasRef}
              width={640}
              height={200}
              onPointerDown={start}
              onPointerMove={move}
              onPointerUp={end}
              onPointerLeave={end}
              className="mt-3 h-[184px] w-full cursor-crosshair touch-none rounded-lg border border-dashed border-border bg-white"
            />
            <div className="mt-1.5 flex items-center justify-between">
              <p className="text-[11.5px] text-muted-foreground">Draw your signature above.</p>
              <button type="button" onClick={clear} className="text-[12px] font-medium text-primary hover:underline">
                Clear
              </button>
            </div>
          </>
        ) : (
          <>
            <input
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              placeholder="Your full name"
              className="mt-3 w-full rounded-lg border border-border bg-background px-3 py-2 text-[14px] outline-none focus:ring-2 focus:ring-primary/30"
            />
            <div className="mt-3 flex h-40 items-center justify-center overflow-hidden rounded-lg border border-dashed border-border bg-white">
              <span
                ref={typedTextRef}
                style={{ fontFamily: "'Great Vibes', cursive", transform: `scale(${typedScale})` }}
                className="whitespace-nowrap px-4 text-5xl text-[#14213B]"
              >
                {typed.trim() || "…"}
              </span>
            </div>
          </>
        )}

        <button
          type="button"
          onClick={adopt}
          disabled={tab === "draw" ? !hasInk : !typed.trim()}
          className="mt-4 w-full rounded-lg bg-primary px-4 py-2.5 text-[14px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-50"
        >
          Adopt & sign
        </button>
        <SigningDisclosure />
      </div>
    </div>
  );
}

// initialsOf: the two capital letters — first letters of the first and last
// words of the name ("Demo Signer" -> "DS"; single word -> first letter).
function initialsOf(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "";
  const first = words[0][0] ?? "";
  const last = words.length > 1 ? words[words.length - 1][0] ?? "" : "";
  return (first + last).toUpperCase();
}

// makeScriptPNG renders text in the adopted-signature script (Great Vibes,
// 1.6× vertical stretch, ink-cropped) and returns a PNG data URL.
function makeScriptPNG(text: string): string {
  const c = document.createElement("canvas");
  c.width = 640;
  // generous bottom headroom: stretched script flourishes dive well below
  // the nominal descent, and any ink past the canvas edge is sliced flat
  c.height = 360;
  const g = c.getContext("2d");
  if (!g) return "";
  g.fillStyle = "#0B1526"; // near-black navy — real-ink dark
  g.font = "96px 'Great Vibes', cursive";
  g.textBaseline = "alphabetic";
  const w = g.measureText(text).width;
  const scale = w > 600 ? 600 / w : 1;
  const vScale = scale * 1.6;
  g.setTransform(scale, 0, 0, vScale, 0, 0);
  g.fillText(text, 20 / scale, 200 / vScale);
  g.strokeStyle = "#0B1526";
  g.lineWidth = 1.4 / scale;
  g.strokeText(text, 20 / scale, 200 / vScale);
  return cropToInk(c);
}

// cropToInk trims a signature canvas to its inked bounding box (+ padding) so
// the adopted PNG fills its field instead of floating small inside whitespace.
function cropToInk(c: HTMLCanvasElement): string {
  const g = c.getContext("2d");
  if (!g) return c.toDataURL("image/png");
  const { data } = g.getImageData(0, 0, c.width, c.height);
  let minX = c.width, minY = c.height, maxX = -1, maxY = -1;
  for (let y = 0; y < c.height; y++) {
    for (let x = 0; x < c.width; x++) {
      if (data[(y * c.width + x) * 4 + 3] > 16) {
        if (x < minX) minX = x;
        if (x > maxX) maxX = x;
        if (y < minY) minY = y;
        if (y > maxY) maxY = y;
      }
    }
  }
  if (maxX < 0) return c.toDataURL("image/png");
  const pad = 6;
  minX = Math.max(0, minX - pad); minY = Math.max(0, minY - pad);
  // full pad below too — a tight bottom crop chops descender tails and makes
  // the applied signature read squashed/cut off
  maxX = Math.min(c.width - 1, maxX + pad); maxY = Math.min(c.height - 1, maxY + pad);
  const out = document.createElement("canvas");
  out.width = maxX - minX + 1; out.height = maxY - minY + 1;
  out.getContext("2d")?.drawImage(c, minX, minY, out.width, out.height, 0, 0, out.width, out.height);
  return out.toDataURL("image/png");
}

// Short, human device string from the user agent.
function deviceLabel(ua: string): string {
  const browser = /Edg\//.test(ua) ? "Edge" : /OPR\//.test(ua) ? "Opera"
    : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari"
    : /Firefox\//.test(ua) ? "Firefox" : "Browser";
  const os = /Windows/.test(ua) ? "Windows" : /Mac OS X|Macintosh/.test(ua) ? "macOS"
    : /Android/.test(ua) ? "Android" : /iPhone|iPad|iOS/.test(ua) ? "iOS"
    : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

// The signing disclosure: a sealed, official-feeling notice that the session
// is recorded — the signer sees exactly what will be on the record.
function SigningDisclosure() {
  const [info, setInfo] = useState<{ ip: string; user_agent: string; location: string } | null>(null);
  useEffect(() => {
    api.get<{ ip: string; user_agent: string; location: string }>("/api/sign/echo").then(setInfo).catch(() => setInfo(null));
  }, []);
  const location = info?.location || "\u2014";
  return (
    <div className="mt-4 rounded-xl border border-border bg-muted/40 p-3.5">
      <div className="flex items-start gap-3">
        <span className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full border border-primary/25 bg-primary/10 text-primary shadow-inner">
          <Fingerprint className="h-5 w-5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[11.5px] font-semibold uppercase tracking-wide text-foreground">Signing disclosure</p>
          <p className="mt-1 text-[11.5px] leading-relaxed text-muted-foreground">
            By adopting a signature you agree it is the electronic equivalent of your handwritten
            signature. For verification, this signing session is recorded:
          </p>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[11px]">
            <dt className="font-semibold text-muted-foreground">IP address</dt>
            <dd className="truncate font-mono text-foreground">{info?.ip ?? "\u2014"}</dd>
            <dt className="font-semibold text-muted-foreground">Location</dt>
            <dd className="truncate text-foreground">{location}</dd>
            <dt className="font-semibold text-muted-foreground">Device</dt>
            <dd className="truncate text-foreground">{info ? deviceLabel(info.user_agent) : "\u2014"}</dd>
          </dl>
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------ main page -- */
export default function SignCeremony() {
  const { token, id: previewToken } = useParams();
  const previewID = decodeId(previewToken);
  const preview = previewID != null;
  const [searchParams] = useSearchParams();
  const [view, setView] = useState<CeremonyView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sig, setSig] = useState<{ kind: "drawn" | "typed"; png: string; initials: string } | null>(null);
  const [sigModal, setSigModal] = useState(false);
  // Each signature/initials FIELD is applied individually (DocuSign-style):
  // adopting fills the field that opened the modal; the Next marker then
  // guides the signer to click each remaining one.
  const [applied, setApplied] = useState<Set<number>>(new Set());
  const pendingSigField = useRef<number | null>(null);
  const [values, setValues] = useState<Record<number, string>>({});
  const [editingText, setEditingText] = useState<number | null>(null);
  const [consent, setConsent] = useState(false);
  const [finishing, setFinishing] = useState(false);
  const [done, setDone] = useState(false);
  const [declineOpen, setDeclineOpen] = useState(false);
  const [declineReason, setDeclineReason] = useState("");
  const [declining, setDeclining] = useState(false);
  const [declinedHere, setDeclinedHere] = useState(false);
  const [uploading, setUploading] = useState<number | null>(null);

  useEffect(() => {
    const url = preview
      ? `/api/sign/envelopes/${previewID}/preview${searchParams.get("recipient") ? `?recipient=${searchParams.get("recipient")}` : ""}`
      : `/api/sign/ceremony/${token}`;
    api
      .get<CeremonyView>(url)
      .then((v) => {
        setView(v);
        if (v.status === "signed") setDone(true);
        // prefill autofilled fields for display
        const pre: Record<number, string> = {};
        for (const f of v.fields) {
          if (f.kind === "name") pre[f.id] = v.recipient_name;
          if (f.kind === "date_signed") pre[f.id] = todayStr();
        }
        if (Object.keys(pre).length) setValues((cur) => ({ ...pre, ...cur }));
      })
      .catch(() => setError("This signing link is invalid or has expired."));
  }, [token]);

  const fields = view?.fields ?? [];
  const signable = view?.envelope_status === "sent" && view.status !== "signed" && !done;

  const sigFields = fields.filter((f) => f.kind === "signature" || f.kind === "initials");
  const fieldRefs = useRef<Record<number, HTMLDivElement | null>>({});
  const AUTO_KINDS = ["date", "date_signed", "name", "email", "formula", "attachment", "decline"];

  // formula fields recompute live from this signer's Number fields
  const numberRefs = fields
    .filter((f) => f.kind === "number")
    .sort((a, b) => a.page - b.page || a.y - b.y || a.x - b.x)
    .map((f) => parseFloat((values[f.id] ?? "").replace(/,/g, "")) || 0);
  const formulaValue = (f: CeremonyField) =>
    f.meta?.formula ? evalFormula(f.meta.formula, numberRefs) : "";
  const radioGroupOf = (f: CeremonyField) => f.meta?.group || "default";
  const fieldIncomplete = (f: CeremonyField): boolean => {
    if (!f.required) return false;
    if (f.kind === "signature" || f.kind === "initials") return !applied.has(f.id);
    if (f.kind === "checkbox" || AUTO_KINDS.includes(f.kind)) return false;
    if (f.kind === "radio") {
      return !fields.some((o) => o.kind === "radio" && radioGroupOf(o) === radioGroupOf(f) && values[o.id] === "true");
    }
    if (f.kind === "approve") return values[f.id] !== "approved";
    return !(values[f.id] ?? "").trim();
  };
  const attachmentIncomplete = (f: CeremonyField) =>
    f.kind === "attachment" && f.required && !(values[f.id] ?? f.value ?? "");
  const missing = fields.filter((f) => fieldIncomplete(f) || attachmentIncomplete(f));
  const canFinish = signable && consent && missing.length === 0 && sigFields.every((f) => applied.has(f.id));

  // "Start / Next" navigation: jump to the next field that still needs the
  // signer's attention — on a 50-page document nobody should scroll hunting
  // for the signature line.
  const ordered = [...fields].sort((a, b) => a.page - b.page || a.y - b.y || a.x - b.x);
  const nextField = ordered.find((f) => fieldIncomplete(f) || attachmentIncomplete(f));
  // auto-prefilled kinds (date/name/…) must not flip "Start" to "Next"
  const anyTouched = applied.size > 0 || fields.some((f) => !AUTO_KINDS.includes(f.kind) && f.kind !== "checkbox" && (values[f.id] ?? "").trim());
  const jumpToNext = () => {
    if (!nextField) return;
    fieldRefs.current[nextField.id]?.scrollIntoView({ behavior: "smooth", block: "center" });
  };

  const clickField = useCallback(
    (f: CeremonyField) => {
      if (!signable) return;
      if (f.kind === "signature" || f.kind === "initials") {
        if (!sig) {
          pendingSigField.current = f.id;
          setSigModal(true);
        } else {
          // adopted already — clicking applies (or lifts) the ink on this field
          setApplied((a) => {
            const next = new Set(a);
            if (next.has(f.id)) next.delete(f.id);
            else next.add(f.id);
            return next;
          });
        }
        return;
      }
      if (f.kind === "date_signed" || f.kind === "name" || f.kind === "email") {
        return; // auto-filled, not editable
      }
      if (f.kind === "date") {
        setValues((v) => ({ ...v, [f.id]: v[f.id] ? "" : todayStr() }));
        return;
      }
      if (f.kind === "checkbox") {
        setValues((v) => ({ ...v, [f.id]: v[f.id] === "true" ? "" : "true" }));
        return;
      }
      if (f.kind === "radio") {
        // single choice within the group
        const group = f.meta?.group || "default";
        setValues((v) => {
          const next = { ...v };
          for (const o of fields) {
            if (o.kind === "radio" && (o.meta?.group || "default") === group) next[o.id] = "";
          }
          next[f.id] = "true";
          return next;
        });
        return;
      }
      if (f.kind === "approve") {
        setValues((v) => ({ ...v, [f.id]: v[f.id] === "approved" ? "" : "approved" }));
        return;
      }
      if (f.kind === "decline") {
        setDeclineOpen(true);
        return;
      }
      if (f.kind === "formula" || f.kind === "attachment") {
        return; // formula is computed; attachment uses its file input
      }
      setEditingText(f.id);
    },
    [signable, sig, fields, setApplied],
  );

  const uploadAttachment = async (f: CeremonyField, file: File) => {
    if (preview) {
      setValues((v) => ({ ...v, [f.id]: file.name }));
      return;
    }
    setUploading(f.id);
    setError(null);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const res = await fetch(`/api/sign/ceremony/${token}/fields/${f.id}/attachment`, { method: "POST", body: fd });
      if (!res.ok) throw new Error(await res.text());
      setValues((v) => ({ ...v, [f.id]: file.name }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Upload failed.");
    } finally {
      setUploading(null);
    }
  };

  const decline = async () => {
    if (preview) {
      setDeclinedHere(true);
      setDeclineOpen(false);
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    setDeclining(true);
    setError(null);
    try {
      await api.post(`/api/sign/ceremony/${token}/decline`, { reason: declineReason });
      setDeclinedHere(true);
      setDeclineOpen(false);
      window.scrollTo({ top: 0, behavior: "smooth" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't record the decline.");
    } finally {
      setDeclining(false);
    }
  };

  const finish = async () => {
    if (!canFinish) return;
    if (preview) {
      // dry run: show the terminal state without persisting anything
      setDone(true);
      window.scrollTo({ top: 0, behavior: "smooth" });
      return;
    }
    setFinishing(true);
    setError(null);
    try {
      const payload = {
        consent: true,
        signature_kind: sig?.kind ?? "",
        signature_png: sig?.png ?? "",
        initials_png: sig?.initials ?? "",
        fields: fields.map((f) => ({
          id: f.id,
          value:
            f.kind === "signature" || f.kind === "initials"
              ? "signed"
              : f.kind === "formula"
                ? formulaValue(f)
                : (values[f.id] ?? ""),
        })),
      };
      const v = await api.post<CeremonyView>(`/api/sign/ceremony/${token}/complete`, payload);
      setView(v);
      setDone(true);
      window.scrollTo({ top: 0, behavior: "smooth" });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't complete the signing.");
    } finally {
      setFinishing(false);
    }
  };

  /* ---------- loading / error / terminal states ---------- */
  if (!view) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background">
        {error ? (
          <div className="rounded-2xl border border-border bg-card p-8 text-center shadow-sm">
            <h1 className="text-lg font-semibold text-foreground">Signing link not found</h1>
            <p className="mt-2 text-[13.5px] text-muted-foreground">{error}</p>
          </div>
        ) : (
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        )}
      </div>
    );
  }

  // Desktop preview card lives in the document's right gutter (see <main>);
  // terminal screens + small viewports get the compact pill below.
  const previewBanner = null;
  const previewBannerSm = preview ? (
    <div className="fixed bottom-24 left-1/2 z-40 -translate-x-1/2 xl:hidden">
      <button type="button" onClick={() => window.history.back()}
        className="flex items-center gap-2 rounded-full border border-border bg-card/95 py-1 pl-2 pr-3 text-[12px] font-semibold text-foreground shadow-lg backdrop-blur">
        <span className="rounded-full bg-amber-400 px-2 py-0.5 text-[10px] font-bold uppercase text-[#0A2540]">Preview</span>
        Exit
      </button>
    </div>
  ) : null;

  if (done || declinedHere || view.status === "signed" || view.envelope_status !== "sent") {
    // In preview, "done" simulates completion: context-aware by signer count.
    const completed = view.envelope_status === "completed" || (preview && done && view.total_signers <= 1);
    const declined = declinedHere || view.envelope_status === "declined";
    const signedHere = !declined && (done || view.status === "signed");
    const waitingOn = Math.max(view.total_signers - (preview ? 1 : view.signed_count), 0);
    return (
      <div className="flex min-h-screen flex-col bg-background">
        {previewBanner}
        {previewBannerSm}
        <main className="flex flex-1 items-center justify-center px-5 py-10">
          <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 text-center shadow-sm">
            <div
              className={cn(
                "mx-auto flex h-14 w-14 items-center justify-center rounded-full",
                signedHere ? "bg-success text-white" : "bg-[#0A2540] text-white",
              )}
            >
              {signedHere ? <Check className="h-7 w-7" strokeWidth={3} /> : <PenTool className="h-7 w-7" />}
            </div>
            <h1 className="mt-5 text-lg font-semibold text-foreground">
              {signedHere
                ? completed
                  ? "Document complete"
                  : `You're all set, ${view.recipient_name.split(" ")[0]}!`
                : declined
                  ? "Declined"
                  : "This envelope isn't open for signing"}
            </h1>
            <p className="mt-3 flex items-center justify-center gap-2 rounded-lg border border-border bg-muted/40 px-4 py-3 text-[14px] font-medium text-foreground">
              <FileSignature className="h-4 w-4 flex-shrink-0 text-primary" />
              <span className="truncate">{view.title}</span>
            </p>
            <p className="mt-4 text-[13.5px] leading-relaxed text-muted-foreground">
              {signedHere ? (
                completed ? (
                  <>
                    Every party has signed. A confirmation with the final, sealed copy is being
                    emailed to <span className="font-semibold text-foreground">{view.recipient_email}</span>.
                  </>
                ) : (
                  <>
                    Your signature has been recorded — {waitingOn} of {view.total_signers} signer
                    {view.total_signers === 1 ? "" : "s"} still pending. We&rsquo;ll email you at{" "}
                    <span className="font-semibold text-foreground">{view.recipient_email}</span> when
                    all parties have signed.
                  </>
                )
              ) : declined ? (
                "The decline has been recorded and the sender has been notified. No signature was applied."
              ) : view.envelope_status === "voided" ? (
                "The sender has voided this envelope."
              ) : (
                "This envelope is not currently available to sign."
              )}
            </p>
            {signedHere && completed && !preview && (
              <a
                href={`/api/sign/ceremony/${token}/download`}
                className="mt-5 inline-flex items-center justify-center gap-2 rounded-lg bg-primary px-5 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90"
              >
                Download signed copy
              </a>
            )}
            {preview && (
              <button
                type="button"
                onClick={() => window.history.back()}
                className="mt-5 w-full rounded-lg bg-[#0A2540] px-5 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-[#16375A]"
              >
                Exit preview
              </button>
            )}
          </div>
        </main>
        <footer className="pb-6 text-center text-[11.5px] text-muted-foreground">
          <span className="inline-flex items-center gap-1.5">
            <Lock className="h-3 w-3 text-success" /> Encrypted and auditable — powered by Trilli Sign
          </span>
        </footer>
      </div>
    );
  }

  /* ------------------------------ the ceremony ------------------------------ */
  return (
    <div className="min-h-screen bg-muted/30 pb-28">
      {previewBanner}
      {previewBannerSm}
      {/* header */}
      <header className="sticky top-0 z-40 bg-chrome text-chrome-foreground">
        <div className="mx-auto flex h-14 max-w-5xl items-center gap-3 px-5">
          {/* brand: white Trilli wordmark + cursive "Sign" */}
          <div className="flex flex-shrink-0 items-baseline gap-2">
            <TrilliLogo className="h-6 w-auto translate-y-[3px] text-white" />
            <span style={{ fontFamily: "'Great Vibes', cursive" }} className="text-[25px] leading-none text-white">
              Sign
            </span>
          </div>
          <span className="mx-1 hidden h-7 w-px bg-white/20 sm:block" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-[13.5px] font-semibold text-white">{view.title}</p>
            <p className="truncate text-[11.5px] text-white/60">
              {view.sender_name} requested your signature
            </p>
          </div>
          <span
            className={cn(
              "flex-shrink-0 rounded-full px-3 py-1 text-[11.5px] font-bold",
              missing.length === 0 ? "bg-emerald-400/15 text-emerald-300" : "bg-amber-400/15 text-amber-300",
            )}
          >
            {missing.length === 0
              ? "Ready to finish"
              : `${missing.length} field${missing.length === 1 ? "" : "s"} remaining`}
          </span>
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-5 pt-5">
        {view.message && (
          <p className="mb-4 rounded-xl border border-border bg-card px-4 py-3 text-[13.5px] italic text-muted-foreground shadow-sm">
            “{view.message}” — {view.sender_name}
          </p>
        )}

        <div className="relative">
        {/* Start/Next ribbon — hugs the document's left edge, like DocuSign */}
        {signable && nextField && (
          <div className="absolute bottom-0 left-0 top-6 hidden lg:block">
            <button
              type="button"
              onClick={jumpToNext}
              className="sticky top-24 -translate-x-full rounded-l-lg bg-emerald-700 px-3.5 py-2.5 text-[13px] font-bold text-white shadow-lg transition-colors duration-150 hover:bg-emerald-500"
            >
              {anyTouched ? "Next" : "Start"} →
            </button>
          </div>
        )}
        {/* Preview card — 20px off the document's right edge */}
        {preview && (
          <div className="absolute -right-[228px] bottom-0 top-6 hidden w-52 xl:block">
            <div className="sticky top-24 rounded-xl border border-border bg-card/95 p-3.5 shadow-lg ring-1 ring-black/5 backdrop-blur">
              <span className="rounded-full bg-amber-400 px-2 py-0.5 text-[10.5px] font-bold uppercase tracking-wide text-[#0A2540]">Preview</span>
              <p className="mt-2 text-[12.5px] font-semibold leading-snug text-foreground">
                {view.recipient_name || "Recipient"}&rsquo;s view
              </p>
              <p className="mt-0.5 text-[11.5px] leading-snug text-muted-foreground">Nothing is saved or sent.</p>
              <button type="button" onClick={() => window.history.back()}
                className="mt-2.5 w-full rounded-lg bg-[#0A2540] px-3 py-1.5 text-[12px] font-semibold text-white transition-colors hover:bg-[#16375A]">
                Exit preview
              </button>
            </div>
          </div>
        )}

        {Array.from({ length: view.page_count }, (_, i) => i + 1).map((page) => (
          <div key={page} className="mb-6">
            <p className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
              Page {page} of {view.page_count}
            </p>
            <div className="relative overflow-hidden rounded-lg border border-border bg-white shadow-sm">
              <img
                src={preview ? `/api/sign/envelopes/${previewID}/pages/${page}` : `/api/sign/ceremony/${token}/pages/${page}`}
                alt={`Page ${page}`}
                className="block w-full select-none"
                draggable={false}
              />
              {fields
                .filter((f) => f.page === page)
                .map((f) => {
                  const filled =
                    f.kind === "signature" || f.kind === "initials"
                      ? applied.has(f.id)
                      : f.kind === "checkbox"
                        ? values[f.id] === "true"
                        : !!(values[f.id] ?? "").trim();
                  return (
                    <div
                      key={f.id}
                      ref={(el) => (fieldRefs.current[f.id] = el)}
                      onClick={() => clickField(f)}
                      className={cn(
                        "absolute flex cursor-pointer rounded text-[12px] font-semibold transition-shadow",
                        // adopted signatures may overflow the line box a little
                        // (like real ink) — everything else clips to the field
                        !(filled && (f.kind === "signature" || f.kind === "initials")) && "overflow-hidden",
                        filled
                          ? "items-end justify-start border border-transparent bg-transparent hover:border-success/50"
                          : "items-center justify-center border-2 border-primary bg-primary/10 text-primary hover:shadow-md",
                        filled && (f.kind === "signature" || f.kind === "initials") && "justify-center",
                        filled && (f.kind === "checkbox" || f.kind === "radio") && "items-center justify-center",
                        signable && !filled && "animate-pulse-slow",
                      )}
                      style={{
                        left: `${f.x * 100}%`, top: `${f.y * 100}%`,
                        width: `${f.w * 100}%`, height: `${f.h * 100}%`,
                      }}
                    >
                      {f.kind === "signature" || f.kind === "initials" ? (
                        sig && applied.has(f.id) ? (
                          <img
                            src={f.kind === "initials" ? sig.initials || sig.png : sig.png}
                            alt={f.kind === "initials" ? "initials" : "signature"}
                            className="w-auto max-w-full translate-y-[13%] self-end object-contain object-bottom"
                            style={{ height: "145%" }}
                          />
                        ) : (
                          <span className="flex items-center gap-1">
                            <PenLine className="h-3.5 w-3.5" /> {f.kind === "initials" ? "Initial" : "Sign here"}
                          </span>
                        )
                      ) : f.kind === "date" || f.kind === "date_signed" ? (
                        values[f.id] ? (
                          <span className="truncate px-1 pb-[1px] leading-none text-foreground">{values[f.id]}</span>
                        ) : (
                          <span className="flex items-center gap-1">
                            <Calendar className="h-3.5 w-3.5" /> Date
                          </span>
                        )
                      ) : f.kind === "name" || f.kind === "email" ? (
                        <span className="truncate px-1 pb-[1px] leading-none text-foreground">{values[f.id] || FIELD_LABEL[f.kind]}</span>
                      ) : f.kind === "checkbox" ? (
                        values[f.id] === "true" ? (
                          <CheckSquare className="h-4 w-4 text-success" />
                        ) : (
                          <Square className="h-4 w-4" />
                        )
                      ) : f.kind === "radio" ? (
                        <span className={cn("flex h-4 w-4 items-center justify-center rounded-full border-2",
                          values[f.id] === "true" ? "border-success" : "border-current")}>
                          {values[f.id] === "true" && <span className="h-2 w-2 rounded-full bg-success" />}
                        </span>
                      ) : f.kind === "dropdown" ? (
                        <select
                          value={values[f.id] ?? ""}
                          onChange={(e) => setValues((v) => ({ ...v, [f.id]: e.target.value }))}
                          onClick={(e) => e.stopPropagation()}
                          disabled={!signable}
                          className="h-full w-full cursor-pointer bg-transparent px-1 text-[12px] text-foreground outline-none"
                        >
                          <option value="">Select…</option>
                          {(f.meta?.options ?? []).map((o) => (
                            <option key={o} value={o}>{o}</option>
                          ))}
                        </select>
                      ) : f.kind === "approve" ? (
                        <span className="flex items-center gap-1 font-semibold">
                          {values[f.id] === "approved" ? <><Check className="h-4 w-4 text-success" /> Approved</> : "Approve"}
                        </span>
                      ) : f.kind === "decline" ? (
                        <span className="flex items-center gap-1 font-semibold text-destructive">
                          <X className="h-3.5 w-3.5" /> Decline
                        </span>
                      ) : f.kind === "formula" ? (
                        <span className="truncate px-1 text-foreground">{formulaValue(f) || "—"}</span>
                      ) : f.kind === "attachment" ? (
                        <label onClick={(e) => e.stopPropagation()}
                          className="flex h-full w-full cursor-pointer items-center gap-1 truncate px-1 text-[11.5px]">
                          {uploading === f.id ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                          ) : (values[f.id] ?? f.value) ? (
                            <span className="truncate text-foreground">📎 {values[f.id] ?? f.value}</span>
                          ) : (
                            <span className="font-semibold">📎 Upload file</span>
                          )}
                          <input type="file" className="hidden" disabled={!signable}
                            onChange={(e) => { const file = e.target.files?.[0]; if (file) void uploadAttachment(f, file); }} />
                        </label>
                      ) : f.kind === "note" && editingText === f.id ? (
                        <textarea
                          autoFocus
                          value={values[f.id] ?? ""}
                          onChange={(e) => setValues((v) => ({ ...v, [f.id]: e.target.value }))}
                          onBlur={() => setEditingText(null)}
                          onClick={(e) => e.stopPropagation()}
                          className="h-full w-full resize-none bg-transparent px-1 py-0.5 text-[11.5px] leading-snug text-foreground outline-none"
                        />
                      ) : f.kind === "note" && values[f.id] ? (
                        <span className="h-full w-full overflow-hidden whitespace-pre-wrap px-1 py-0.5 text-left text-[11.5px] leading-snug text-foreground">{values[f.id]}</span>
                      ) : editingText === f.id ? (
                        <input
                          autoFocus
                          inputMode={f.kind === "number" ? "decimal" : undefined}
                          value={values[f.id] ?? ""}
                          onChange={(e) => {
                            const val = f.kind === "number" ? e.target.value.replace(/[^0-9.,-]/g, "") : e.target.value;
                            setValues((v) => ({ ...v, [f.id]: val }));
                          }}
                          onBlur={() => setEditingText(null)}
                          onKeyDown={(e) => e.key === "Enter" && setEditingText(null)}
                          onClick={(e) => e.stopPropagation()}
                          className="h-full w-full bg-transparent px-1 text-[12px] text-foreground outline-none"
                        />
                      ) : values[f.id] ? (
                        <span className="truncate px-1 pb-[1px] leading-none text-foreground">{values[f.id]}</span>
                      ) : (
                        <span className="flex items-center gap-1">
                          <Type className="h-3.5 w-3.5" /> {FIELD_LABEL[f.kind] ?? "Text"}
                        </span>
                      )}
                    </div>
                  );
                })}
            </div>
          </div>
        ))}

        </div>

        {error && (
          <p className="mb-4 rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-2.5 text-[13px] text-destructive">
            {error}
          </p>
        )}
      </main>

      {/* decline confirmation */}
      {declineOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={() => setDeclineOpen(false)}>
          <div className="w-full max-w-md rounded-2xl bg-card p-6 shadow-2xl" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold text-foreground">Decline to sign?</h2>
            <p className="mt-2 text-[13.5px] leading-relaxed text-muted-foreground">
              This closes the envelope for everyone and notifies {view.sender_name || "the sender"}. It can't be undone.
            </p>
            <textarea
              value={declineReason}
              onChange={(e) => setDeclineReason(e.target.value)}
              rows={3}
              maxLength={500}
              placeholder="Reason (optional)"
              className="mt-3 w-full resize-none rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30"
            />
            <div className="mt-4 flex justify-end gap-2">
              <button type="button" onClick={() => setDeclineOpen(false)}
                className="rounded-lg border border-border bg-card px-4 py-2 text-[13.5px] font-semibold text-foreground hover:bg-muted">Cancel</button>
              <button type="button" onClick={() => void decline()} disabled={declining}
                className="inline-flex items-center gap-2 rounded-lg bg-destructive px-5 py-2 text-[13.5px] font-semibold text-white hover:bg-destructive/90 disabled:opacity-50">
                {declining && <Loader2 className="h-4 w-4 animate-spin" />} Decline
              </button>
            </div>
          </div>
        </div>
      )}

      {/* small screens: the ribbon gutter doesn't fit — fall back to edge tab */}
      {signable && nextField && (
        <button
          type="button"
          onClick={jumpToNext}
          className="fixed left-0 top-1/2 z-40 -translate-y-1/2 rounded-r-lg bg-emerald-700 px-3 py-3 text-[13px] font-bold text-white shadow-lg transition-colors hover:bg-emerald-500 lg:hidden"
        >
          {anyTouched ? "Next" : "Start"} →
        </button>
      )}

      {/* sticky finish bar */}
      <footer className="fixed inset-x-0 bottom-0 z-40 border-t border-border bg-card/95 backdrop-blur">
        <div className="mx-auto flex max-w-5xl flex-col gap-2.5 px-5 py-3.5 sm:flex-row sm:items-center">
          <label className="relative flex flex-1 cursor-pointer items-start gap-2.5 text-[12.5px] leading-snug text-muted-foreground">
            {signable && !consent && missing.length === 0 && (
              <span className="pointer-events-none absolute -top-14 left-0 z-10 animate-nudge">
                <span className="relative block rounded-lg bg-[#0A2540] px-3 py-1.5 text-[12px] font-semibold text-white shadow-lg">
                  Check the box to finish signing
                  <span className="absolute -bottom-1 left-4 h-2.5 w-2.5 rotate-45 bg-[#0A2540]" />
                </span>
              </span>
            )}
            <input
              type="checkbox"
              checked={consent}
              onChange={(e) => setConsent(e.target.checked)}
              className="mt-0.5 h-4 w-4 flex-shrink-0 accent-primary"
            />
            I agree to sign this document electronically and that my electronic signature is
            legally binding, and I consent to Trilli's electronic records &amp; signatures terms.
          </label>
          <button
            type="button"
            onClick={() => void finish()}
            disabled={!canFinish || finishing}
            className="inline-flex items-center justify-center gap-2 rounded-lg bg-primary px-8 py-2.5 text-[14px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            {finishing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" strokeWidth={3} />}
            Finish signing
          </button>
        </div>
      </footer>

      {sigModal && (
        <SignatureModal
          name={view.recipient_name}
          onAdopt={(kind, png, initialsPng) => {
            setSig({ kind, png, initials: initialsPng });
            if (pendingSigField.current != null) {
              const id = pendingSigField.current;
              setApplied((a) => new Set(a).add(id));
              pendingSigField.current = null;
            }
            setSigModal(false);
          }}
          onClose={() => setSigModal(false)}
        />
      )}
    </div>
  );
}
