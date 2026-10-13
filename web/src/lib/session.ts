import { api, type UserRole } from "./api";

// Where to go back to after signing in again. When a session ends mid-use, the page the
// person was on is remembered here, so signing back in returns them to it instead of
// dropping them on Discover.

const KEY = "arrmada.next";

// signOut ends this session and reloads at the root, so every bit of the signed-in app
// (router, caches, polls) starts over at the sign-in screen.
export async function signOut(): Promise<void> {
  try { await api.logout(); } catch { /* clearing the session locally is enough */ }
  window.location.href = "/";
}

// sanitizeNext keeps only a same-site path: it must start with a single "/" (not "//" or
// "/\", which browsers treat as another host), and carry no scheme and no control
// characters (browsers drop tabs and newlines, so "/\t/evil" would become "//evil").
// Anything else is null, so a crafted ?next= can never send someone off-site.
export function sanitizeNext(s: string | null | undefined): string | null {
  if (!s) return null;
  if (!s.startsWith("/") || s.startsWith("//") || s.startsWith("/\\")) return null;
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(s)) return null;
  if (s.includes("://")) return null;
  return s;
}

// rememberNext stores the current page (path + query + hash) for after the next sign-in.
export function rememberNext(): void {
  try {
    const here = window.location.pathname + window.location.search + window.location.hash;
    const ok = sanitizeNext(here);
    if (ok) sessionStorage.setItem(KEY, ok);
  } catch {
    /* storage blocked (private mode): fall back to Discover */
  }
}

// takeNext returns where to go after signing in — ?next= on the URL first, then the page
// remembered when the session ended — and forgets it, so it's used once.
export function takeNext(): string | null {
  let stored: string | null = null;
  try {
    stored = sessionStorage.getItem(KEY);
    sessionStorage.removeItem(KEY);
  } catch {
    /* storage blocked */
  }
  let fromURL: string | null = null;
  try {
    fromURL = new URLSearchParams(window.location.search).get("next");
  } catch {
    /* no location (tests) */
  }
  return sanitizeNext(fromURL) ?? sanitizeNext(stored);
}

// homeFor is where someone lands when nothing asked for a page: staff on the Dashboard
// (Needs you, and Getting started on a fresh install), everyone else on Discover.
export function homeFor(role?: UserRole | null): string {
  return role === "admin" || role === "manager" ? "/" : "/discover";
}

// askedFor is the page this sign-in should return to, if anything asked for one:
// takeNext(), else the deep link the login screen was shown on (a reload or a resumed app
// keeps its URL).
function askedFor(): string | null {
  const next = takeNext();
  if (next) return next;
  try {
    const { pathname, search, hash } = window.location;
    if (pathname !== "/") {
      const q = new URLSearchParams(search);
      q.delete("next");
      const qs = q.toString();
      const here = sanitizeNext(pathname + (qs ? `?${qs}` : "") + hash);
      if (here) return here;
    }
  } catch {
    /* no location */
  }
  return null;
}

// nextAfterSignIn is where a successful sign-in goes: the page asked for, else the signed-in
// person's home (homeFor).
export function nextAfterSignIn(role?: UserRole | null): string {
  return askedFor() ?? homeFor(role);
}

// keepNextAcrossRedirect is for a sign-in that leaves the page (Sign in with Plex by
// redirect, on a phone): it stores the page asked for now, so the page plex.tv sends back
// to still lands there. With nothing asked for it stores nothing: who signed in (and so
// their home) is only known once plex.tv sends them back.
export function keepNextAcrossRedirect(): void {
  const next = askedFor();
  if (!next) return;
  try {
    sessionStorage.setItem(KEY, next);
  } catch {
    /* storage blocked: their home it is */
  }
}
