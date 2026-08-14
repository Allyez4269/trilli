// Lightweight, dependency-free password strength heuristic. Favors length and
// character variety (NIST-aligned: longer beats forced composition), with small
// penalties for obviously weak patterns. Returns a 0–4 score plus a label and a
// single actionable suggestion for the UI.

export interface Strength {
  score: 0 | 1 | 2 | 3 | 4; // 0 = too short/empty, 4 = strong
  label: string;
  suggestion: string;
}

const LABELS = ["", "Weak", "Fair", "Good", "Strong"] as const;

// A few of the most common passwords + obvious keyboard runs. Not exhaustive —
// just enough to knock down the score on the laziest choices.
const COMMON = new Set([
  "password",
  "password1",
  "passw0rd",
  "12345678",
  "123456789",
  "1234567890",
  "qwerty",
  "qwertyui",
  "qwerty123",
  "111111",
  "abc123",
  "iloveyou",
  "admin",
  "welcome",
  "letmein",
  "trilli",
]);

function isLowEntropy(pw: string): boolean {
  const lower = pw.toLowerCase();
  if (COMMON.has(lower)) return true;
  // All one repeated character ("aaaaaaaa").
  if (/^(.)\1+$/.test(pw)) return true;
  // Straight numeric run like 12345678 / 87654321.
  if (/^\d+$/.test(pw) && pw.length <= 10) return true;
  return false;
}

const MIN_LEN = 8;

export function scorePassword(pw: string): Strength {
  if (!pw) return { score: 0, label: "", suggestion: "" };
  if (pw.length < MIN_LEN) {
    return { score: 0, label: "Too short", suggestion: `Use at least ${MIN_LEN} characters.` };
  }

  const classes =
    (/[a-z]/.test(pw) ? 1 : 0) +
    (/[A-Z]/.test(pw) ? 1 : 0) +
    (/[0-9]/.test(pw) ? 1 : 0) +
    (/[^A-Za-z0-9]/.test(pw) ? 1 : 0);

  let points = 0;
  if (pw.length >= 8) points++;
  if (pw.length >= 12) points++;
  if (pw.length >= 16) points++;
  if (classes >= 2) points++;
  if (classes >= 3) points++;
  if (classes >= 4) points++;

  if (isLowEntropy(pw)) points = Math.min(points, 1);

  // Map points (1–6) to a 1–4 score.
  const score: Strength["score"] = points <= 2 ? 1 : points <= 3 ? 2 : points <= 4 ? 3 : 4;

  // One nudge toward the cheapest win.
  let suggestion = "";
  if (score < 4) {
    if (pw.length < 12) suggestion = "Longer is stronger — aim for 12+ characters.";
    else if (classes < 3) suggestion = "Mix in uppercase, numbers, or symbols.";
    else if (isLowEntropy(pw)) suggestion = "Avoid common words and simple sequences.";
  }

  return { score, label: LABELS[score], suggestion };
}
