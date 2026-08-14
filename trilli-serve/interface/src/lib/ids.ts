// Opaque, reversible URL identifiers.
//
// Workspace and folder primary keys are small sequential integers (1, 2, 3…).
// Exposing them directly in the address bar (?workspace_id=4&folder_id=8) is
// ugly, leaks ordering/volume, and invites enumeration. This codec scrambles an
// integer id into a short, opaque, URL-safe token for the address bar — and
// back again — so the JSON API contract (which still speaks integers) is left
// completely untouched. Only the visible URL changes.
//
// The scramble is a modular-multiplicative permutation over a 48-bit domain
// (a.k.a. "Optimus" / Knuth multiplicative hashing): multiply by a large odd
// constant mod 2^48, XOR a fixed mask, then base32-encode with a compact
// alphabet. It is a bijection, so distinct ids always map to distinct tokens
// and decoding is exact. It is *reversible* (the constants live in this bundle),
// so it is obfuscation, not encryption — the server still authorizes every
// access via CanAccess/CanRead. Its only jobs are (a) clean URLs and (b) making
// consecutive ids look unrelated so the address bar can't be walked with +1.

const BITS = 48n;
const MOD = 1n << BITS; // 2^48 domain
const MASK = MOD - 1n; // low 48 bits

// A large odd constant (coprime to 2^48 — any odd number is) and a fixed XOR
// mask. Changing either invalidates every previously-shared link, so treat them
// as frozen. `| 1n` forces the multiplier odd regardless of the literal.
const PRIME = (0xa1b2c3d4e5f1n & MASK) | 1n;
const XOR = 0x5deec3c9a66dn & MASK;

// 32-char URL-safe alphabet (lowercase Crockford base32: digits + letters minus
// the ambiguous i, l, o, u). Tokens look like "h7k3qm9b".
const ALPHABET = "0123456789abcdefghjkmnpqrstvwxyz";

// Extended Euclid — modular inverse of `a` mod `m` (a must be coprime to m).
function modInverse(a: bigint, m: bigint): bigint {
  let [oldR, r] = [((a % m) + m) % m, m];
  let [oldS, s] = [1n, 0n];
  while (r !== 0n) {
    const q = oldR / r;
    [oldR, r] = [r, oldR - q * r];
    [oldS, s] = [s, oldS - q * s];
  }
  return ((oldS % m) + m) % m;
}

const INVERSE = modInverse(PRIME, MOD);

// encodeId turns an integer id into its opaque URL token.
export function encodeId(id: number): string {
  let x = BigInt(Math.trunc(id)) & MASK;
  x = (x * PRIME) & MASK;
  x = x ^ XOR;
  if (x === 0n) return ALPHABET[0];
  let out = "";
  while (x > 0n) {
    out = ALPHABET[Number(x & 31n)] + out;
    x >>= 5n;
  }
  return out;
}

// decodeId reverses encodeId. Returns null for anything that isn't one of our
// tokens (unknown chars), so callers can fall back gracefully.
export function decodeId(token: string | null | undefined): number | null {
  if (!token) return null;
  let x = 0n;
  for (const ch of token) {
    const v = ALPHABET.indexOf(ch);
    if (v < 0) return null;
    x = (x << 5n) | BigInt(v);
  }
  x = x ^ XOR;
  x = (x * INVERSE) & MASK;
  return Number(x);
}

// filesPath builds a clean, opaque path into the file browser. The API still
// uses integer ids; this is purely the address-bar representation.
//
//   filesPath(4)        -> "/files/w/<wsToken>"
//   filesPath(4, 8)     -> "/files/w/<wsToken>/f/<folderToken>"
//   filesPath(null, 8)  -> "/files/f/<folderToken>"  (workspace self-resolves)
//   filesPath()         -> "/files"
//   filesPath(4, null, "report") -> "/files/w/<wsToken>?q=report"
export function filesPath(
  workspaceId?: number | null,
  folderId?: number | null,
  q?: string,
): string {
  let p = "/files";
  if (workspaceId != null) {
    p += `/w/${encodeId(workspaceId)}`;
    if (folderId != null) p += `/f/${encodeId(folderId)}`;
  } else if (folderId != null) {
    // Folder-only deep link (e.g. the activity feed knows the folder but not
    // its workspace). Files resolves the workspace from the first browse and
    // upgrades the URL to the canonical /w/.../f/... form.
    p += `/f/${encodeId(folderId)}`;
  }
  if (q) p += `?q=${encodeURIComponent(q)}`;
  return p;
}
