import { useState } from "react";
import { Check, Copy, Download } from "lucide-react";

import { cn } from "@/lib/utils";

interface Param {
  loc: "path" | "query" | "body" | "form";
  name: string;
  type: string;
  required?: boolean;
  desc: string;
}
interface Endpoint {
  method: "GET" | "POST" | "DELETE";
  path: string;
  summary: string;
  desc?: string;
  params?: Param[];
  request: string;
  response: string;
}
interface Group {
  tag: string;
  blurb: string;
  endpoints: Endpoint[];
}

const methodColor = (m: string) =>
  m === "GET"
    ? "text-emerald-600 bg-emerald-500/10"
    : m === "POST"
      ? "text-blue-600 bg-blue-500/10"
      : m === "DELETE"
        ? "text-red-600 bg-red-500/10"
        : "text-muted-foreground bg-secondary";

function CopyBlock({ label, code }: { label?: string; code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div>
      {label && (
        <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
      )}
      <div className="group relative">
        <pre className="overflow-x-auto rounded-md bg-secondary px-3 py-2.5 font-mono text-[12px] leading-relaxed text-foreground">
          {code}
        </pre>
        <button
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(code);
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            } catch {
              /* clipboard blocked */
            }
          }}
          className="absolute right-1.5 top-1.5 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground group-hover:opacity-100"
          title="Copy"
        >
          {copied ? <Check className="h-3.5 w-3.5 text-emerald-600" /> : <Copy className="h-3.5 w-3.5" />}
        </button>
      </div>
    </div>
  );
}

