// Trilli Sign client — types + thin wrappers over /api/sign (private beta).
import { api } from "@/lib/api";

export type EnvelopeStatus = "draft" | "sent" | "completed" | "voided" | "declined";
export type FieldKind =
  | "signature"
  | "initials"
  | "date_signed"
  | "title"
  | "name"
  | "email"
  | "company"
  | "number"
  | "dropdown"
  | "radio"
  | "note"
  | "approve"
  | "decline"
  | "attachment"
  | "formula"
  // legacy kinds (still rendered)
  | "date"
  | "text"
  | "checkbox";

// Per-field sender configuration (dropdown options, radio group, formula).
export interface FieldMeta {
  options?: string[];
  group?: string;
  formula?: string;
}

export interface SignRecipient {
  id: number;
  name: string;
  email: string;
  signing_order: number;
  status: "pending" | "notified" | "viewed" | "signed" | "declined";
  signed_at?: string;
}

export interface SignField {
  id: number;
  recipient_id: number;
  kind: FieldKind;
  page: number; // 1-based
  x: number; // normalized 0..1
  y: number;
  w: number;
  h: number;
  required: boolean;
  meta?: FieldMeta;
  value?: string; // ceremony payload only (e.g. uploaded attachment filename)
}

export interface Envelope {
  id: number;
  title: string;
  subject: string;
  category: string;
  message: string;
  status: EnvelopeStatus;
  page_count: number;
  size_bytes: number;
  created_at: string;
  updated_at: string;
  sent_at?: string;
  completed_at?: string;
  recipients?: SignRecipient[];
  fields?: SignField[];
}

export interface SignEvent {
  actor: string;
  action: string;
  detail: string;
  created_at: string;
}

// Per-recipient accent colors (index-stable) for field boxes + roster dots.
export const RECIPIENT_COLORS = ["#2563EB", "#16A34A", "#F59E0B", "#C4589F", "#0D9488", "#E5252A"];
export const recipientColor = (recipients: SignRecipient[], id: number) => {
  const i = recipients.findIndex((r) => r.id === id);
  return RECIPIENT_COLORS[(i < 0 ? 0 : i) % RECIPIENT_COLORS.length];
};

// mix(a, b, t): blend hex color a toward hex color b by t (0 = a, 1 = b).
export const mix = (a: string, b: string, t: number) => {
  const pa = parseInt(a.replace("#", ""), 16);
  const pb = parseInt(b.replace("#", ""), 16);
  const ch = (sh: number) => Math.round(((pa >> sh) & 0xff) + (((pb >> sh) & 0xff) - ((pa >> sh) & 0xff)) * t);
  return `rgb(${ch(16)}, ${ch(8)}, ${ch(0)})`;
};

// tint(color, t): mix toward white — OPAQUE pastels that stay crisp over any page.
export const tint = (hex: string, t: number) => mix(hex, "#ffffff", t);

// muted(color): the color desaturated toward slate — visible lineage, low volume.
export const muted = (hex: string) => mix(hex, "#64748b", 0.65);

// Default field footprints, normalized to page width/height.
// Two palette groups mirroring DocuSign: Signature fields + Contact fields. Each
// footprint is normalized to page width/height.
export type FieldCategory = "signature" | "contact" | "inputs" | "actions" | "other";
export const FIELD_DEFAULTS: Record<
  FieldKind,
  { w: number; h: number; label: string; category: FieldCategory }
