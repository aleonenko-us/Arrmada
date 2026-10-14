// lanOnly says whether an address can only be reached from home: localhost, a private or
// link-local IP, a .local / .lan / .home.arpa / .internal name, or a bare name with no dots
// (a container name). Watch links sent to a phone on mobile data can't open those.
// A blank or unreadable address isn't flagged: there's nothing to warn about yet.
export function lanOnly(address: string): boolean {
  const raw = address.trim();
  if (!raw) return false;
  let host: string;
  try {
    host = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `http://${raw}`).hostname.toLowerCase();
  } catch {
    return false;
  }
  if (!host) return false;
  if (host.startsWith("[")) return privateV6(host.slice(1, -1));
  const v4 = host.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (v4) return privateV4(v4.slice(1).map(Number));
  if (host === "localhost" || host.endsWith(".localhost")) return true;
  if (/\.(local|lan|home\.arpa|internal|localdomain)$/.test(host)) return true;
  return !host.includes(".");
}

function privateV4([a, b]: number[]): boolean {
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || a === 0;
}

function privateV6(h: string): boolean {
  if (h === "::1" || h === "::") return true;
  // fc00::/7 (unique local) and fe80::/10 (link-local).
  return /^f[cd][0-9a-f]{0,2}:/.test(h) || /^fe[89ab][0-9a-f]?:/.test(h);
}
