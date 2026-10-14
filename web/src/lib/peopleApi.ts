import { req, type AuthUser, type AutoApproveFlags, type JellyfinBlock, type Person, type PlexBlock, type UserImpact, type UserQuota, type UserRole } from "./api";

// People & access calls (accounts, roles, Plex and Jellyfin blocks). They live here rather
// than in api.ts so they only download with the staff pages that use them, not with a
// requester's first load.
export const peopleApi = {
  users: () => req<{ users: AuthUser[] }>("/api/v1/users").then((r) => r.users),
  people: () => req<{ people: Person[] }>("/api/v1/people").then((r) => r.people),
  // auto_approve_<type> sets one type; the old auto_approve sets all three.
  createUser: (body: { email: string; password: string; role: string } & AutoApproveFlags) =>
    req<AuthUser>("/api/v1/users", { method: "POST", body: JSON.stringify(body) }),
  // disabled: true turns off their sign-in and signs them out everywhere; nothing is deleted.
  updateUser: (id: number, body: { role?: string; auto_approve?: boolean; password?: string; disabled?: boolean; quota?: UserQuota; plex_unlink?: boolean; jellyfin_unlink?: boolean } & AutoApproveFlags) =>
    req<{ id: number; role: UserRole; auto_approve: boolean; disabled: boolean } & Required<AutoApproveFlags>>(`/api/v1/users/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  userImpact: (id: number) => req<UserImpact>(`/api/v1/users/${id}/impact`),
  // confirm is the username, required by the server when the user has listening data.
  // block.plex / block.jellyfin also block their Plex account / Jellyfin user, so they
  // can't come straight back by signing in with it.
  deleteUser: (id: number, confirm?: string, block: { plex?: boolean; jellyfin?: boolean } = {}) => {
    const q = new URLSearchParams();
    if (confirm) q.set("confirm", confirm);
    if (block.plex) q.set("block_plex", "1");
    if (block.jellyfin) q.set("block_jellyfin", "1");
    const qs = q.toString();
    return req<void>(`/api/v1/users/${id}${qs ? `?${qs}` : ""}`, { method: "DELETE" });
  },
  plexBlocks: () => req<{ blocks: PlexBlock[] }>("/api/v1/users/plex-blocks").then((r) => r.blocks),
  blockUserPlex: (id: number) => req<{ blocks: PlexBlock[] }>(`/api/v1/users/${id}/block-plex`, { method: "POST" }).then((r) => r.blocks),
  unblockPlex: (plexID: string) =>
    req<{ blocks: PlexBlock[] }>(`/api/v1/users/plex-blocks/${encodeURIComponent(plexID)}`, { method: "DELETE" }).then((r) => r.blocks),
  jellyfinBlocks: () => req<{ blocks: JellyfinBlock[] }>("/api/v1/users/jellyfin-blocks").then((r) => r.blocks),
  blockUserJellyfin: (id: number) => req<{ blocks: JellyfinBlock[] }>(`/api/v1/users/${id}/block-jellyfin`, { method: "POST" }).then((r) => r.blocks),
  unblockJellyfin: (jellyfinID: string) =>
    req<{ blocks: JellyfinBlock[] }>(`/api/v1/users/jellyfin-blocks/${encodeURIComponent(jellyfinID)}`, { method: "DELETE" }).then((r) => r.blocks),
};
