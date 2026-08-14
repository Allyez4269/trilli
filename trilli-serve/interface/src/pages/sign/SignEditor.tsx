// Trilli Sign — envelope builder, DocuSign-style two-step wizard:
//   Step 1 "Set Up Envelope": document, recipients, message.
//   Step 2 "Add Fields": drag fields from the palette onto the pages.
// Sent/completed envelopes are read-only (document + audit trail).
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowLeft, Ban, Calculator, Calendar, Check, CheckSquare, ChevronDown, ChevronsUpDown,
  CircleDot, Copy as CopyIcon, Download, FileText, GripVertical, Hash, Loader2, Lock, Mail,
  Minus, MoreVertical, Paperclip, PenLine, Plus, Send, Settings2, StickyNote, Trash2, Type,
  Upload, User, X,
} from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { OpenFromTrilliModal } from "@/components/productivity/OpenFromTrilliModal";
import { useAuth } from "@/contexts/AuthContext";
import { canUseSign } from "@/lib/productivity/access";
import {
  FIELD_DEFAULTS, RECIPIENT_COLORS, muted, recipientColor, signApi, tint,
  type Envelope, type FieldCategory, type FieldKind, type SignEvent, type SignField,
} from "@/lib/sign/api";
import { decodeId, encodeId } from "@/lib/ids";
import { cn } from "@/lib/utils";

const KIND_ICON: Record<string, typeof PenLine> = {
  signature: PenLine, initials: PenLine, date_signed: Calendar, title: Type,
  name: User, email: Mail, company: Type, date: Calendar, text: Type,
  number: Hash, checkbox: CheckSquare, dropdown: ChevronsUpDown, radio: CircleDot,
  approve: Check, decline: Ban, note: StickyNote, attachment: Paperclip, formula: Calculator,
};

const ACTION_LABELS: Record<string, string> = {
  created: "Envelope created", recipient_added: "Recipient added",
  recipient_removed: "Recipient removed", updated: "Envelope updated",
  sent: "Sent for signature", notified: "Recipient notified",
  viewed: "Document viewed", consented: "Consent given", signed: "Signed",
  completed: "Completed", executed: "Document executed", filed: "Signed copy saved to Files",
  sealed: "Cryptographic seal applied", voided: "Voided", declined: "Declined",
  attachment_uploaded: "Attachment uploaded",
};
const prettyAction = (a: string) => ACTION_LABELS[a] ?? a;

// The DocuSign-style palette, grouped.
const PALETTE: { category: FieldCategory; label: string; kinds: FieldKind[] }[] = [
  { category: "signature", label: "Signature fields", kinds: ["signature", "initials", "date_signed", "title"] },
  { category: "contact", label: "Contact fields", kinds: ["name", "email", "company", "title"] },
];

export default function SignEditor() {
  const { id } = useParams();
  const navigate = useNavigate();
  const { identity } = useAuth();
  // Envelope ids travel as opaque tokens (same codec as Files URLs) so the
  // address bar can't be walked with +1.
  const envelopeID = decodeId(id) ?? NaN;

  const [env, setEnv] = useState<Envelope | null>(null);
  const [error, setError] = useState<string | null>(null);
  // The wizard step lives in the URL so a refresh (or back button) keeps you
  // exactly where you were instead of dumping you at the setup stage.
  const [searchParams, setSearchParams] = useSearchParams();
  const step: 1 | 2 = searchParams.get("step") === "fields" ? 2 : 1;
  const setStep = (n: 1 | 2) => setSearchParams(n === 2 ? { step: "fields" } : {});
  const [events, setEvents] = useState<SignEvent[] | null>(null);

  const reload = useCallback(() => {
    if (!Number.isFinite(envelopeID)) return;
    signApi.get(envelopeID).then(setEnv).catch(() => setError("Envelope not found."));
  }, [envelopeID]);
  useEffect(reload, [reload]);
  useEffect(() => {
    if (env && env.status !== "draft") {
      signApi.events(env.id).then((r) => setEvents(r.events)).catch(() => setEvents([]));
    }
  }, [env?.id, env?.status]);

  if (!canUseSign(identity?.user?.email)) {
    navigate("/apps/sign", { replace: true });
    return null;
  }
  if (!env) {
    return (
      <div className="flex flex-1 items-center justify-center">
        {error ? <p className="text-[13px] text-destructive">{error}</p> : <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />}
      </div>
    );
  }

  const editable = env.status === "draft";
  if (!editable) return <ReadOnlyEnvelope env={env} events={events} />;

  return step === 1 ? (
    <SetupStep env={env} reload={reload} onNext={() => setStep(2)} setError={setError} error={error} />
  ) : (
    <FieldsStep env={env} reload={reload} onBack={() => setStep(1)} setError={setError} error={error} />
  );
}

/* ============================ Step 1 — Set Up Envelope ============================ */
const ENVELOPE_CATEGORIES = [
  "Employment Contracts",
  "Franchise Agreements",
  "Independent Contractor Agreements",
  "Intellectual Property Assignment Agreements",
  "Investment Account Agreements",
  "Joint Venture Agreements",
  "Lease Agreements",
  "Licensing Agreements",
  "Loan Agreements",
  "Loan Applications",
  "Non-Disclosure Agreements (NDAs)",
  "Onboarding Agreements",
  "Other",
  "Partnership Agreements",
  "Purchase Agreements",
  "Sales Contracts",
  "Service Agreements",
  "Software License Agreements",
  "Vendor Agreements",
  "Wealth Management Agreements",
];

