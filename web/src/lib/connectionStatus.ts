// Words and colours for a connection's card on Settings → Connections (see ConnectionItem).
import type { ConnectionItem, ConnectionState } from "./connectionsApi";
import { hhmm } from "./indexerStatus";
import { ago } from "./taskTime";

// The card's dot: green working, amber failing or worth a look, red paused after repeated
// failures, grey never checked, not set up or switched off. label is the dot's tooltip.
export const CONNECTION_DOT: Record<ConnectionState, { color: string; label: string }> = {
  ok: { color: "var(--good)", label: "Working" },
  failing: { color: "var(--avoid)", label: "Failing" },
  warn: { color: "var(--avoid)", label: "Needs a look" },
  backing_off: { color: "var(--reject)", label: "Paused after repeated failures" },
  unknown: { color: "var(--ink-faint)", label: "Not checked yet" },
  disabled: { color: "var(--ink-faint)", label: "Switched off" },
};

export function connectionDot(it: Pick<ConnectionItem, "state" | "configured">) {
  if (!it.configured && it.state !== "warn") return { color: "var(--ink-faint)", label: "Not set up" };
  return CONNECTION_DOT[it.state] ?? CONNECTION_DOT.unknown;
}

export interface StatusLine {
  text: string;
  color: string;
}

// connectionStatusLine is the card's one status line: when it last worked ("Last OK 3 min
// ago"), or since when it has been failing, why, and when it's next tried ("Failing since
// 14:02: login failed · next try 15:00").
export function connectionStatusLine(it: ConnectionItem, now = Date.now()): StatusLine {
  const dim = "var(--ink-dim)";
  if (it.state === "disabled") return { text: "Switched off", color: dim };
  if (!it.configured) return { text: "Not set up", color: dim };
  if (it.state === "failing" || it.state === "backing_off") {
    let text = `Failing since ${hhmm(it.failing_since ?? it.last_error_at)}`;
    if (!it.failing_since && !it.last_error_at) text = "Failing";
    if (it.last_error) text += `: ${it.last_error}`;
    if (it.state === "backing_off" && it.backoff_until) text += ` · next try ${hhmm(it.backoff_until)}`;
    return { text, color: it.state === "backing_off" ? "var(--reject-text)" : "var(--avoid-text)" };
  }
  if (it.last_ok_at) return { text: `Last OK ${ago(it.last_ok_at, now)}`, color: it.state === "warn" ? "var(--avoid-text)" : dim };
  if (it.state === "ok") return { text: "Working", color: dim };
  return { text: it.kind === "indexer" ? "Not used yet" : "Not checked yet", color: it.state === "warn" ? "var(--avoid-text)" : dim };
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// countsLine is an indexer's last 24 hours: "214 searches · 3 grabs · 3 failed".
export function countsLine(c: ConnectionItem["counts"]): string | null {
  if (!c || (c.queries_24h === 0 && c.grabs_24h === 0)) return null;
  const parts = [plural(c.queries_24h, "search", "searches"), plural(c.grabs_24h, "grab")];
  if (c.failures_24h > 0) parts.push(`${c.failures_24h} failed`);
  return `${parts.join(" · ")} (24h)`;
}

// Kind labels for the mono tag: the indexer or client type where there is one.
const KIND_LABEL: Record<string, string> = {
  prowlarr: "indexer manager",
  flaresolverr: "cloudflare",
  apikey: "api key",
  plex: "media server",
  notifications: "apprise",
  audioserver: "listening apps",
};

export function kindLabel(it: Pick<ConnectionItem, "kind" | "type">): string {
  return it.type || KIND_LABEL[it.kind] || it.kind;
}

// needsFix: a card whose primary action is Fix rather than Test.
export function needsFix(it: ConnectionItem): boolean {
  return it.configured && it.enabled && (it.state === "failing" || it.state === "backing_off" || it.state === "warn");
}
