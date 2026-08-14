import SignHome from "@/pages/sign/SignHome";
import { canUseSign } from "@/lib/productivity/access";
import { useAuth } from "@/contexts/AuthContext";
import { ComingSoon } from "@/components/ComingSoon";

export function SharedWithMe() {
  return (
    <ComingSoon
      title="Shared with me"
      description="Files other workspace members have shared into your view."
      phase="Phase 3"
      details="Requires the shares table and an inbox query."
    />
  );
}

export function SharedByMe() {
  return (
    <ComingSoon
      title="Shared by me"
      description="Files you've shared out, plus active transfer-drop links."
      phase="Phase 3"
      details="Lists your active share tokens with expiry, password protection, and download counts."
    />
  );
}

export function ActivityLog() {
  return (
    <ComingSoon
      title="Activity"
      description="An audit trail of every upload, download, share, rename, delete in your workspace."
      phase="Phase 4"
      details="Needs an audit_log table populated by service-level hooks."
    />
  );
}

export function WorkspaceSettings() {
  return (
    <ComingSoon
      title="Workspace settings"
      description="Tenant name, branding, share defaults, retention policy."
      phase="Phase 4"
      details="Tenant admin only."
    />
  );
}

// API access now lives in pages/ApiAccess.tsx (real key management + docs).

export function Support() {
  return (
    <ComingSoon
      title="Support"
      description="Get help, browse guides, and contact the Trilli team."
      phase="a future release"
      details="In-app help center, ticketing, and status page."
    />
  );
}

export function TrilliPDF() {
  return (
    <ComingSoon
      title="Trilli PDF"
      description="Edit, annotate, reorder, merge, split, and fill PDFs right inside Trilli — no downloads, no third-party tools."
      phase="soon"
      details="Part of the Trilli Apps ecosystem."
      note="Trilli PDF is on the way — work with PDFs directly in your workspace: annotate, reorganize pages, merge/split, and fill forms. Check back soon."
    />
  );
}

export function TrilliSign() {
  // Private beta: allowlisted users get the live (in-progress) Trilli Sign
  // app; everyone else sees the unchanged coming-soon stub.
  const { identity } = useAuth();
  if (canUseSign(identity?.user?.email)) return <SignHome />;
  return (
    <ComingSoon
      title="Trilli Sign"
      description="Send documents for e-signature and track their status — a DocuSign-style signing flow built into Trilli."
      phase="soon"
      details="Part of the Trilli Apps ecosystem."
      note="Trilli Sign is on the way — prepare a document, add signers and fields, send it out, and watch it complete, all within Trilli. Check back soon."
    />
  );
}

export function StatusPage() {
  return (
    <ComingSoon
      title="Status"
      description="Live health of Trilli's services, plus a history of past incidents and maintenance."
      phase="soon"
      details="Real-time uptime, component status, and incident timeline."
      note="We're building a public status page so you can check service health at a glance and subscribe to incident updates. Check back shortly."
    />
  );
}

// Privacy + Terms now live in pages/Legal.tsx (full policies).
