import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import { api, type Quota } from "@/lib/api";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ArrowDownUp, BarChart3, Files, HardDrive, Users } from "lucide-react";

const UNLIMITED_BYTES = 1e18; // uncapped plans return max int64 from the API

function formatBytes(n: number): string {
  if (n >= UNLIMITED_BYTES) return "Unlimited";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  if (n < 1024 ** 4) return `${(n / 1024 ** 3).toFixed(2)} GB`;
  return `${(n / 1024 ** 4).toFixed(2)} TB`;
}

export default function Usage() {
  const { identity } = useAuth();
  const [quota, setQuota] = useState<Quota | null>(null);

  useEffect(() => {
    void api.get<Quota>("/api/files/quota").then(setQuota);
  }, []);

  if (!identity?.tenant || !quota) {
    return (
      <div className="px-6 py-8">
        <div className="mx-auto max-w-4xl space-y-6">
          <div className="space-y-2">
            <Skeleton className="h-8 w-56" />
            <Skeleton className="h-4 w-72" />
          </div>
          <Skeleton className="h-36 w-full rounded-xl" />
          <Skeleton className="h-44 w-full rounded-xl" />
          <div className="grid gap-4 md:grid-cols-2">
            <Skeleton className="h-28 w-full rounded-xl" />
            <Skeleton className="h-28 w-full rounded-xl" />
          </div>
        </div>
      </div>
    );
  }

  const tenant = identity.tenant;
  const unlimited = quota.max_storage_bytes >= UNLIMITED_BYTES;
  const pct = unlimited ? 0 : Math.min(100, (quota.storage_bytes_used / quota.max_storage_bytes) * 100);

  // Monthly transfer (bandwidth). No cap field = unlimited transfer.
  const xferUnlimited = quota.transfer_cap_bytes == null;
  const xferPct = xferUnlimited
    ? 0
    : Math.min(100, (quota.transfer_used_bytes / quota.transfer_cap_bytes!) * 100);
  const xferOver = !xferUnlimited && quota.transfer_used_bytes >= quota.transfer_cap_bytes!;

  return (
    <div className="px-6 py-8">
      <div className="mx-auto max-w-4xl">
        <h1 className="flex items-center gap-2 text-3xl font-semibold">
          <BarChart3 className="h-7 w-7 text-brand-purple" />
          Plan &amp; usage
        </h1>
        <p className="text-muted-foreground">
          Workspace <span className="font-medium text-foreground">{tenant.name}</span> ·{" "}
          <span className="font-medium text-foreground">{tenant.plan_name || `Plan #${tenant.plan_id}`}</span> plan
        </p>

        <Card className="mt-6">
          <CardHeader>
            <CardTitle className="text-base">Storage</CardTitle>
            <CardDescription>
              Bytes you're using across all active files.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center justify-between text-sm">
              <span className="inline-flex items-center gap-2">
                <HardDrive className="h-4 w-4 text-muted-foreground" />
                Used
              </span>
              <span className="tabular-nums">
                {formatBytes(quota.storage_bytes_used)} of {formatBytes(quota.max_storage_bytes)}
                {unlimited ? "" : ` (${pct.toFixed(1)}%)`}
              </span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div
                className={
                  pct >= 100
                    ? "h-full bg-brand-red"
                    : pct >= 80
                      ? "h-full bg-brand-red-light"
                      : "h-full bg-brand-purple"
                }
                style={{ width: `${pct}%` }}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mt-6">
          <CardHeader>
            <CardTitle className="text-base">Transfer this month</CardTitle>
            <CardDescription>
              Upload + download bandwidth for the current calendar month. Resets on the 1st.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center justify-between text-sm">
              <span className="inline-flex items-center gap-2">
                <ArrowDownUp className="h-4 w-4 text-muted-foreground" />
                Used
              </span>
              <span className="tabular-nums">
                {formatBytes(quota.transfer_used_bytes)}
                {xferUnlimited
                  ? " (Unlimited)"
                  : ` of ${formatBytes(quota.transfer_cap_bytes!)} (${xferPct.toFixed(1)}%)`}
              </span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-muted">
              <div
                className={
                  xferPct >= 100
                    ? "h-full bg-brand-red"
                    : xferPct >= 80
                      ? "h-full bg-brand-red-light"
                      : "h-full bg-brand-purple"
                }
                style={{ width: `${xferUnlimited ? 0 : xferPct}%` }}
              />
            </div>
            {xferOver && (
              <p className="text-xs text-brand-red">
                Monthly transfer limit reached — new uploads are paused until next month or a plan
                upgrade. Downloads still work, so your files stay accessible.
              </p>
            )}
          </CardContent>
        </Card>

        <div className="mt-6 grid gap-4 md:grid-cols-2">
          <Card>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-medium text-muted-foreground">
                  Max file size
                </CardTitle>
                <Files className="h-4 w-4 text-muted-foreground" />
              </div>
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-semibold">
                {quota.max_file_size_bytes >= Number.MAX_SAFE_INTEGER
                  ? "∞"
                  : formatBytes(quota.max_file_size_bytes)}
              </div>
              <CardDescription>per single upload</CardDescription>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm font-medium text-muted-foreground">Users</CardTitle>
                <Users className="h-4 w-4 text-muted-foreground" />
              </div>
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-semibold">{tenant.user_count}</div>
              <CardDescription>active in workspace</CardDescription>
            </CardContent>
          </Card>
        </div>

        <Card className="mt-6 border-dashed">
          <CardHeader>
            <CardTitle className="text-base">
              {tenant.plan_name ? `${tenant.plan_name} plan` : `Plan #${tenant.plan_id}`}
            </CardTitle>
            <CardDescription>
              The limits above come from your plan. Need more storage, transfer, or a bigger max
              file size?{" "}
              <Link to="/settings/plan" className="font-medium text-brand-purple hover:underline">
                Compare plans
              </Link>{" "}
              — upgrades are prorated and take effect immediately.
            </CardDescription>
          </CardHeader>
        </Card>
      </div>
    </div>
  );
}
