import { req, type UserRole } from "./api";

// Personal API keys: what someone makes on their Me page so a script (n8n, Home Assistant,
// curl) can call Arrmada as them, and the admin's list of everyone's in People & access.
// Not the outside services' keys (TMDB and the like) on Settings → Connections. These calls
// live here so they download with the Me page and People & access only, never with a
// requester's first load.

export type KeyScope = "read" | "full";

/** One key. Never the key itself: only its hint ("arrk_…7f3q"). */
export interface PersonalApiKey {
  id: number;
  name: string;
  hint: string;
  scope: KeyScope;
  /** The role it was made under; it never acts above it. */
  role: UserRole;
  created_at: string;
  last_used_at: string | null;
  /** "local", a coarse address like "203.0.113.x", or absent. Your own keys only. */
  last_used_network?: string;
  expires_at: string | null;
  expired: boolean;
  /** The admin's list: whose key it is. */
  user_id?: number;
  username?: string;
}

export interface MyApiKeys {
  keys: PersonalApiKey[];
  /** The lifetimes a key can be made with, in days; 0 is never. */
  expiry_days: number[];
  max: number;
  /** Making a key asks for your password (false: you sign in with Plex or Jellyfin only). */
  password_required: boolean;
}

export interface NewApiKey {
  name: string;
  scope: KeyScope;
  expires_in_days: number;
  password?: string;
}

export const apiKeysApi = {
  mine: () => req<MyApiKeys>("/api/v1/me/api-keys"),
  /** key is shown this once; only its hash is kept. */
  create: (body: NewApiKey) =>
    req<{ key: string; api_key: PersonalApiKey }>("/api/v1/me/api-keys", { method: "POST", body: JSON.stringify(body) }),
  revoke: (id: number) => req<void>(`/api/v1/me/api-keys/${id}`, { method: "DELETE" }),
  /** Admin: everyone's keys (whose and when, never from where). */
  all: () => req<{ keys: PersonalApiKey[] }>("/api/v1/users/api-keys").then((r) => r.keys ?? []),
  /** Admin: revoke anyone's key. */
  revokeAny: (id: number) => req<void>(`/api/v1/users/api-keys/${id}`, { method: "DELETE" }),
};

/** "Never", "in 12 days", "Expired" — when a key stops working. */
export function expiryLabel(k: Pick<PersonalApiKey, "expires_at" | "expired">, now = Date.now()): string {
  if (!k.expires_at) return "Never expires";
  if (k.expired) return "Expired";
  const days = Math.ceil((Date.parse(k.expires_at) - now) / 86_400_000);
  if (!(days > 0)) return "Expired";
  return days === 1 ? "Expires tomorrow" : `Expires in ${days} days`;
}

export const SCOPE_LABEL: Record<KeyScope, string> = { read: "Read only", full: "Full" };
