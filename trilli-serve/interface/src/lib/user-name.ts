// Helpers for showing a user's name + avatar initials consistently.
// Prefers the captured first/last name; falls back to the email's local part
// so accounts without a name (e.g. the workspace owner from signup) still
// read sensibly.

interface NameParts {
  first_name?: string | null;
  last_name?: string | null;
  email?: string | null;
}

function titleizeLocalPart(email: string): string {
  const local = email.split("@")[0] ?? "";
  return local
    .replace(/[._-]+/g, " ")
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(" ");
}

// displayName renders "First L." when both names are present, just the first
// name if only that's set, else a titleized email local part.
export function displayName(u?: NameParts | null): string {
  const first = (u?.first_name ?? "").trim();
  const last = (u?.last_name ?? "").trim();
  if (first && last) return `${first} ${last[0].toUpperCase()}.`;
  if (first) return first;
  const email = u?.email ?? "";
  return email ? titleizeLocalPart(email) || email : "";
}

// firstNameOf returns just the first name (for greetings), falling back to
// the first word of the email's local part.
export function firstNameOf(u?: NameParts | null): string {
  const first = (u?.first_name ?? "").trim();
  if (first) return first;
  const local = (u?.email ?? "").split("@")[0] ?? "";
  const w = local.replace(/[._-]+/g, " ").split(/\s+/).filter(Boolean)[0] ?? "";
  return w ? w[0].toUpperCase() + w.slice(1) : "";
}

// userInitials returns first+last initials, or a sensible email-derived
// fallback. Always upper-case, 1–2 chars.
export function userInitials(u?: NameParts | null): string {
  const first = (u?.first_name ?? "").trim();
  const last = (u?.last_name ?? "").trim();
  if (first && last) return (first[0] + last[0]).toUpperCase();
  if (first) return first.slice(0, 2).toUpperCase();
  const email = u?.email ?? "";
  const local = email.split("@")[0] ?? "";
  const parts = local.replace(/[._-]+/g, " ").split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (parts[0]?.slice(0, 2) ?? email.slice(0, 2)).toUpperCase();
}
