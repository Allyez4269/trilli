import { useState } from "react";
import { Ban, RotateCcw, HardDrive } from "lucide-react";

import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import StepUpModal from "@/components/StepUpModal";

// Operator actions on a tenant (SPEC §6.2). Both CMX and Global admins may
// suspend/reactivate and adjust quota (§3); each is consequential, so it routes
// through a step-up 2FA confirmation (StepUpModal) before the guarded write.
type Pending =
  | { kind: "suspend" }
  | { kind: "reactivate" }
  | { kind: "quota-set"; bytes: number }
  | { kind: "quota-clear" }
  | null;

const GB = 1024 * 1024 * 1024;

export default function AccountActions({
  tenantId,
  status,
  onChanged,
}: {
  tenantId: number;
  status: string;
  onChanged: () => void;
}) {
  const [pending, setPending] = useState<Pending>(null);
  const [gb, setGb] = useState("");

  const suspended = status === "suspended";

  async function run(p: Exclude<Pending, null>) {
    if (p.kind === "suspend") {
      await api.post(`/api/cmx/tenants/${tenantId}/suspend`);
    } else if (p.kind === "reactivate") {
      await api.post(`/api/cmx/tenants/${tenantId}/reactivate`);
    } else if (p.kind === "quota-set") {
      await api.post(`/api/cmx/tenants/${tenantId}/quota-override`, { bytes: p.bytes });
    } else {
      await api.post(`/api/cmx/tenants/${tenantId}/quota-override`, { bytes: null });
    }
    onChanged();
  }

  const modalProps = () => {
    switch (pending?.kind) {
      case "suspend":
        return {
          title: "Suspend this account?",
          description: "Members lose access on their next request. Reversible at any time.",
          confirmLabel: "Suspend account",
          destructive: true,
        };
      case "reactivate":
        return {
          title: "Reactivate this account?",
          description: "Members regain access immediately.",
          confirmLabel: "Reactivate account",
          destructive: false,
        };
      case "quota-set":
        return {
          title: "Set storage cap override?",
          description: `Override this account's storage cap to ${(pending.bytes / GB).toFixed(0)} GB, regardless of plan.`,
          confirmLabel: "Set override",
          destructive: false,
        };
      case "quota-clear":
        return {
          title: "Clear quota override?",
          description: "Revert this account to its plan's storage cap.",
          confirmLabel: "Clear override",
          destructive: false,
        };
      default:
        return null;
    }
  };
  const mp = modalProps();

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Operator actions</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-wrap items-center gap-3">
        {suspended ? (
          <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setPending({ kind: "reactivate" })}>
            <RotateCcw className="h-4 w-4" /> Reactivate
          </Button>
        ) : (
          <Button variant="outline" size="sm" className="h-8 gap-1.5 text-destructive hover:text-destructive" onClick={() => setPending({ kind: "suspend" })}>
            <Ban className="h-4 w-4" /> Suspend
          </Button>
        )}

        <div className="flex items-center gap-2">
          <HardDrive className="h-4 w-4 text-muted-foreground" />
          <Input
            type="number"
            min={0}
            value={gb}
            onChange={(e) => setGb(e.target.value)}
            placeholder="GB"
            className="h-8 w-24"
          />
          <Button
            variant="secondary"
            size="sm"
            className="h-8"
            disabled={!gb || Number(gb) <= 0}
            onClick={() => setPending({ kind: "quota-set", bytes: Math.round(Number(gb) * GB) })}
          >
            Set cap
          </Button>
          <Button variant="ghost" size="sm" className="h-8 text-muted-foreground" onClick={() => setPending({ kind: "quota-clear" })}>
            Clear override
          </Button>
        </div>

        <p className="w-full text-[11px] text-muted-foreground/60">
          Each action requires a fresh 2FA confirmation and is written to the operator audit.
        </p>
      </CardContent>

      {pending && mp && (
        <StepUpModal
          title={mp.title}
          description={mp.description}
          confirmLabel={mp.confirmLabel}
          destructive={mp.destructive}
          onConfirm={() => run(pending)}
          onClose={() => setPending(null)}
        />
      )}
    </Card>
  );
}
