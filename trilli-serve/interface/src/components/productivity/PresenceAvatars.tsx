// PresenceAvatars — Google-Docs-style participant circles for the menu-bar row.
// Fed by Collabora's Views_List: EVERY user with the document open, including
// you. Each shows the user's profile picture (falling back to colored initials),
// deduped so a person with two tabs appears once, current user marked "You".
import { useState } from "react";

import { cn } from "@/lib/utils";

export interface CollabView {
  ViewId: number | string;
  UserName: string;
  UserId: string; // "trilli-{userId}"
  Color?: number; // RGB int from the engine — matches their cursor color
  IsCurrentView?: boolean;
  ReadOnly?: string | boolean;
}

const colorOf = (v: CollabView): string => {
  if (typeof v.Color === "number" && v.Color > 0) {
    return `#${(v.Color & 0xffffff).toString(16).padStart(6, "0")}`;
  }
  let h = 0;
  for (const c of v.UserName) h = (h * 31 + c.charCodeAt(0)) % 360;
  return `hsl(${h}, 55%, 45%)`;
};

const initialsOf = (name: string): string =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? "")
    .join("") || "?";

// numeric user id out of "trilli-1234"
const userIdOf = (uid: string): string => uid.replace(/^trilli-/, "");

function Avatar({ view, current }: { view: CollabView; current: boolean }) {
  const [imgOk, setImgOk] = useState(true);
  const uid = userIdOf(view.UserId);
  const color = colorOf(view);
  const readOnly = String(view.ReadOnly) === "1" || view.ReadOnly === true;
  return (
    <span
      title={`${view.UserName}${current ? " (you)" : ""}${readOnly ? " · viewing" : " · editing"}`}
      className="relative -ml-2 flex h-7 w-7 select-none items-center justify-center overflow-hidden rounded-full text-[10px] font-bold leading-none text-white ring-2 ring-card first:ml-0"
      style={{ backgroundColor: color, boxShadow: `0 0 0 1.5px ${color}` }}
    >
      {imgOk && uid ? (
        <img
          src={`/api/users/${uid}/avatar`}
          alt=""
          className="h-full w-full object-cover"
          onError={() => setImgOk(false)}
        />
      ) : (
        initialsOf(view.UserName)
      )}
    </span>
  );
}

export function PresenceAvatars({ views, className }: { views: CollabView[]; className?: string }) {
  // dedupe by user (two tabs = one person); keep the current view's entry so
  // "you" is marked. Sort so the current user is last (rightmost).
  const byUser = new Map<string, CollabView>();
  for (const v of views) {
    const existing = byUser.get(v.UserId);
    if (!existing || v.IsCurrentView) byUser.set(v.UserId, v);
  }
  const people = [...byUser.values()].sort(
    (a, b) => Number(!!a.IsCurrentView) - Number(!!b.IsCurrentView),
  );
  if (people.length === 0) return null;
  const shown = people.slice(0, 6);
  const overflow = people.length - shown.length;
  return (
    <div className={cn("flex items-center pl-2", className)}>
      {shown.map((v) => (
        <Avatar key={v.UserId} view={v} current={!!v.IsCurrentView} />
      ))}
      {overflow > 0 && (
        <span className="-ml-2 flex h-7 w-7 items-center justify-center rounded-full bg-muted text-[10px] font-bold text-muted-foreground ring-2 ring-card">
          +{overflow}
        </span>
      )}
    </div>
  );
}
