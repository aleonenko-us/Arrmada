import { ApiError, req, type AuthUser } from "./api";

// Sign in with Jellyfin and your own Jellyfin link. These calls live here, not in api.ts,
// so they only download with the Jellyfin sign-in sheet and the account pages.
//
// The password goes up once, to the server, which hands it to Jellyfin and forgets it. A
// Quick Connect request's secret never comes to the browser at all: the server keeps it and
// gives back an id to poll and the code to show.

/** POST …/quickconnect: the code to type into a Jellyfin app, and the id to poll with. */
export interface QuickConnectStart {
  id: string;
  code: string;
}

/** GET /api/v1/me/jellyfin. */
export interface JellyfinLink {
  linked: boolean;
  jellyfin_username?: string;
  /** The account has a password or a Plex link, so unlinking leaves a way in. */
  can_unlink: boolean;
  /** A Jellyfin server is connected, so linking can work at all. */
  available: boolean;
  /** Linking can use Quick Connect. */
  quickconnect: boolean;
}

/** The 409 when the Jellyfin user belongs to another account. user_id and mergeable come
 *  to admins only, for a requester's link they may move here. */
export interface JellyfinLinkConflict {
  already_linked_to: string;
  user_id?: number;
  mergeable?: boolean;
}

export type LinkResult = { linked: true; jellyfin_username?: string } | { conflict: JellyfinLinkConflict };

const json = (body: unknown): RequestInit => ({ method: "POST", body: JSON.stringify(body) });

/** A link call's 409 is an answer (who has it), not a failure. */
async function linkCall(p: Promise<{ linked?: boolean; jellyfin_username?: string; pending?: boolean }>): Promise<LinkResult | null> {
  try {
    const r = await p;
    return r.linked ? { linked: true, jellyfin_username: r.jellyfin_username } : null;
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.body?.already_linked_to) return { conflict: e.body as unknown as JellyfinLinkConflict };
    throw e;
  }
}

export const jellyfinSignIn = {
  // The login page.
  login: (username: string, password: string) => req<{ user: AuthUser }>("/api/v1/auth/jellyfin", json({ username, password })),
  loginQCStart: () => req<QuickConnectStart>("/api/v1/auth/jellyfin/quickconnect", { method: "POST" }),
  /** null until the code is approved, then who signed in. */
  loginQCPoll: async (id: string) => (await req<{ pending?: boolean; user?: AuthUser }>(`/api/v1/auth/jellyfin/quickconnect/${encodeURIComponent(id)}`)).user ?? null,

  // Your own account.
  mine: () => req<JellyfinLink>("/api/v1/me/jellyfin"),
  link: (username: string, password: string) => linkCall(req("/api/v1/me/jellyfin/link", json({ username, password }))),
  linkQCStart: () => req<QuickConnectStart>("/api/v1/me/jellyfin/link/quickconnect", { method: "POST" }),
  /** null until the code is approved. */
  linkQCPoll: (id: string) => linkCall(req(`/api/v1/me/jellyfin/link/quickconnect/${encodeURIComponent(id)}`)),
  unlink: () => req<JellyfinLink>("/api/v1/me/jellyfin/link", { method: "DELETE" }),
};