> = {
  signature: { w: 0.2, h: 0.05, label: "Signature", category: "signature" },
  initials: { w: 0.08, h: 0.045, label: "Initial", category: "signature" },
  date_signed: { w: 0.13, h: 0.032, label: "Date Signed", category: "signature" },
  title: { w: 0.18, h: 0.032, label: "Title", category: "signature" },
  name: { w: 0.2, h: 0.032, label: "Name", category: "contact" },
  email: { w: 0.22, h: 0.032, label: "Email", category: "contact" },
  company: { w: 0.2, h: 0.032, label: "Company", category: "contact" },
  text: { w: 0.2, h: 0.038, label: "Text", category: "inputs" },
  number: { w: 0.13, h: 0.038, label: "Number", category: "inputs" },
  checkbox: { w: 0.028, h: 0.022, label: "Checkbox", category: "inputs" },
  dropdown: { w: 0.18, h: 0.038, label: "Dropdown", category: "inputs" },
  radio: { w: 0.028, h: 0.022, label: "Radio", category: "inputs" },
  approve: { w: 0.15, h: 0.045, label: "Approve", category: "actions" },
  decline: { w: 0.15, h: 0.045, label: "Decline", category: "actions" },
  note: { w: 0.25, h: 0.08, label: "Note", category: "other" },
  attachment: { w: 0.2, h: 0.04, label: "Attachment", category: "other" },
  formula: { w: 0.15, h: 0.038, label: "Formula", category: "other" },
  date: { w: 0.13, h: 0.038, label: "Date", category: "inputs" },
};

export interface SignSettings {
  workspace_id: number | null;
  folder_id: number | null;
  workspace_name?: string;
  folder_name?: string;
}

export const signApi = {
  getSettings: () => api.get<SignSettings>("/api/sign/settings"),
  putSettings: (body: { workspace_id: number | null; folder_id: number | null }) =>
    api.put<SignSettings>("/api/sign/settings", body),
  list: () => api.get<{ envelopes: Envelope[] }>("/api/sign/envelopes"),
  // fileId omitted → a blank, document-less draft (the PDF is attached later
  // in setup via attachDocument).
  create: (fileId?: number) =>
    api.post<Envelope>("/api/sign/envelopes", fileId ? { file_id: fileId } : {}),
  attachDocument: (id: number, fileId: number) =>
    api.post<Envelope>(`/api/sign/envelopes/${id}/document`, { file_id: fileId }),
  removeDocument: (id: number) => api.delete<Envelope>(`/api/sign/envelopes/${id}/document`),
  // Desktop drag-drop: raw bytes staged as a protected file in Trilli
  // Sign/Drafts server-side, then attached — nothing lands loose in Files.
  uploadDocument: async (id: number, file: File): Promise<Envelope> => {
    const res = await fetch(
      `/api/sign/envelopes/${id}/document/upload?name=${encodeURIComponent(file.name)}`,
      { method: "POST", body: file, credentials: "same-origin" },
    );
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || "Upload failed");
    return data as Envelope;
  },
  get: (id: number) => api.get<Envelope>(`/api/sign/envelopes/${id}`),
  patch: (id: number, body: { title?: string; subject?: string; category?: string; message?: string }) =>
    api.patch<void>(`/api/sign/envelopes/${id}`, body),
  remove: (id: number) => api.delete<void>(`/api/sign/envelopes/${id}`),
  send: (id: number) => api.post<{ sent: boolean }>(`/api/sign/envelopes/${id}/send`),
  resend: (id: number) => api.post<{ resent: number }>(`/api/sign/envelopes/${id}/resend`),
  events: (id: number) => api.get<{ events: SignEvent[] }>(`/api/sign/envelopes/${id}/events`),
  addRecipient: (id: number, body: { name: string; email: string; signing_order?: number }) =>
    api.post<SignRecipient>(`/api/sign/envelopes/${id}/recipients`, body),
  removeRecipient: (id: number, rid: number) =>
    api.delete<void>(`/api/sign/envelopes/${id}/recipients/${rid}`),
  addField: (id: number, body: Omit<SignField, "id" | "required" | "value">) =>
    api.post<SignField>(`/api/sign/envelopes/${id}/fields`, body),
  patchField: (id: number, fid: number, body: Partial<Pick<SignField, "x" | "y" | "w" | "h" | "page" | "required" | "recipient_id" | "meta">>) =>
    api.patch<void>(`/api/sign/envelopes/${id}/fields/${fid}`, body),
  removeField: (id: number, fid: number) => api.delete<void>(`/api/sign/envelopes/${id}/fields/${fid}`),
  pageURL: (id: number, page: number) => `/api/sign/envelopes/${id}/pages/${page}`,
  downloadURL: (id: number) => `/api/sign/envelopes/${id}/download`,
};