// ApiReference renders the full, on-brand reference for the /api/v1 surface —
// getting started, auth, an OpenAPI download, and a card per endpoint with
// parameters and copy-paste request/response examples.
export function ApiReference() {
  const base = `${window.location.origin}/api/v1`;
  const KEY = "trilli_sk_…";

  const groups: Group[] = [
    {
      tag: "Account",
      blurb: "Account and plan information.",
      endpoints: [
        {
          method: "GET",
          path: "/account",
          summary: "Get account info",
          desc: "Account id, plan name, storage usage, and the plan's storage / max-file-size limits.",
          request: `curl ${base}/account \\\n  -H "Authorization: Bearer ${KEY}"`,
          response: `{
  "account_id": 767327631254,
  "plan": "Plus",
  "storage_bytes_used": 55734275,
  "max_storage_bytes": 7696581394432,
  "max_file_size_bytes": 26843545600
}`,
        },
      ],
    },
    {
      tag: "Files",
      blurb: "Upload, download, list, and remove files. Uploads obey your plan's max file size and storage quota; transfer is metered both ways.",
      endpoints: [
        {
          method: "GET",
          path: "/files",
          summary: "List files",
          desc: "Files directly inside a folder. Omit folder_id for the account root.",
          params: [{ loc: "query", name: "folder_id", type: "integer", desc: "Folder to list. Omit for root." }],
          request: `curl "${base}/files?folder_id=1" \\\n  -H "Authorization: Bearer ${KEY}"`,
          response: `{
  "files": [
    {
      "id": 119,
      "name": "report.pdf",
      "size_bytes": 459588,
      "content_type": "application/pdf",
      "parent_folder_id": 1,
      "created_at": "2026-06-06T18:02:10Z"
    }
  ]
}`,
        },
        {
          method: "POST",
          path: "/files",
          summary: "Upload a file",
          desc: "multipart/form-data. Returns the created file. 413 if it exceeds the plan's max file size; 507 if over storage/transfer.",
          params: [
            { loc: "form", name: "file", type: "binary", required: true, desc: "The file contents." },
            { loc: "form", name: "folder_id", type: "integer", desc: "Destination folder. Omit for root." },
          ],
          request: `curl -X POST ${base}/files \\\n  -H "Authorization: Bearer ${KEY}" \\\n  -F "file=@report.pdf" \\\n  -F "folder_id=1"`,
          response: `{
  "id": 120,
  "name": "report.pdf",
  "size_bytes": 459588,
  "content_type": "application/pdf",
  "parent_folder_id": 1,
  "status": "active"
}`,
        },
        {
          method: "GET",
          path: "/files/{id}",
          summary: "Get file metadata",
          params: [{ loc: "path", name: "id", type: "integer", required: true, desc: "File id." }],
          request: `curl ${base}/files/119 \\\n  -H "Authorization: Bearer ${KEY}"`,
          response: `{
  "id": 119,
  "name": "report.pdf",
  "size_bytes": 459588,
  "content_type": "application/pdf",
  "parent_folder_id": 1
}`,
        },
        {
          method: "GET",
          path: "/files/{id}/content",
          summary: "Download file contents",
          desc: "Streams the raw bytes. Egress is metered against your monthly transfer allowance.",
          params: [{ loc: "path", name: "id", type: "integer", required: true, desc: "File id." }],
          request: `curl ${base}/files/119/content \\\n  -H "Authorization: Bearer ${KEY}" \\\n  -o report.pdf`,
          response: `# binary file contents (application/octet-stream)`,
        },
        {
          method: "DELETE",
          path: "/files/{id}",
          summary: "Move a file to trash",
          params: [{ loc: "path", name: "id", type: "integer", required: true, desc: "File id." }],
          request: `curl -X DELETE ${base}/files/119 \\\n  -H "Authorization: Bearer ${KEY}"`,
          response: `# 204 No Content`,
        },
      ],
    },
    {
      tag: "Folders",
      blurb: "List and create folders.",
      endpoints: [
        {
          method: "GET",
          path: "/folders",
          summary: "List folders",
          desc: "Subfolders of a folder. Omit parent_id for top-level folders.",
          params: [{ loc: "query", name: "parent_id", type: "integer", desc: "Parent folder. Omit for top-level." }],
          request: `curl "${base}/folders" \\\n  -H "Authorization: Bearer ${KEY}"`,
          response: `{
  "folders": [
    { "id": 1, "name": "Documents", "parent_folder_id": null }
  ]
}`,
        },
        {
          method: "POST",
          path: "/folders",
          summary: "Create a folder",
          desc: "JSON body. 409 if a folder with that name already exists in the same place.",
          params: [
            { loc: "body", name: "name", type: "string", required: true, desc: "Folder name." },
            { loc: "body", name: "parent_id", type: "integer", desc: "Parent folder. Omit for root." },
          ],
          request: `curl -X POST ${base}/folders \\\n  -H "Authorization: Bearer ${KEY}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"name":"Reports","parent_id":1}'`,
          response: `{
  "id": 42,
  "name": "Reports",
  "parent_folder_id": 1,
  "status": "active"
}`,
        },
      ],
    },
  ];

  return (
    <div className="space-y-6">
      {/* Getting started */}
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-foreground">API reference</h2>
          <a
            href={`${base}/openapi.json`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-[12px] font-medium text-foreground hover:bg-muted"
            title="Machine-readable OpenAPI 3 spec — import into Postman/Insomnia or generate a client"
          >
            <Download className="h-3.5 w-3.5" /> OpenAPI spec
          </a>
        </div>
        <div className="space-y-4 px-4 py-4">
          <div>
            <p className="mb-1 text-[12px] font-medium text-foreground">Base URL</p>
            <code className="block rounded-md bg-secondary px-3 py-2 font-mono text-[12px] text-foreground">{base}</code>
          </div>
          <div>
            <p className="mb-1 text-[12px] font-medium text-foreground">Authentication</p>
            <p className="mb-1.5 text-[12px] text-muted-foreground">
              Create a key above, then send it as a Bearer token on every request. Keys carry full
              access to this account and obey the same plan limits as the app.
            </p>
            <CopyBlock code={`Authorization: Bearer ${KEY}`} />
          </div>
        </div>
      </div>

      {/* Endpoint groups */}
      {groups.map((g) => (
        <div key={g.tag} className="space-y-3">
          <div>
            <h3 className="text-sm font-semibold text-foreground">{g.tag}</h3>
            <p className="text-[12.5px] text-muted-foreground">{g.blurb}</p>
          </div>
          {g.endpoints.map((e) => (
            <div
              key={e.method + e.path}
              className="overflow-hidden rounded-xl border border-border bg-card shadow-sm"
            >
              <div className="flex items-center gap-2.5 border-b border-border px-4 py-2.5">
                <span
                  className={cn(
                    "rounded px-1.5 py-0.5 font-mono text-[11px] font-bold",
                    methodColor(e.method),
                  )}
                >
                  {e.method}
                </span>
                <code className="font-mono text-[13px] font-medium text-foreground">{e.path}</code>
                <span className="ml-auto text-[12px] text-muted-foreground">{e.summary}</span>
              </div>
              <div className="space-y-3 px-4 py-3.5">
                {e.desc && <p className="text-[12.5px] text-muted-foreground">{e.desc}</p>}
                {e.params && e.params.length > 0 && (
                  <div className="overflow-hidden rounded-md border border-border">
                    <table className="w-full text-[12px]">
                      <thead className="bg-muted/40">
                        <tr className="text-left text-[10.5px] uppercase tracking-wide text-muted-foreground">
                          <th className="px-3 py-1.5 font-medium">Param</th>
                          <th className="px-3 py-1.5 font-medium">In</th>
                          <th className="px-3 py-1.5 font-medium">Type</th>
                          <th className="px-3 py-1.5 font-medium">Description</th>
                        </tr>
                      </thead>
                      <tbody>
                        {e.params.map((p) => (
                          <tr key={p.name} className="border-t border-border">
                            <td className="px-3 py-1.5">
                              <code className="font-mono text-foreground">{p.name}</code>
                              {p.required && <span className="ml-1 text-[10px] text-red-600">required</span>}
                            </td>
                            <td className="px-3 py-1.5 text-muted-foreground">{p.loc}</td>
                            <td className="px-3 py-1.5 text-muted-foreground">{p.type}</td>
                            <td className="px-3 py-1.5 text-muted-foreground">{p.desc}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                <CopyBlock label="Request" code={e.request} />
                <CopyBlock label="Response" code={e.response} />
              </div>
            </div>
          ))}
        </div>
      ))}

      {/* Errors */}
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="border-b border-border px-4 py-3">
          <h3 className="text-sm font-semibold text-foreground">Errors</h3>
        </div>
        <table className="w-full text-[12.5px]">
          <tbody>
            {[
              ["400", "Bad request — a required field is missing or invalid."],
              ["401", "Missing or invalid API key."],
              ["403", "API access is not included in this account's plan."],
              ["404", "The file or folder doesn't exist."],
              ["409", "A folder with that name already exists here."],
              ["413", "Upload exceeds the plan's max file size."],
              ["507", "Storage quota or monthly transfer limit reached."],
            ].map(([code, desc]) => (
              <tr key={code} className="border-b border-border last:border-0">
                <td className="w-16 px-4 py-1.5">
                  <code className="font-mono font-semibold text-foreground">{code}</code>
                </td>
                <td className="px-3 py-1.5 text-muted-foreground">{desc}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
