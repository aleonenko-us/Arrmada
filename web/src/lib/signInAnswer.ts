import type { AuthUser, LoginAnswer } from "./api";

// What a Plex PIN or Jellyfin Quick Connect poll answers: pending until approved, then the
// signed-in user, or (a staff account with two-factor sign-in on, or an admin required to
// set it up) the ticket for the code step, as a password sign-in answers.
export type PolledAnswer = { pending?: boolean; user?: AuthUser; two_factor?: "code" | "setup"; ticket?: string };

/** The sign-in a poll finished with, or null while it's still pending. */
export function answered(r: PolledAnswer): LoginAnswer | null {
  if (r.two_factor && r.ticket) return { two_factor: r.two_factor, ticket: r.ticket };
  return r.user ? { user: r.user } : null;
}
