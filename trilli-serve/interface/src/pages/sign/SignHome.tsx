// Trilli Sign — envelope dashboard. Create envelopes from Trilli PDFs or
// uploads, edit, send, track; generally available to all users.
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Ban, Check, Download, FileSignature, Folder, Loader2, PenTool, Plus, Search, Send, Trash2, Users, X } from "lucide-react";
import { Paginator } from "@/components/Paginator";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "@/components/Dialogs";

import { signApi, type Envelope, type EnvelopeStatus } from "@/lib/sign/api";
import { encodeId } from "@/lib/ids";
import { cn } from "@/lib/utils";

const STATUS_STYLES: Record<EnvelopeStatus, string> = {
  draft: "bg-secondary text-muted-foreground",
  sent: "bg-primary/10 text-primary",
  completed: "bg-success/10 text-success",
  voided: "bg-destructive/10 text-destructive",
  declined: "bg-destructive/10 text-destructive",
};

function StatusChip({ status }: { status: EnvelopeStatus }) {
  return (
    <span className={cn("rounded px-1.5 py-px text-[9.5px] font-bold uppercase tracking-wide", STATUS_STYLES[status])}>
      {status}
    </span>
  );
}

export default function SignHome() {
  const navigate = useNavigate();
  const [envelopes, setEnvelopes] = useState<Envelope[] | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const [query, setQuery] = useState("");

  // Quick filter across every visible attribute — name, status, recipients,
  // and the formatted date — so a single box searches "declined", "alex",
  // "consulting", "7/3", etc.
  const q = query.trim().toLowerCase();
  const matches = (e: Envelope) => {
    if (!q) return true;
    const hay = [
      e.title,
      e.status,
      e.category,
      (e.recipients ?? []).flatMap((r) => [r.name, r.email]).join(" "),
      new Date(e.updated_at).toLocaleString(),
    ]
      .join(" ")
      .toLowerCase();
    return hay.includes(q);
  };
  const filtered = (envelopes ?? []).filter(matches);

  // At most 7 envelopes per page; the rest are reachable via the paginator.
  const PAGE_SIZE = 7;
  const total = filtered.length;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const safePage = Math.min(page, totalPages - 1);
  const pageEnvelopes = filtered.slice(safePage * PAGE_SIZE, safePage * PAGE_SIZE + PAGE_SIZE);

  // Reset to the first page whenever the filter changes.
  useEffect(() => setPage(0), [q]);

  const reload = () => {
    signApi
      .list()
      .then((r) => setEnvelopes(r.envelopes))
      .catch(() => setError("Couldn't load envelopes."));
  };
  useEffect(reload, []);

  // New envelope: create a blank draft and go straight to Set Up (recipients);
  // the document is chosen inside the flow, not via an up-front picker.
  const startNewEnvelope = async () => {
    setCreating(true);
    setError(null);
    try {
      const e = await signApi.create();
      navigate(`/apps/sign/e/${encodeId(e.id)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't create the envelope.");
      setCreating(false);
    }
  };

  // Draft pending deletion / sent envelope pending void — via ConfirmDialog.
  const [deleteTarget, setDeleteTarget] = useState<Envelope | null>(null);
  const [voidTarget, setVoidTarget] = useState<Envelope | null>(null);
  const [resentId, setResentId] = useState<number | null>(null); // brief ✓ after resend



  const resend = async (e: Envelope) => {
    setError(null);
    try {
      await signApi.resend(e.id);
      setResentId(e.id);
      setTimeout(() => setResentId((cur) => (cur === e.id ? null : cur)), 2500);
    } catch {
      setError("Couldn't resend the signing links.");
    }
  };

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="mx-auto max-w-7xl px-6 py-8 lg:px-8">
        <header className="mb-8 flex items-center gap-3">
          <div className="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-xl bg-[#0A2540] text-white shadow-sm">
            <PenTool className="h-6 w-6" />
          </div>
          <div className="min-w-0 flex-1">
            <h1 className="text-xl font-semibold text-foreground">Trilli Sign</h1>
            <p className="text-[13px] text-muted-foreground">
              Send agreements for e-signature — encrypted end to end, native to Trilli.
            </p>
          </div>
          {(envelopes?.length ?? 0) > 0 && (
            <button
              type="button"
              onClick={() => void startNewEnvelope()}
              disabled={creating}
              className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-60"
            >
              {creating ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
              New envelope
            </button>
          )}
        </header>

        {error && (
          <p className="mb-4 rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-2.5 text-[13px] text-destructive">
            {error}
          </p>
        )}

        {envelopes === null ? (
          <div className="flex justify-center py-16">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : envelopes.length === 0 ? (
          <section className="rounded-xl border border-border bg-card p-10 text-center shadow-sm">
            <FileSignature className="mx-auto h-10 w-10 text-muted-foreground/60" />
            <h2 className="mt-4 text-[15px] font-semibold text-foreground">No envelopes yet</h2>
            <p className="mx-auto mt-1 max-w-md text-[13px] text-muted-foreground">
              Pick a PDF or Word document from your Trilli space, place signature fields, add recipients, and send.
            </p>
            <button
              type="button"
              onClick={() => void startNewEnvelope()}
              disabled={creating}
              className="mt-6 inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-[13.5px] font-semibold text-white transition-colors hover:bg-primary/90 disabled:opacity-60"
            >
              <Plus className="h-4 w-4" /> Create your first envelope
            </button>
          </section>
        ) : (
          <>
            {/* Save-destination control (left) + quick filter (right). */}
            <div className="mb-3 flex flex-wrap items-center gap-3">
              {/* fixed disclosure — completed agreements always file into the
                  protected Trilli Sign directory; not user-configurable */}
              <span
                title="Completed agreements are filed automatically in the protected Trilli Sign directory"
                className="inline-flex h-[34px] items-center gap-2 rounded-[11px] bg-muted/60 px-3 text-[12.5px] text-muted-foreground"
              >
                Envelopes save to
                <span className="inline-flex items-center gap-1.5 font-semibold text-foreground">
                  <Folder className="h-3.5 w-3.5 fill-amber-300 text-amber-500" />
                  Trilli Sign › Signed Agreements
                </span>
              </span>
              <div className="relative ml-auto w-full sm:w-80">
                <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder="Search envelopes, status, recipients…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  className="h-[34px] rounded-[11px] border border-border bg-card pl-10 pr-9 text-[13.5px] focus-visible:ring-1 focus-visible:ring-primary/40"
                />
                {query && (
                  <button
                    type="button"
                    onClick={() => setQuery("")}
                    className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-background hover:text-foreground"
                    title="Clear search"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                )}
              </div>
            </div>
            {total === 0 ? (
              <section className="rounded-xl border border-border bg-card p-10 text-center shadow-sm">
                <Search className="mx-auto h-8 w-8 text-muted-foreground/60" />
                <p className="mt-3 text-[13.5px] text-muted-foreground">
                  No envelopes match “<span className="font-medium text-foreground">{query}</span>”.
                </p>
              </section>
            ) : (
          <section className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-border text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                  <th className="px-5 py-3">Envelope</th>
                  <th className="px-5 py-3">Status</th>
                  <th className="hidden px-5 py-3 lg:table-cell">Category</th>
                  <th className="hidden px-5 py-3 sm:table-cell">Recipients</th>
                  <th className="hidden px-5 py-3 md:table-cell">Updated</th>
                  <th className="px-5 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {pageEnvelopes.map((e) => (
                  <tr
                    key={e.id}
                    onClick={() => navigate(`/apps/sign/e/${encodeId(e.id)}`)}
                    className="cursor-pointer border-b border-border/60 transition-colors last:border-0 hover:bg-muted/40"
                  >
                    <td className="px-5 py-3.5">
                      <p className="text-[10px] text-foreground">{e.title}</p>
                      <p className="mt-0.5 text-[9.5px] text-muted-foreground">
                        {e.page_count} page{e.page_count === 1 ? "" : "s"}
                      </p>
                    </td>
                    <td className="px-5 py-3.5">
                      <StatusChip status={e.status} />
                    </td>
                    <td className="hidden whitespace-nowrap px-5 py-3.5 text-[12.5px] text-muted-foreground lg:table-cell">
                      {e.category || "—"}
                    </td>
                    <td className="hidden px-5 py-3.5 sm:table-cell">
                      {(e.recipients ?? []).length === 0 ? (
                        <span className="flex items-center gap-1.5 text-[12.5px] text-muted-foreground">
                          <Users className="h-3.5 w-3.5" /> —
                        </span>
                      ) : (
                        <div className="flex items-start gap-1.5">
                          <Users className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-muted-foreground" />
                          {/* stacked, one name per line */}
                          <div className="min-w-0 space-y-0.5">
                            {(e.recipients ?? []).map((r) => (
                              <p key={r.id} className="truncate whitespace-nowrap text-[12.5px] leading-snug text-muted-foreground">
                                {r.name}
                              </p>
                            ))}
                          </div>
                        </div>
                      )}
                    </td>
                    <td className="hidden whitespace-nowrap px-5 py-3.5 text-[12.5px] tabular-nums text-muted-foreground md:table-cell">
                      {new Date(e.updated_at).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}
                      <span className="text-muted-foreground/60"> · </span>
                      {new Date(e.updated_at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}
                    </td>
                    <td className="px-5 py-3.5 text-right">
                      {e.status === "completed" && (
                        <a
                          href={signApi.downloadURL(e.id)}
                          onClick={(ev) => ev.stopPropagation()}
                          className="inline-flex items-center rounded p-1.5 align-middle text-muted-foreground transition-colors hover:bg-primary/10 hover:text-primary"
                          title="Download signed PDF"
                        >
                          <Download className="h-4 w-4" />
                        </a>
                      )}
                      {e.status === "sent" && (
                        <span className="inline-flex items-center gap-0.5">
                          <button
                            type="button"
                            onClick={(ev) => {
                              ev.stopPropagation();
                              void resend(e);
                            }}
                            className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-primary/10 hover:text-primary"
                            title="Resend the signing link to recipients who haven't signed"
                          >
                            {resentId === e.id ? <Check className="h-4 w-4 text-success" /> : <Send className="h-4 w-4" />}
                          </button>
                          <button
                            type="button"
                            onClick={(ev) => {
                              ev.stopPropagation();
                              setVoidTarget(e);
                            }}
                            className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                            title="Void this envelope"
                          >
                            <Ban className="h-4 w-4" />
                          </button>
                        </span>
                      )}
                      {(e.status === "completed" || e.status === "voided" || e.status === "declined") && (
                        <button
                          type="button"
                          onClick={(ev) => {
                            ev.stopPropagation();
                            setDeleteTarget(e);
                          }}
                          className="inline-flex items-center rounded p-1.5 align-middle text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                          title={e.status === "completed" ? "Delete envelope (also removes the signed copy from Files)" : "Delete envelope"}
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      )}
                      {e.status === "draft" && (
                        <button
                          type="button"
                          onClick={(ev) => {
                            ev.stopPropagation();
                            setDeleteTarget(e);
                          }}
                          className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                          title="Delete draft"
                        >
                          <Trash2 className="h-4 w-4" />
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
            )}
          </>
        )}
        {envelopes && totalPages > 1 && (
          <div className="mt-1">
            <Paginator
              bordered={false}
              currentPage={safePage + 1}
              totalPages={totalPages}
              rangeStart={safePage * PAGE_SIZE + 1}
              rangeEnd={Math.min(safePage * PAGE_SIZE + PAGE_SIZE, total)}
              total={total}
              onJump={(p) => setPage(p - 1)}
            />
          </div>
        )}
      </div>

      {voidTarget && (
        <ConfirmDialog
          title="Void this envelope?"
          danger
          confirmLabel="Void"
          message={
            <>
              <span className="font-medium text-foreground">{voidTarget.title}</span>
              <span className="mt-1 block text-[12px]">Signing links stop working and every recipient is notified that the agreement is void.</span>
            </>
          }
          onConfirm={async () => {
            await signApi.remove(voidTarget.id);
            reload();
          }}
          onClose={() => setVoidTarget(null)}
        />
      )}
      {deleteTarget && (
        <ConfirmDialog
          title={deleteTarget.status === "draft" ? "Delete this draft?" : "Delete this envelope?"}
          danger
          confirmLabel="Delete"
          message={
            <>
              <span className="font-medium text-foreground">{deleteTarget.title}</span>
              {deleteTarget.status === "completed" && (
                <span className="mt-1 block text-[12px]">The signed copy saved in Files is removed too.</span>
              )}
            </>
          }
          onConfirm={async () => {
            await signApi.remove(deleteTarget.id);
            reload();
          }}
          onClose={() => setDeleteTarget(null)}
        />
      )}
    </div>
  );
}
