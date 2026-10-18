// sameEndpoint mirrors the server's rule (internal/netsafe SameEndpoint): a saved key or
// password is only sent back to the address it was saved for. Two addresses are the same
// when scheme, host, port and path match once normalised (case, a default port, a
// trailing slash; the query and fragment don't count). Edit forms use it to ask for the
// secret again as soon as the address changes, rather than after a refused save.
export function sameEndpoint(a: string, b: string): boolean {
  const na = normalize(a);
  const nb = normalize(b);
  if (na === null || nb === null) return a.trim() === b.trim();
  return na === nb;
}

function normalize(raw: string): string | null {
  const s = raw.trim();
  if (s === "") return "";
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return null;
  }
  if (!u.host) return null;
  // URL already lower-cases the scheme and host and drops a default port.
  const user = u.username || u.password ? `${u.username}:${u.password}@` : "";
  return `${u.protocol}//${user}${u.host}${u.pathname.replace(/\/+$/, "")}`;
}

// secretMoved: a secret is saved, none is typed, and the address now differs from the one
// it was saved with — the form should ask for the secret again.
export function secretMoved(opts: { saved: boolean; typed: string; url: string; savedUrl: string }): boolean {
  return opts.saved && opts.typed.trim() === "" && !sameEndpoint(opts.url, opts.savedUrl);
}