function SetupStep({
  env, reload, onNext, setError, error,
}: {
  env: Envelope; reload: () => void; onNext: () => void;
  setError: (s: string | null) => void; error: string | null;
}) {
  const recipients = env.recipients ?? [];
  const navigate = useNavigate();
  // The default subject inherits the agreement name. It must FOLLOW the title
  // when a document is attached later (blank drafts start as "Untitled
  // envelope") — only a subject the user actually typed is left alone.
  const autoSubject = (t: string) => `Please sign: ${t}`;
  const [subject, setSubject] = useState(env.subject || autoSubject(env.title));
  const [subjectEdited, setSubjectEdited] = useState(
    !!env.subject && !env.subject.startsWith("Please sign: "),
  );
  useEffect(() => {
    if (!subjectEdited) setSubject(autoSubject(env.title));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [env.title]);
  const [message, setMessage] = useState(env.message);
  const [category, setCategory] = useState(env.category ?? "");
  const [docPickerOpen, setDocPickerOpen] = useState(false);
  const [attaching, setAttaching] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const [docMenu, setDocMenu] = useState(false);
  const dragDepth = useRef(0); // dragenter/leave nesting counter for the page-wide highlight
  const hasDocument = env.page_count > 0;

  // sections collapse independently (DocuSign-style accordion), all open by default
  const [open, setOpen] = useState({ documents: true, recipients: true, message: true });
  const toggle = (k: keyof typeof open) => setOpen((o) => ({ ...o, [k]: !o[k] }));

  const attachDoc = async (fileId: number) => {
    setAttaching(true);
    setError(null);
    try {
      await signApi.attachDocument(env.id, fileId);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't attach the document.");
    } finally {
      setAttaching(false);
      setDocPickerOpen(false);
    }
  };

  const removeDoc = async () => {
    setError(null);
    try {
      await signApi.removeDocument(env.id);
      reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't remove the document.");
    }
  };

  // Window-level drag detection: any file drag over the WHOLE screen lights
  // the drop zone, and a drop anywhere uploads + attaches. The counter absorbs
  // child enter/leave churn; dragover preventDefault also stops the browser
  // from navigating to the dropped PDF.
  useEffect(() => {
    const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files");
    const enter = (e: DragEvent) => {
      if (!hasFiles(e)) return;
      dragDepth.current += 1;
      setDragOver(true);
    };
    const leave = () => {
      dragDepth.current = Math.max(0, dragDepth.current - 1);
      if (dragDepth.current === 0) setDragOver(false);
    };
    const over = (e: DragEvent) => {
      if (hasFiles(e)) e.preventDefault();
    };
    const drop = (e: DragEvent) => {
      dragDepth.current = 0;
      setDragOver(false);
      if (!hasFiles(e)) return;
      e.preventDefault();
      void handleDroppedFiles(e.dataTransfer?.files ?? null);
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragleave", leave);
    window.addEventListener("dragover", over);
    window.addEventListener("drop", drop);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("dragover", over);
      window.removeEventListener("drop", drop);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [env.id]);

  const SIGN_SOURCE_EXTS = [".pdf", ".docx", ".doc", ".odt", ".rtf"];
  const handleDroppedFiles = async (files: FileList | null) => {
    const file = files
      ? Array.from(files).find((f) => SIGN_SOURCE_EXTS.some((x) => f.name.toLowerCase().endsWith(x)))
      : undefined;
    if (!file) {
      setError("Drop a PDF or Word document to add it to this envelope.");
      return;
    }
    setAttaching(true);
    setError(null);
    try {
      await signApi.uploadDocument(env.id, file);
      reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't upload the document.");
    } finally {
      setAttaching(false);
    }
  };

  const [rows, setRows] = useState<{ name: string; email: string }[]>(
    recipients.length ? recipients.map((r) => ({ name: r.name, email: r.email })) : [{ name: "", email: "" }],
  );
  const setRow = (i: number, patch: Partial<{ name: string; email: string }>) =>
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const addRow = () => setRows((rs) => [...rs, { name: "", email: "" }]);
  const delRow = (i: number) => setRows((rs) => (rs.length > 1 ? rs.filter((_, j) => j !== i) : rs));
  const validRows = rows.filter((r) => r.name.trim() && r.email.includes("@"));

  const saveAndNext = async () => {
    setError(null);
    try {
      await signApi.patch(env.id, { subject, message, category });
      const key = (n: string, e: string) => `${n.trim().toLowerCase()}|${e.trim().toLowerCase()}`;
      const desiredKeys = new Set(validRows.map((r) => key(r.name, r.email)));
      const existingKeys = new Set(recipients.map((r) => key(r.name, r.email)));
      for (const r of recipients) {
        if (!desiredKeys.has(key(r.name, r.email))) await signApi.removeRecipient(env.id, r.id);
      }
      for (const r of validRows) {
        if (!existingKeys.has(key(r.name, r.email))) await signApi.addRecipient(env.id, { name: r.name.trim(), email: r.email.trim() });
      }
      reload();
      onNext();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't save the envelope.");
    }
  };

  // No document -> the envelope record shouldn't exist. Exiting setup on a
  // blank draft discards it (the server also hides/GCs strays as backstop).
  const closeSetup = async () => {
    if (!hasDocument) {
      try { await signApi.remove(env.id); } catch { /* backstop GC covers it */ }
    }
    navigate("/apps/sign");
  };

  return (
    <WizardShell
      title="Set Up Envelope"
      onClose={() => void closeSetup()}
      action={
        <button
          type="button"
          onClick={() => void saveAndNext()}
          disabled={validRows.length === 0 || !hasDocument}
          title={!hasDocument ? "Add a document first" : validRows.length === 0 ? "Add at least one recipient" : "Next"}
          className="inline-flex items-center gap-2 rounded-lg bg-primary px-5 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-50"
        >
          Next: Add Fields <ArrowLeft className="h-4 w-4 rotate-180" />
        </button>
      }
    >
      {/* drag detection is WINDOW-level (see the useEffect above), so the zone
          lights up no matter where on the screen the file is dragged */}
      <div className="relative mx-auto w-full max-w-7xl space-y-6 px-6 py-8">
        {error && <p className="rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-2.5 text-[13px] text-destructive">{error}</p>}

        {/* ---- Add documents ---- */}
        <AccordionSection title="Add documents" open={open.documents} onToggle={() => toggle("documents")}>
          <div className="flex flex-col gap-4 sm:flex-row">
            {/* document preview card, once attached */}
            {hasDocument && (
              <div className="relative flex w-full flex-shrink-0 flex-col rounded-xl border border-border bg-card shadow-sm sm:w-56">
                <div className="relative flex h-52 items-start justify-center overflow-hidden rounded-t-xl bg-muted/40 p-2">
                  <img
                    src={signApi.pageURL(env.id, 1)}
                    alt="Document preview"
                    className="max-h-full w-auto rounded-sm bg-white shadow ring-1 ring-black/10"
                  />
                </div>
                <div className="flex items-center gap-1 rounded-b-xl border-t border-border px-3 py-2.5">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[12.5px] font-semibold text-foreground">{env.title}</p>
                    <p className="text-[11px] text-muted-foreground">{env.page_count} page{env.page_count === 1 ? "" : "s"}</p>
                  </div>
                  <div className="relative flex-shrink-0">
                    <button type="button" onClick={() => setDocMenu((v) => !v)}
                      className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
                      <MoreVertical className="h-4 w-4" />
                    </button>
                    {docMenu && (
                      <>
                        {/* click-away backdrop */}
                        <div className="fixed inset-0 z-40" onClick={() => setDocMenu(false)} />
                        {/* drops BELOW the card, right edge aligned to the ⋮ */}
                        <div className="absolute right-0 top-full z-50 mt-2 w-48 overflow-hidden rounded-xl border border-border bg-card py-1.5 shadow-xl ring-1 ring-black/5">
                          <button type="button" onClick={() => { setDocMenu(false); setDocPickerOpen(true); }}
                            className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left text-[13px] font-medium text-foreground transition-colors hover:bg-muted">
                            <Upload className="h-4 w-4 text-muted-foreground" /> Replace document
                          </button>
                          <a href={signApi.pageURL(env.id, 1)} target="_blank" rel="noreferrer"
                            onClick={() => setDocMenu(false)}
                            className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left text-[13px] font-medium text-foreground transition-colors hover:bg-muted">
                            <FileText className="h-4 w-4 text-muted-foreground" /> Preview
                          </a>
                          <div className="mx-3 my-1 border-t border-border" />
                          <button type="button" onClick={() => { setDocMenu(false); void removeDoc(); }}
                            className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left text-[13px] font-medium text-destructive transition-colors hover:bg-destructive/10">
                            <Trash2 className="h-4 w-4" /> Remove document
                          </button>
                        </div>
                      </>
                    )}
                  </div>
                </div>
              </div>
            )}

            {/* drop zone */}
            <div
              onDragOver={(e) => e.preventDefault()}
              className={cn(
                "flex min-h-[15rem] flex-1 flex-col items-center justify-center gap-2.5 rounded-xl border-2 border-dashed px-4 py-12 text-center transition-colors",
                dragOver ? "border-primary bg-primary/5 ring-4 ring-primary/15" : "border-border bg-muted/30",
              )}
            >
              <span className="flex h-11 w-11 items-center justify-center rounded-lg bg-card text-muted-foreground shadow-sm ring-1 ring-border">
                {attaching ? <Loader2 className="h-5 w-5 animate-spin" /> : <Upload className="h-5 w-5" />}
              </span>
              <p className="text-[13.5px] text-foreground">
                Drop your PDF or Word document here or
              </p>
              <button
                type="button"
                onClick={() => setDocPickerOpen(true)}
                disabled={attaching}
                className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-[13px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-60"
              >
                <FileText className="h-4 w-4" /> Choose from Trilli
              </button>
            </div>
          </div>
        </AccordionSection>

        {/* ---- Add recipients ---- */}
        <AccordionSection title="Add recipients" open={open.recipients} onToggle={() => toggle("recipients")}>
          <div className="space-y-3">
            {rows.map((r, i) => {
              const color = recipientColor(rows.map((x, j) => ({ id: j } as any)), i);
              return (
                <div key={i} className="relative rounded-xl border border-border bg-card p-4 pl-5 shadow-sm">
                  <span className="absolute inset-y-0 left-0 w-1.5 rounded-l-xl" style={{ backgroundColor: color }} />
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div>
                      <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Name <span className="text-destructive">*</span></label>
                      <input value={r.name} onChange={(e) => setRow(i, { name: e.target.value })} placeholder="Full name"
                        className="w-full rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30" />
                    </div>
                    <div>
                      <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Email <span className="text-destructive">*</span></label>
                      <input value={r.email} onChange={(e) => setRow(i, { email: e.target.value })} placeholder="email@example.com" type="email"
                        className="w-full rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30" />
                    </div>
                  </div>
                  {rows.length > 1 && (
                    <button type="button" onClick={() => delRow(i)}
                      className="absolute right-3 top-3 rounded p-1 text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
                      <Trash2 className="h-4 w-4" />
                    </button>
                  )}
                </div>
              );
            })}
            <button type="button" onClick={addRow}
              className="inline-flex items-center gap-2 rounded-lg border border-border bg-card px-4 py-2 text-[13px] font-semibold text-foreground hover:bg-muted">
              <Plus className="h-4 w-4" /> Add Recipient
            </button>
          </div>
        </AccordionSection>

        {/* ---- Add message ---- */}
        <AccordionSection title="Add message" open={open.message} onToggle={() => toggle("message")}>
          <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Email subject</label>
          <input value={subject} onChange={(e) => { setSubject(e.target.value); setSubjectEdited(true); }} maxLength={200}
            className="mb-4 w-full rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30" />
          <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Message</label>
          <textarea value={message} onChange={(e) => setMessage(e.target.value)} rows={4} maxLength={2000} placeholder="Enter a message for all recipients…"
            className="w-full resize-none rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30" />
        </AccordionSection>

        {/* ---- Category (optional; indexed on the dashboard) ---- */}
        <div>
          <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">
            Category <span className="font-normal text-muted-foreground/70">(optional)</span>
          </label>
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            className="w-full rounded-lg border border-border bg-background px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30"
          >
            <option value="">— Select —</option>
            {ENVELOPE_CATEGORIES.map((c) => (
              <option key={c} value={c}>{c}</option>
            ))}
          </select>
          <p className="mt-1 text-[11px] text-muted-foreground">Shown on your Trilli Sign dashboard to organize envelopes.</p>
        </div>
      </div>
      {docPickerOpen && (
        <OpenFromTrilliModal
          extensions={["pdf", "docx", "doc"]}
          onClose={() => setDocPickerOpen(false)}
          onOpen={async (f) => {
            await attachDoc(f.id);
          }}
        />
      )}
    </WizardShell>
  );
}

// AccordionSection — a collapsible titled block with a chevron toggle.
function AccordionSection({
  title, open, onToggle, children,
}: {
  title: string; open: boolean; onToggle: () => void; children: React.ReactNode;
}) {
  return (
    <section className="border-b border-border pb-6">
      <button type="button" onClick={onToggle} className="flex w-full items-center justify-between py-1 text-left">
        <h2 className="text-[15px] font-semibold text-foreground">{title}</h2>
        <ChevronDown className={cn("h-5 w-5 text-muted-foreground transition-transform", !open && "-rotate-90")} />
      </button>
      {open && <div className="mt-4">{children}</div>}
    </section>
  );
}

/* ============================ Step 2 — Add Fields ============================ */
const ZOOM_STEPS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const PALETTE_GROUPS: { label: string; kinds: FieldKind[] }[] = [
  { label: "Signature", kinds: ["signature", "initials", "date_signed"] },
  { label: "Contact", kinds: ["name", "email", "company", "title"] },
  { label: "Inputs", kinds: ["text", "number", "checkbox", "dropdown", "radio"] },
  { label: "Actions", kinds: ["approve", "decline"] },
  { label: "Other", kinds: ["note", "attachment", "formula"] },
];

// kinds that carry sender configuration (gear in the field toolbar)
const CONFIG_KINDS: Record<string, { key: keyof import("@/lib/sign/api").FieldMeta; label: string; hint: string; multi?: boolean }> = {
  dropdown: { key: "options", label: "Options (one per line)", hint: "The signer picks one of these.", multi: true },
  radio: { key: "group", label: "Radio group", hint: "Radios sharing a group are one single-choice set." },
  formula: { key: "formula", label: "Formula", hint: "Use {1}, {2}… to reference this signer's Number fields in placement order, e.g. {1} * {2} + 10." },
};
type Corner = "nw" | "ne" | "sw" | "se";
const initialsOf = (name: string) =>
  name.split(/\s+/).filter(Boolean).slice(0, 2).map((p) => p[0]?.toUpperCase() ?? "").join("") || "?";

function FieldsStep({
  env, reload, onBack, setError, error,
}: {
  env: Envelope; reload: () => void; onBack: () => void;
  setError: (s: string | null) => void; error: string | null;
}) {
  const recipients = env.recipients ?? [];
  const navigate = useNavigate();
  // Optimistic field state: every mutation lands here INSTANTLY; the API call
  // follows in the background. Server state re-syncs on envelope reloads.
  const [fields, setFields] = useState<SignField[]>(env.fields ?? []);
  useEffect(() => setFields(env.fields ?? []), [env.fields]);
  const [active, setActive] = useState<number | null>(recipients[0]?.id ?? null);
  useEffect(() => {
    if (active == null && recipients[0]) setActive(recipients[0].id);
  }, [recipients, active]);
  const [selected, setSelected] = useState<number | null>(null);
  const [chipMenu, setChipMenu] = useState(false); // field toolbar's recipient menu
  const [gearOpen, setGearOpen] = useState(false); // field toolbar's settings popover
  const [editRecipients, setEditRecipients] = useState(false);
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState(false);
  const [zoom, setZoom] = useState(1);

  const pageRefs = useRef<Record<number, HTMLDivElement | null>>({});
  const dragKind = useRef<FieldKind | null>(null);
  const drag = useRef<null | { id: number; mode: "move" | "resize"; corner?: Corner; rect: DOMRect; ox: number; oy: number; sw: number; sh: number; sx: number; sy: number; moved: boolean }>(null);
  const [ghost, setGhost] = useState<null | { id: number; x: number; y: number; w: number; h: number }>(null);

  const activeRecipient = recipients.find((r) => r.id === active) ?? null;
  const activeColor = activeRecipient ? recipientColor(recipients, activeRecipient.id) : "#94A3B8";


  const drop = async (page: number, e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    const kind = dragKind.current;
    if (!kind || !active) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const d = FIELD_DEFAULTS[kind];
    const x = +Math.min(Math.max((e.clientX - rect.left) / rect.width - d.w / 2, 0), 1 - d.w).toFixed(4);
    const y = +Math.min(Math.max((e.clientY - rect.top) / rect.height - d.h / 2, 0), 1 - d.h).toFixed(4);
    dragKind.current = null;
    const tempID = -Date.now();
    const temp: SignField = { id: tempID, recipient_id: active, kind, page, x, y, w: d.w, h: d.h, required: true };
    setFields((fs) => [...fs, temp]);
    try {
      const created = await signApi.addField(env.id, { recipient_id: active, kind, page, x, y, w: d.w, h: d.h });
      setFields((fs) => fs.map((f) => (f.id === tempID ? { ...temp, id: created.id } : f)));
      setSelected(created.id);
    } catch (err) {
      setFields((fs) => fs.filter((f) => f.id !== tempID));
      setError(err instanceof Error ? err.message : "Couldn't place the field.");
    }
  };

  const startMove = (f: SignField, e: React.PointerEvent) => {
    e.stopPropagation();
    const fieldEl = e.currentTarget as HTMLElement;
    const pageEl = fieldEl.parentElement as HTMLElement;
    const r = fieldEl.getBoundingClientRect();
    fieldEl.setPointerCapture(e.pointerId);
    drag.current = { id: f.id, mode: "move", rect: pageEl.getBoundingClientRect(), ox: (e.clientX - r.left) / r.width, oy: (e.clientY - r.top) / r.height, sw: f.w, sh: f.h, sx: f.x, sy: f.y, moved: false };
  };
  const startResize = (f: SignField, corner: Corner, e: React.PointerEvent) => {
    e.stopPropagation();
    const handleEl = e.currentTarget as HTMLElement;
    const fieldEl = handleEl.parentElement as HTMLElement;
    const pageEl = fieldEl.parentElement as HTMLElement;
    fieldEl.setPointerCapture(e.pointerId);
    drag.current = { id: f.id, mode: "resize", corner, rect: pageEl.getBoundingClientRect(), ox: e.clientX, oy: e.clientY, sw: f.w, sh: f.h, sx: f.x, sy: f.y, moved: false };
  };
  const onFieldMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d) return;
    d.moved = true;
    const rect = d.rect;
    if (d.mode === "move") {
      const x = Math.min(Math.max((e.clientX - rect.left) / rect.width - d.ox * d.sw, 0), 1 - d.sw);
      const y = Math.min(Math.max((e.clientY - rect.top) / rect.height - d.oy * d.sh, 0), 1 - d.sh);
      setGhost({ id: d.id, x, y, w: d.sw, h: d.sh });
      return;
    }
    const dx = (e.clientX - d.ox) / rect.width;
    const dy = (e.clientY - d.oy) / rect.height;
    let { sx: x, sy: y, sw: w, sh: h } = d;
    const c = d.corner ?? "se";
    if (c.includes("e")) w = d.sw + dx;
    if (c.includes("s")) h = d.sh + dy;
    if (c.includes("w")) { x = d.sx + dx; w = d.sw - dx; }
    if (c.includes("n")) { y = d.sy + dy; h = d.sh - dy; }
    // clamp: sane min, bounded max (nobody needs a half-page signature), on-page
    const MINW = 0.03, MINH = 0.015, MAXW = 0.45, MAXH = 0.12;
    if (w < MINW) { if (c.includes("w")) x -= MINW - w; w = MINW; }
    if (h < MINH) { if (c.includes("n")) y -= MINH - h; h = MINH; }
    if (w > MAXW) { if (c.includes("w")) x = d.sx + d.sw - MAXW; w = MAXW; }
    if (h > MAXH) { if (c.includes("n")) y = d.sy + d.sh - MAXH; h = MAXH; }
    x = Math.min(Math.max(x, 0), 1 - MINW);
    y = Math.min(Math.max(y, 0), 1 - MINH);
    w = Math.min(w, 1 - x);
    h = Math.min(h, 1 - y);
    setGhost({ id: d.id, x, y, w, h });
  };
  const onFieldUp = () => {
    const d = drag.current, g = ghost;
    drag.current = null;
    if (!d) return;
    if (!d.moved || !g) { setGhost(null); return; } // plain click -> selection handled by onClick
    const patch = { x: +g.x.toFixed(4), y: +g.y.toFixed(4), w: +g.w.toFixed(4), h: +g.h.toFixed(4) };
    setFields((fs) => fs.map((f) => (f.id === d.id ? { ...f, ...patch } : f)));
    setGhost(null);
    if (d.id > 0) signApi.patchField(env.id, d.id, patch).catch(() => reload());
  };
  const removeField = (fid: number) => {
    setFields((fs) => fs.filter((f) => f.id !== fid));
    setSelected(null);
    if (fid > 0) signApi.removeField(env.id, fid).catch(() => reload());
  };
  const duplicateField = async (f: SignField) => {
    const x = Math.min(f.x + 0.025, 1 - f.w);
    const y = Math.min(f.y + 0.025, 1 - f.h);
    const tempID = -Date.now();
    setFields((fs) => [...fs, { ...f, id: tempID, x, y }]);
    try {
      const created = await signApi.addField(env.id, { recipient_id: f.recipient_id, kind: f.kind, page: f.page, x: +x.toFixed(4), y: +y.toFixed(4), w: f.w, h: f.h });
      setFields((fs) => fs.map((ff) => (ff.id === tempID ? { ...ff, id: created.id } : ff)));
      setSelected(created.id);
    } catch {
      setFields((fs) => fs.filter((ff) => ff.id !== tempID));
    }
  };
  const setFieldRequired = (f: SignField, required: boolean) => {
    setFields((fs) => fs.map((ff) => (ff.id === f.id ? { ...ff, required } : ff)));
    if (f.id > 0) signApi.patchField(env.id, f.id, { required }).catch(() => reload());
  };
  const setFieldMeta = (f: SignField, meta: import("@/lib/sign/api").FieldMeta) => {
    setFields((fs) => fs.map((ff) => (ff.id === f.id ? { ...ff, meta } : ff)));
    if (f.id > 0) signApi.patchField(env.id, f.id, { meta }).catch(() => reload());
  };
  const reassignField = (f: SignField, rid: number) => {
    setFields((fs) => fs.map((ff) => (ff.id === f.id ? { ...ff, recipient_id: rid } : ff)));
    setChipMenu(false);
    if (f.id > 0) signApi.patchField(env.id, f.id, { recipient_id: rid }).catch(() => reload());
  };

  const send = async () => {
    setSending(true); setError(null);
    // NOTE: no reload() here — refetching flips status to 'sent', which swaps
    // in the read-only envelope view and the success summary only flashes.
    try { await signApi.send(env.id); setSent(true); }
    catch (e) { setError(e instanceof Error ? e.message : "Couldn't send."); }
    finally { setSending(false); }
  };

  // After the summary has had its moment, return to the envelope index.
  useEffect(() => {
    if (!sent) return;
    const t = setTimeout(() => navigate("/apps/sign"), 6000);
    return () => clearTimeout(t);
  }, [sent, navigate]);

  const perRecipient = (rid: number) => fields.filter((f) => f.recipient_id === rid).length;
  const canSend = recipients.length > 0 && recipients.every((r) => perRecipient(r.id) > 0);
  const pageWidth = Math.round(820 * zoom);
  const zoomIdx = ZOOM_STEPS.indexOf(zoom) < 0 ? 2 : ZOOM_STEPS.indexOf(zoom);
  const scrollToPage = (p: number) => pageRefs.current[p]?.scrollIntoView({ behavior: "smooth", block: "start" });

  // Keyboard on the selected field: Delete removes; arrows nudge (Shift = 10×)
  // instead of scrolling the page. Position saves are debounced.
  const nudgeSave = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    const ARROWS: Record<string, [number, number]> = {
      ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1],
    };
    const onKey = (e: KeyboardEvent) => {
      if (selected == null) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
      if (e.key === "Delete" || e.key === "Backspace") {
        e.preventDefault();
        removeField(selected);
        return;
      }
      const dir = ARROWS[e.key];
      if (!dir) return;
      e.preventDefault(); // the page must not scroll while nudging
      const px = e.shiftKey ? 10 : 1.5;
      const dx = (dir[0] * px) / pageWidth;
      const dy = (dir[1] * px) / (pageWidth * 1.294);
      let moved: SignField | undefined;
      setFields((fs) => fs.map((f) => {
        if (f.id !== selected) return f;
        moved = {
          ...f,
          x: Math.min(Math.max(f.x + dx, 0), 1 - f.w),
          y: Math.min(Math.max(f.y + dy, 0), 1 - f.h),
        };
        return moved;
      }));
      if (nudgeSave.current) clearTimeout(nudgeSave.current);
      nudgeSave.current = setTimeout(() => {
        if (moved && moved.id > 0) {
          signApi.patchField(env.id, moved.id, { x: +moved.x.toFixed(4), y: +moved.y.toFixed(4) }).catch(() => reload());
        }
      }, 450);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selected, pageWidth]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <WizardShell
      title="Add Fields"
      onClose={() => navigate("/apps/sign")}
      onBack={onBack}
      action={
        <div className="flex items-center gap-2">
          <button type="button"
            onClick={() => navigate(`/apps/sign/e/${encodeId(env.id)}/preview${active ? `?recipient=${active}` : ""}`)}
            disabled={fields.length === 0}
            title={fields.length === 0 ? "Place at least one field to preview" : "Preview the signing experience"}
            className="inline-flex items-center rounded-md border border-border bg-card px-4 py-1.5 text-[13.5px] font-semibold text-foreground transition-colors hover:bg-muted disabled:opacity-50">
            Preview
          </button>
          <button type="button" onClick={() => void send()} disabled={!canSend || sending}
            title={!canSend ? "Each recipient needs at least one field" : "Send"}
            className="inline-flex items-center gap-2 rounded-md bg-primary px-5 py-1.5 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-50">
            {sending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />} Send
          </button>
        </div>
      }
    >
      {sent ? (
        <div className="flex flex-1 items-center justify-center p-8">
          <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 text-center shadow-sm">
            <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-success text-white"><Send className="h-6 w-6" /></div>
            <h2 className="mt-4 text-lg font-semibold text-foreground">Sent for signature</h2>
            <p className="mt-1 text-[13px] text-muted-foreground">{env.title}</p>

            {/* where the process stands, per recipient */}
            <div className="mt-6 space-y-2 text-left">
              {recipients.map((r) => (
                <div key={r.id} className="flex items-center gap-3 rounded-lg border border-border bg-background px-3.5 py-2.5">
                  <span className="h-2.5 w-2.5 flex-shrink-0 rounded-full" style={{ backgroundColor: recipientColor(recipients, r.id) }} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[13px] font-medium text-foreground">{r.name}</p>
                    <p className="truncate text-[11.5px] text-muted-foreground">{r.email}</p>
                  </div>
                  <span className="flex flex-shrink-0 items-center gap-1.5 text-[11.5px] font-medium text-success">
                    <Mail className="h-3.5 w-3.5" /> Link emailed
                  </span>
                </div>
              ))}
            </div>

            <p className="mt-5 text-[12px] text-muted-foreground">
              You'll be notified as each recipient signs. Track progress anytime from your envelopes.
            </p>
            <Link to="/apps/sign" className="mt-4 inline-block rounded-lg bg-primary px-5 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90">
              Back to envelopes
            </Link>
            <p className="mt-2.5 text-[11px] text-muted-foreground/80">Returning to your envelopes automatically…</p>
          </div>
        </div>
      ) : (
        <div className="flex flex-1 overflow-hidden">
          {/* left rail: recipient dropdown + palette (tinted to the active recipient) */}
          <aside className="w-80 flex-shrink-0 overflow-y-auto border-r border-border bg-card px-4 py-4">
            <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Recipient</p>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button type="button"
                  className="mt-2 flex w-full items-center gap-2 rounded-lg border border-border bg-background px-2.5 py-1.5 text-left outline-none hover:bg-muted focus:outline-none focus-visible:outline-none">
                  <span className="h-2.5 w-2.5 flex-shrink-0 rounded-full" style={{ backgroundColor: activeColor }} />
                  <span className="min-w-0 flex-1 truncate text-[13px] font-medium text-foreground">
                    {activeRecipient?.name ?? "Select recipient"}
                    {activeRecipient && (
                      <span className="ml-1.5 text-[11px] font-normal text-muted-foreground">· {perRecipient(activeRecipient.id)} field{perRecipient(activeRecipient.id) === 1 ? "" : "s"}</span>
                    )}
                  </span>
                  <ChevronDown className="h-4 w-4 flex-shrink-0 text-muted-foreground" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-56 p-1">
                {recipients.map((r) => (
                  <DropdownMenuItem key={r.id} onSelect={() => setActive(r.id)} className="flex items-center gap-2.5">
                    <span className="h-3 w-3 flex-shrink-0 rounded-full" style={{ backgroundColor: recipientColor(recipients, r.id) }} />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[13px]">{r.name}</span>
                      <span className="block truncate text-[11px] opacity-70">{r.email}</span>
                    </span>
                    {r.id === active && <Check className="h-3.5 w-3.5 flex-shrink-0 text-primary" />}
                  </DropdownMenuItem>
                ))}
                <DropdownMenuItem onSelect={() => setEditRecipients(true)} className="flex items-center gap-2 border-t border-border text-primary">
                  <Plus className="h-3.5 w-3.5" /> Edit recipients…
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <p className="mt-5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Drag fields onto the page</p>
            {PALETTE_GROUPS.map((grp) => (
              <div key={grp.label} className="mt-4">
                <p className="mb-2 text-[12.5px] font-bold text-foreground">{grp.label}</p>
                <div className="grid grid-cols-2 gap-1.5">
                  {grp.kinds.map((k) => {
                    const Icon = KIND_ICON[k];
                    return (
                      <div key={grp.label + k} draggable={!!active}
                        onDragStart={() => (dragKind.current = k)}
                        style={active ? { backgroundColor: tint(activeColor, 0.98) } : undefined}
                        className={cn("relative flex cursor-grab items-center gap-1.5 rounded-lg border border-border py-2 pl-2.5 pr-5 text-[12.5px] font-medium text-foreground shadow-sm active:cursor-grabbing",
                          active ? "hover:border-foreground/25 hover:shadow" : "cursor-not-allowed bg-background opacity-40")}>
                        <Icon className="h-4 w-4 flex-shrink-0" style={active ? { color: muted(activeColor) } : undefined} />
                        <span className="truncate" style={active ? { color: muted(activeColor) } : undefined}>{FIELD_DEFAULTS[k].label}</span>
                        {/* soft recipient dot: light, but a step deeper than the chip */}
                        {active && (
                          <span className="absolute right-1.5 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full"
                            style={{ backgroundColor: tint(activeColor, 0.62) }} />
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
            {!active && <p className="mt-3 text-[11.5px] text-amber-600">Select a recipient to place their fields.</p>}
          </aside>

          {/* document canvas */}
          <div className="relative flex flex-1 flex-col overflow-hidden bg-muted/40">
            <div className="flex flex-shrink-0 items-center justify-center gap-2 border-b border-border bg-card/70 px-4 py-1.5 backdrop-blur">
              <button type="button" onClick={() => setZoom(ZOOM_STEPS[Math.max(0, zoomIdx - 1)])} disabled={zoomIdx === 0}
                className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-background text-foreground hover:bg-muted disabled:opacity-40"><Minus className="h-4 w-4" /></button>
              <span className="w-14 text-center text-[12.5px] font-semibold tabular-nums text-foreground">{Math.round(zoom * 100)}%</span>
              <button type="button" onClick={() => setZoom(ZOOM_STEPS[Math.min(ZOOM_STEPS.length - 1, zoomIdx + 1)])} disabled={zoomIdx === ZOOM_STEPS.length - 1}
                className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-background text-foreground hover:bg-muted disabled:opacity-40"><Plus className="h-4 w-4" /></button>
            </div>
            <div className="flex-1 overflow-auto px-6 py-6" onClick={() => { setSelected(null); setChipMenu(false); }}>
              {error && <p className="mx-auto mb-4 max-w-3xl rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-2.5 text-[13px] text-destructive">{error}</p>}
              <div className="mx-auto space-y-6" style={{ width: pageWidth }}>
                {Array.from({ length: env.page_count }, (_, i) => i + 1).map((page) => (
                  <div key={page} ref={(el) => (pageRefs.current[page] = el)}>
                    <p className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Page {page} of {env.page_count}</p>
                    <div className="relative rounded-[3px] bg-white shadow-sm outline outline-1 outline-slate-300"
                      onDragOver={(e) => e.preventDefault()} onDrop={(e) => void drop(page, e)}>
                      <img src={signApi.pageURL(env.id, page)} alt={`Page ${page}`} className="block w-full select-none" draggable={false} />
                      {fields.filter((f) => f.page === page).map((f) => {
                        const color = recipientColor(recipients, f.recipient_id);
                        const rec = recipients.find((r) => r.id === f.recipient_id);
                        const pos = ghost?.id === f.id ? ghost : f;
                        const Icon = KIND_ICON[f.kind];
                        const isSel = selected === f.id;
                        return (
                          <div key={f.id}
                            onPointerDown={(e) => startMove(f, e)} onPointerMove={onFieldMove} onPointerUp={onFieldUp}
                            onClick={(e) => { e.stopPropagation(); setSelected(f.id); setChipMenu(false); setGearOpen(false); }}
                            className={cn("group absolute touch-none select-none rounded-md",
                              isSel ? "cursor-move" : "cursor-pointer")}
                            style={{
                              left: `${pos.x * 100}%`, top: `${pos.y * 100}%`, width: `${pos.w * 100}%`, height: `${pos.h * 100}%`,
                              color: muted(color),
                              border: `1px solid ${tint(color, 0.3)}`,
                              backgroundColor: tint(color, 0.94),
                              boxShadow: isSel ? `0 0 0 2.5px ${tint(color, 0.75)}, 0 2px 6px rgba(10,37,64,0.10)` : "0 1px 2px rgba(10,37,64,0.07)",
                              zIndex: isSel ? 30 : 10,
                            }}>
                            {(() => {
                              // scale label + icon with the widget (Letter aspect ~1.294)
                              const px = Math.min(Math.max(pos.h * pageWidth * 1.294 * 0.34, 9), 26);
                              return (
                                <span className="flex h-full w-full items-center justify-center gap-[0.4em] overflow-hidden px-1 font-semibold"
                                  style={{ fontSize: px }}>
                                  <Icon style={{ width: px * 1.1, height: px * 1.1 }} className="flex-shrink-0" />
                                  <span className="truncate">{FIELD_DEFAULTS[f.kind].label}</span>
                                </span>
                              );
                            })()}
                            {/* corner handles (selected only) — resize from any corner */}
                            {isSel && (["nw", "ne", "sw", "se"] as Corner[]).map((c) => (
                              <span key={c} onPointerDown={(e) => startResize(f, c, e)}
                                className={cn("absolute h-3 w-3 rounded-full border-2 bg-white shadow",
                                  c === "nw" && "-left-1.5 -top-1.5 cursor-nw-resize",
                                  c === "ne" && "-right-1.5 -top-1.5 cursor-ne-resize",
                                  c === "sw" && "-bottom-1.5 -left-1.5 cursor-sw-resize",
                                  c === "se" && "-bottom-1.5 -right-1.5 cursor-se-resize")}
                                style={{ borderColor: color }} />
                            ))}
                            {/* the DocuSign-style toolbar */}
                            {isSel && !ghost && (
                              <div onClick={(e) => e.stopPropagation()} onPointerDown={(e) => e.stopPropagation()}
                                className="absolute left-0 top-full z-40 mt-2 flex items-center gap-1 whitespace-nowrap rounded-xl border border-border bg-card px-2 py-1.5 shadow-lg">
                                {/* recipient chip + reassign menu */}
                                <div className="relative">
                                  <button type="button" onClick={() => setChipMenu((v) => !v)}
                                    className="flex items-center gap-1 rounded-lg px-1.5 py-1 hover:bg-muted">
                                    <span className="flex h-7 w-7 items-center justify-center rounded-full text-[11px] font-bold leading-none tracking-tight text-white ring-2 ring-white" style={{ backgroundColor: color }}>
                                      {initialsOf(rec?.name ?? "?")}
                                    </span>
                                    <ChevronDown className="h-4 w-4 text-muted-foreground" />
                                  </button>
                                  {chipMenu && (
                                    <div className="absolute left-0 top-full z-50 mt-1 w-52 rounded-lg border border-border bg-card p-1 shadow-xl">
                                      {recipients.map((r) => (
                                        <button key={r.id} type="button" onClick={() => reassignField(f, r.id)}
                                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[12.5px] hover:bg-muted">
                                          <span className="h-2.5 w-2.5 flex-shrink-0 rounded-full" style={{ backgroundColor: recipientColor(recipients, r.id) }} />
                                          <span className="min-w-0 flex-1 truncate text-foreground">{r.name}</span>
                                          {r.id === f.recipient_id && <Check className="h-3.5 w-3.5 flex-shrink-0 text-primary" />}
                                        </button>
                                      ))}
                                    </div>
                                  )}
                                </div>
                                {/* required toggle */}
                                <button type="button" onClick={() => setFieldRequired(f, !f.required)}
                                  className="flex items-center gap-1.5 rounded-lg px-1.5 py-1 hover:bg-muted">
                                  <span className={cn("relative h-4 w-7 rounded-full transition-colors", f.required ? "bg-primary" : "bg-border")}>
                                    <span className={cn("absolute top-0.5 h-3 w-3 rounded-full bg-white transition-all", f.required ? "left-3.5" : "left-0.5")} />
                                  </span>
                                  <span className="text-[12px] font-medium text-foreground">Required</span>
                                </button>
                                <span className="mx-0.5 h-5 w-px bg-border" />
                                {CONFIG_KINDS[f.kind] && (
                                  <div className="relative">
                                    <button type="button" onClick={() => setGearOpen((v) => !v)} title="Field settings"
                                      className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"><Settings2 className="h-4 w-4" /></button>
                                    {gearOpen && (
                                      <div className="absolute left-1/2 top-full z-50 mt-1 w-80 max-w-[85vw] -translate-x-1/2 whitespace-normal rounded-lg border border-border bg-card p-3.5 text-left shadow-xl">
                                        <p className="mb-1 text-[11.5px] font-semibold text-foreground">{CONFIG_KINDS[f.kind].label}</p>
                                        {CONFIG_KINDS[f.kind].multi ? (
                                          <textarea rows={4} defaultValue={(f.meta?.options ?? []).join("\n")}
                                            onBlur={(e) => setFieldMeta(f, { ...f.meta, options: e.target.value.split("\n").map((o) => o.trim()).filter(Boolean) })}
                                            placeholder={"Option A\nOption B"}
                                            className="w-full resize-none rounded-md border border-border bg-background px-2 py-1.5 text-[12.5px] outline-none focus:ring-2 focus:ring-primary/30" />
                                        ) : (
                                          <input defaultValue={(f.meta?.[CONFIG_KINDS[f.kind].key] as string) ?? ""}
                                            onBlur={(e) => setFieldMeta(f, { ...f.meta, [CONFIG_KINDS[f.kind].key]: e.target.value.trim() })}
                                            className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-[12.5px] outline-none focus:ring-2 focus:ring-primary/30" />
                                        )}
                                        <p className="mt-2 break-words text-[11.5px] leading-relaxed text-muted-foreground">{CONFIG_KINDS[f.kind].hint}</p>
                                        <button type="button" onClick={() => setGearOpen(false)}
                                          className="mt-2 w-full rounded-md bg-primary py-1 text-[12px] font-semibold text-white hover:bg-primary/90">Done</button>
                                      </div>
                                    )}
                                  </div>
                                )}
                                <button type="button" onClick={() => void duplicateField(f)} title="Duplicate"
                                  className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"><CopyIcon className="h-4 w-4" /></button>
                                <button type="button" onClick={() => removeField(f.id)} title="Delete"
                                  className="rounded-lg p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"><Trash2 className="h-4 w-4" /></button>
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {editRecipients && (
            <RecipientsModal
              env={env}
              recipients={recipients}
              onClose={() => setEditRecipients(false)}
              onSaved={() => { setEditRecipients(false); reload(); }}
            />
          )}

          {/* right page index */}
          {env.page_count > 1 && (
            <aside className="hidden w-64 flex-shrink-0 overflow-y-auto border-l border-border bg-card px-4 py-4 xl:block">
              <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Pages</p>
              <div className="space-y-2.5">
                {Array.from({ length: env.page_count }, (_, i) => i + 1).map((page) => {
                  const pageColors = [...new Set(fields.filter((f) => f.page === page).map((f) => recipientColor(recipients, f.recipient_id)))];
                  const FLAG = "polygon(0 0, 70% 0, 100% 50%, 70% 100%, 0 100%)";
                  return (
                    <button key={page} type="button" onClick={() => scrollToPage(page)}
                      className="block w-full rounded-lg border border-border bg-slate-300/60 p-3 pb-1.5 transition-all hover:border-primary hover:shadow-md">
                      {/* the paper floats on the gray mat; flags stick out past its edge */}
                      <span className="relative mx-auto block w-[88%]">
                        <img src={signApi.pageURL(env.id, page)} alt={`Page ${page}`}
                          className="block w-full rounded-[3px] bg-white shadow-md ring-1 ring-black/10" loading="lazy" />
                        {pageColors.map((c, ci) => (
                          <span key={c} className="absolute -left-3.5 block h-[18px] w-9"
                            style={{ top: 10 + ci * 26, clipPath: FLAG, backgroundColor: c, borderRadius: "3px 0 0 3px" }}>
                            <span className="absolute inset-[1.5px] block"
                              style={{ clipPath: FLAG, backgroundColor: tint(c, 0.6), borderRadius: "2px 0 0 2px" }} />
                          </span>
                        ))}
                      </span>
                      <span className="mt-1.5 block px-1 text-left text-[13px] font-semibold text-foreground">{page}</span>
                    </button>
                  );
                })}
              </div>
            </aside>
          )}
        </div>
      )}
    </WizardShell>
  );
}

/* ============================ recipients modal ============================ */
function RecipientsModal({
  env, recipients, onClose, onSaved,
}: {
  env: Envelope; recipients: SignRecipient[]; onClose: () => void; onSaved: () => void;
}) {
  const [rows, setRows] = useState<{ name: string; email: string }[]>(
    recipients.length ? recipients.map((r) => ({ name: r.name, email: r.email })) : [{ name: "", email: "" }],
  );
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const setRow = (i: number, patch: Partial<{ name: string; email: string }>) =>
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const emailOk = (e: string) => /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(e.trim());
  // a row counts if the user typed anything in it; it must then be complete + valid
  const touched = rows.filter((r) => r.name.trim() || r.email.trim());
  const badRow = (r: { name: string; email: string }) => !r.name.trim() || !emailOk(r.email);
  const invalid = touched.filter(badRow);
  const valid = touched.filter((r) => !badRow(r));

  const save = async () => {
    setErr(null);
    if (invalid.length > 0) {
      setErr(invalid.some((r) => r.email.trim() && !emailOk(r.email))
        ? "One of the email addresses isn't valid — please fix it before saving."
        : "Every recipient needs a name and a valid email.");
      return;
    }
    if (valid.length === 0) {
      setErr("Add at least one recipient.");
      return;
    }
    setSaving(true);
    try {
      // reconcile by identity — untouched recipients keep their placed fields
      const key = (n: string, e: string) => `${n.trim().toLowerCase()}|${e.trim().toLowerCase()}`;
      const desired = new Set(valid.map((r) => key(r.name, r.email)));
      const existing = new Set(recipients.map((r) => key(r.name, r.email)));
      for (const r of recipients) {
        if (!desired.has(key(r.name, r.email))) await signApi.removeRecipient(env.id, r.id);
      }
      for (const r of valid) {
        if (!existing.has(key(r.name, r.email))) await signApi.addRecipient(env.id, { name: r.name.trim(), email: r.email.trim() });
      }
      onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Couldn't save recipients.");
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div className="w-full max-w-xl rounded-2xl bg-card p-6 shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <h2 className="text-lg font-semibold text-foreground">Edit recipients</h2>
        <div className="mt-4 max-h-[50vh] space-y-3 overflow-y-auto pr-1">
          {rows.map((r, i) => (
            <div key={i} className="relative rounded-xl border border-border bg-background p-3.5 pl-5">
              <span className="absolute inset-y-0 left-0 w-1.5 rounded-l-xl" style={{ backgroundColor: RECIPIENT_COLORS[i % RECIPIENT_COLORS.length] }} />
              <div className="grid gap-2.5 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Name <span className="text-destructive">*</span></label>
                  <input value={r.name} onChange={(e) => setRow(i, { name: e.target.value })} placeholder="Full name"
                    className="w-full rounded-lg border border-border bg-card px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30" />
                </div>
                <div>
                  <label className="mb-1 block text-[11.5px] font-semibold text-muted-foreground">Email <span className="text-destructive">*</span></label>
                  <input value={r.email} onChange={(e) => setRow(i, { email: e.target.value })} placeholder="email@example.com" type="email"
                    className={cn("w-full rounded-lg border bg-card px-3 py-2 text-[13.5px] outline-none focus:ring-2 focus:ring-primary/30",
                      r.email.trim() && !emailOk(r.email) ? "border-destructive/60" : "border-border")} />
                </div>
              </div>
              {rows.length > 1 && (
                <button type="button" onClick={() => setRows((rs) => rs.filter((_, j) => j !== i))}
                  className="absolute right-2.5 top-2.5 rounded p-1 text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
                  <Trash2 className="h-4 w-4" />
                </button>
              )}
            </div>
          ))}
        </div>
        <button type="button" onClick={() => setRows((rs) => [...rs, { name: "", email: "" }])}
          className="mt-3 inline-flex items-center gap-2 rounded-lg border border-border bg-card px-3.5 py-1.5 text-[13px] font-semibold text-foreground hover:bg-muted">
          <Plus className="h-4 w-4" /> Add Recipient
        </button>
        {err && <p className="mt-3 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[12.5px] text-destructive">{err}</p>}
        <p className="mt-3 text-[11.5px] text-muted-foreground">Removing a recipient also removes their placed fields.</p>
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onClose}
            className="rounded-lg border border-border bg-card px-4 py-2 text-[13.5px] font-semibold text-foreground hover:bg-muted">Cancel</button>
          <button type="button" onClick={() => void save()} disabled={saving}
            className="inline-flex items-center gap-2 rounded-lg bg-primary px-5 py-2 text-[13.5px] font-semibold text-white hover:bg-primary/90 disabled:opacity-50">
            {saving && <Loader2 className="h-4 w-4 animate-spin" />} Save
          </button>
        </div>
      </div>
    </div>
  );
}

/* ============================ read-only (sent/completed) ============================ */
// SkeletonPage — a shimmering US-letter placeholder until the rendered PDF
// page arrives (pdfium renders on demand, so first paint can take a moment).
function SkeletonPage({ src, alt }: { src: string; alt: string }) {
  const [loaded, setLoaded] = useState(false);
  return (
    <div className="relative overflow-hidden rounded-lg border border-border bg-white shadow-sm">
      {!loaded && (
        <div className="aspect-[17/22] w-full animate-pulse bg-muted/70 p-8">
          <div className="h-4 w-2/3 rounded bg-muted-foreground/15" />
          <div className="mt-4 space-y-2.5">
            {Array.from({ length: 10 }, (_, i) => (
              <div key={i} className="h-2.5 rounded bg-muted-foreground/10" style={{ width: `${90 - (i % 4) * 12}%` }} />
            ))}
          </div>
        </div>
      )}
      <img
        src={src}
        alt={alt}
        onLoad={() => setLoaded(true)}
        className={cn("block w-full", !loaded && "absolute inset-0 opacity-0")}
      />
    </div>
  );
}

function ReadOnlyEnvelope({ env, events }: { env: Envelope; events: SignEvent[] | null }) {
  const recipients = env.recipients ?? [];
  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl px-6 py-6 lg:px-8">
        <Link to="/apps/sign" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" /> All envelopes
        </Link>
        <header className="mb-5 flex flex-wrap items-center gap-3">
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-lg font-semibold text-foreground">{env.title}</h1>
            <p className="text-[12.5px] text-muted-foreground">{env.page_count} pages · status: <span className="font-semibold uppercase">{env.status}</span></p>
          </div>
          {env.status === "completed" && (
            <a href={signApi.downloadURL(env.id)} className="inline-flex items-center gap-2 rounded-lg bg-success px-5 py-2 text-[13.5px] font-semibold text-white hover:bg-success/90">
              <Download className="h-4 w-4" /> Download signed PDF
            </a>
          )}
        </header>
        <div className="flex flex-col gap-6 lg:flex-row">
          <div className="min-w-0 flex-1 space-y-6">
            {Array.from({ length: env.page_count }, (_, i) => i + 1).map((page) => (
              <div key={page}>
                <p className="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Page {page} of {env.page_count}</p>
                <SkeletonPage src={signApi.pageURL(env.id, page)} alt={`Page ${page}`} />
              </div>
            ))}
          </div>
          <aside className="w-full flex-shrink-0 space-y-4 lg:w-80">
            {/* invisible twin of the page label so the first card's top edge
                aligns exactly with the PDF container */}
            <p aria-hidden="true" className="!mb-[-10px] hidden select-none text-[11px] font-semibold uppercase tracking-wide text-transparent lg:block">.</p>
            <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
              <h3 className="text-[13px] font-semibold text-foreground">Recipients</h3>
              <ul className="mt-2 space-y-1.5">
                {recipients.map((r) => (
                  <li key={r.id} className="flex items-center gap-2.5 rounded-lg border border-border px-2.5 py-2">
                    <span className="h-2.5 w-2.5 flex-shrink-0 rounded-full" style={{ backgroundColor: recipientColor(recipients, r.id) }} />
                    <span className="min-w-0 flex-1"><span className="block truncate text-[13px] font-medium text-foreground">{r.name}</span><span className="block truncate text-[11.5px] text-muted-foreground">{r.email}</span></span>
                    <span className="text-[10px] font-bold uppercase text-muted-foreground">{r.status}</span>
                  </li>
                ))}
              </ul>
            </section>
            {events && (
              <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
                <h3 className="text-[13px] font-semibold text-foreground">Audit trail</h3>
                <ol className="mt-2.5 space-y-2.5">
                  {events.map((ev, i) => (
                    <li key={i} className="flex gap-2.5 text-[12.5px]">
                      <span className="mt-1 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-primary" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium text-foreground">{prettyAction(ev.action)}</span>
                        <span className="block truncate text-[11.5px] text-muted-foreground" title={ev.actor && ev.actor !== "system" ? ev.actor : undefined}>
                          {new Date(ev.created_at).toLocaleString()}
                          {ev.actor && ev.actor !== "system" ? ` · ${ev.actor}` : ""}
                        </span>
                        {/* runaway detail strings (long filenames etc.) get their
                            own single line, ellipsized — full text on hover */}
                        {ev.detail && (
                          <span className="block truncate text-[11.5px] text-muted-foreground/80" title={ev.detail}>
                            {ev.detail}
                          </span>
                        )}
                      </span>
                    </li>
                  ))}
                </ol>
              </section>
            )}
          </aside>
        </div>
      </div>
    </div>
  );
}

/* ============================ shared shell ============================ */
function WizardShell({
  title, onClose, onBack, action, children,
}: {
  title: string; onClose: () => void; onBack?: () => void;
  action: React.ReactNode; children: React.ReactNode;
}) {
  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <header className="flex flex-shrink-0 items-center gap-3 border-b border-border bg-card px-4 py-2.5">
        <button type="button" onClick={onClose} className="rounded p-1.5 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        {onBack && (
          <button type="button" onClick={onBack} className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[13px] font-medium text-muted-foreground hover:bg-muted">
            <ArrowLeft className="h-4 w-4" /> Back
          </button>
        )}
        <h1 className="text-[14px] font-semibold text-foreground">{title}</h1>

        <div className="ml-auto">{action}</div>
      </header>
      <div className="flex flex-1 flex-col overflow-y-auto">{children}</div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="mb-3 border-b border-border pb-2 text-[15px] font-semibold text-foreground">{title}</h2>
      {children}
    </section>
  );
}
