// Calls a Trilli PDF tool endpoint. Every tool shares one multipart contract:
// PDF inputs (Trilli file ids and/or ad-hoc uploads) + params → result either
// streamed back as a PDF blob (download) or written into the user's file space
// (save). Read-only "info" returns JSON. Uses fetch (not the JSON api client)
// because requests are multipart and responses can be binary.

export type ToolInput = { fileIds: number[]; uploads: File[] };

export type ToolOutput =
  | { mode: "download" }
  | { mode: "save"; folderId: number | null; name: string };

export type ToolResult =
  | { kind: "blob"; blob: Blob }
  | { kind: "json"; data: unknown };

export async function runPdfTool(
  toolKey: string,
  input: ToolInput,
  params: Record<string, string>,
  output: ToolOutput,
): Promise<ToolResult> {
  const fd = new FormData();
  if (input.fileIds.length) fd.append("file_ids", JSON.stringify(input.fileIds));
  for (const f of input.uploads) fd.append("file", f);
  for (const [k, v] of Object.entries(params)) fd.append(k, v);

  if (output.mode === "save") {
    fd.append("output", "save");
    if (output.folderId != null) fd.append("target_folder_id", String(output.folderId));
    fd.append("output_name", output.name);
  } else {
    fd.append("output", "download");
  }

  const res = await fetch(`/api/pdf/${toolKey}`, { method: "POST", body: fd, credentials: "include" });
  const ct = res.headers.get("Content-Type") || "";

  if (!res.ok) {
    let msg = `The request failed (${res.status}).`;
    if (ct.includes("application/json")) {
      try {
        const j = (await res.json()) as { error?: string };
        if (j.error) msg = j.error;
      } catch {
        /* keep the generic message */
      }
    }
    throw new Error(msg);
  }

  if (ct.includes("application/json")) return { kind: "json", data: await res.json() };
  return { kind: "blob", blob: await res.blob() }; // PDF or ZIP
}
