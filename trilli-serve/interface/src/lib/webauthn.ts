// WebAuthn (passkey) helpers. The server speaks the standard WebAuthn JSON:
// binary fields (challenge, credential ids, signatures) travel as base64url
// strings, but the browser's navigator.credentials APIs want ArrayBuffers — so
// we convert in both directions here. The per-ceremony challenge is carried in
// an opaque "state" token that we echo back via the X-Passkey-State header.

import { ApiError, type Identity } from "@/lib/api";

const STATE_HEADER = "X-Passkey-State";

// ----- base64url ⇄ ArrayBuffer ----------------------------------------------

function base64urlToBuffer(value: string): ArrayBuffer {
  const pad = "=".repeat((4 - (value.length % 4)) % 4);
  const base64 = (value + pad).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return bytes.buffer;
}

function bufferToBase64url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let str = "";
  for (let i = 0; i < bytes.length; i++) str += String.fromCharCode(bytes[i]);
  return btoa(str).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// ----- options coercion (server JSON → browser options) ---------------------

/* eslint-disable @typescript-eslint/no-explicit-any */
function coerceCreationOptions(pk: any): PublicKeyCredentialCreationOptions {
  pk.challenge = base64urlToBuffer(pk.challenge);
  pk.user.id = base64urlToBuffer(pk.user.id);
  if (Array.isArray(pk.excludeCredentials)) {
    pk.excludeCredentials = pk.excludeCredentials.map((c: any) => ({
      ...c,
      id: base64urlToBuffer(c.id),
    }));
  }
  return pk as PublicKeyCredentialCreationOptions;
}

function coerceRequestOptions(pk: any): PublicKeyCredentialRequestOptions {
  pk.challenge = base64urlToBuffer(pk.challenge);
  if (Array.isArray(pk.allowCredentials)) {
    pk.allowCredentials = pk.allowCredentials.map((c: any) => ({
      ...c,
      id: base64urlToBuffer(c.id),
    }));
  }
  return pk as PublicKeyCredentialRequestOptions;
}

// ----- credential serialization (browser result → server JSON) --------------

function attestationToJSON(cred: PublicKeyCredential) {
  const r = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      attestationObject: bufferToBase64url(r.attestationObject),
      clientDataJSON: bufferToBase64url(r.clientDataJSON),
      transports: typeof r.getTransports === "function" ? r.getTransports() : [],
    },
  };
}

function assertionToJSON(cred: PublicKeyCredential) {
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      authenticatorData: bufferToBase64url(r.authenticatorData),
      clientDataJSON: bufferToBase64url(r.clientDataJSON),
      signature: bufferToBase64url(r.signature),
      userHandle: r.userHandle ? bufferToBase64url(r.userHandle) : undefined,
    },
  };
}
/* eslint-enable @typescript-eslint/no-explicit-any */

// ----- low-level fetch (mirrors api.ts error handling + custom header) ------

async function parseOrThrow<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const j = await res.json();
      if (j?.error) msg = j.error;
    } catch {
      /* fall back to statusText */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

async function post<T>(path: string, headers: Record<string, string>, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: body ? { "Content-Type": "application/json", ...headers } : headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  return parseOrThrow<T>(res);
}

const postWithState = <T>(path: string, state: string, body: unknown) =>
  post<T>(path, { [STATE_HEADER]: state }, body);

const postJSON = <T>(path: string, body?: unknown) => post<T>(path, {}, body);

// ----- public API -----------------------------------------------------------

/** Whether this browser can do WebAuthn at all. */
export function passkeysSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    !!window.PublicKeyCredential &&
    typeof navigator.credentials?.create === "function"
  );
}

/** Map a thrown DOMException from create()/get() to a friendly message. */
export function passkeyErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof DOMException) {
    // User dismissed the OS prompt, or no matching credential.
    if (err.name === "NotAllowedError") return "Passkey prompt was cancelled.";
    if (err.name === "InvalidStateError") return "A passkey is already registered on this device.";
  }
  return fallback;
}

/** Enroll a new passkey for the logged-in user. */
export async function registerPasskey(label?: string): Promise<void> {
  const begin = await postJSON<{ publicKey: unknown; state: string }>(
    "/api/me/passkeys/register/begin",
  );
  const publicKey = coerceCreationOptions(begin.publicKey);
  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey setup was cancelled.");
  const q = label ? `?label=${encodeURIComponent(label)}` : "";
  await postWithState<void>(`/api/me/passkeys/register/finish${q}`, begin.state, attestationToJSON(cred));
}

/** Passwordless sign-in with a passkey. Resolves to the new Identity. */
export async function loginWithPasskey(): Promise<Identity> {
  const begin = await postJSON<{ publicKey: unknown; state: string }>("/api/auth/passkey/begin");
  const publicKey = coerceRequestOptions(begin.publicKey);
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey sign-in was cancelled.");
  return postWithState<Identity>("/api/auth/passkey/finish", begin.state, assertionToJSON(cred));
}

/**
 * Complete a password-first login using a passkey as the second factor. The
 * user is identified by the 2FA pending token (not discoverable). Resolves to
 * the Identity once the assertion verifies.
 */
export async function loginWithPasskeyChallenge(pendingToken: string): Promise<Identity> {
  const begin = await postJSON<{ publicKey: unknown; state: string }>(
    "/api/auth/login/2fa/passkey/begin",
    { pending_token: pendingToken },
  );
  const publicKey = coerceRequestOptions(begin.publicKey);
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey verification was cancelled.");
  return post<Identity>(
    "/api/auth/login/2fa/passkey/finish",
    { [STATE_HEADER]: begin.state, "X-2FA-Pending": pendingToken },
    assertionToJSON(cred),
  );
}

/**
 * Re-authenticate with an existing passkey as a step-up: fetch a challenge from
 * `beginPath`, prompt for the passkey, then POST the assertion to `submitPath`
 * (with the challenge in the state header). Resolves to `submitPath`'s response.
 */
export async function passkeyStepUp<T>(beginPath: string, submitPath: string): Promise<T> {
  const begin = await postJSON<{ publicKey: unknown; state: string }>(beginPath);
  const publicKey = coerceRequestOptions(begin.publicKey);
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("Passkey verification was cancelled.");
  return postWithState<T>(submitPath, begin.state, assertionToJSON(cred));
}
