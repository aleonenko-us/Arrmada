import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { ConnectionCard, type CardFix, type CardTest } from "../../components/connections/ConnectionCard";
import { ConnectionSection } from "../../components/connections/ConnectionSection";
import { connectionsApi, type ConnectionItem, type ConnectionSectionData, type ConnectionSectionId } from "../../lib/connectionsApi";
import { LINKS } from "../../lib/links";
import { useQuery } from "../../lib/query";
import { useLive } from "../../lib/useLive";
import { usePoll } from "../../lib/usePoll";
import { ErrorState, Skeleton, StaleBanner } from "../../ui";

const NO_SECTIONS: ConnectionSectionData[] = [];

// What each section holds, under its title.
const SUBTITLES: Record<ConnectionSectionId, string> = {
  search: "Where releases are found: Prowlarr, FlareSolverr for Cloudflare-protected trackers, and every indexer.",
  downloads: "Where grabbed releases are sent to download.",
  metadata: "Keys for the services titles, ratings, books and subtitles come from.",
  other: "Plex, where alerts go, and the server listening apps connect to.",
};

// Each section's anchor. They differ from the API's section ids where those would clash
// with another card's anchor ("downloads" is a section, "metadata" a card under Naming).
const SECTION_ANCHOR: Record<ConnectionSectionId, string> = {
  search: "search",
  downloads: "download-clients",
  metadata: "api-keys",
  other: "other-connections",
};

// Live refresh: a status change is announced on the websocket; the list is read again a
// second after the last one, so a burst (three sweeps at once) is one read. A slow poll
// covers a dropped socket.
const SLOW_POLL_MS = 60_000;

// Settings → Connections: every integration Arrmada talks to as a card with a status dot,
// its last success or error, and a Test. The list comes from what the server already knows,
// so opening the page calls nothing outside Arrmada; Test asks the one connection.
export function ConnectionsSettings() {
  const q = useQuery("connections", () => connectionsApi.list(), { staleMs: 0 });
  const sections = q.data ?? NO_SECTIONS;
  const error = q.error?.message ?? null;
  const [tests, setTests] = useState<Record<string, CardTest>>({});
  const { hash } = useLocation();
  const refetch = q.refetch;

  const { last } = useLive();
  useEffect(() => {
    if (last?.topic !== "integration.status" && last?.topic !== "health.changed") return;
    const t = window.setTimeout(() => void refetch(), 1000);
    return () => window.clearTimeout(t);
    // Keyed on the event alone: refetch changes identity as the list loads.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [last]);
  usePoll(() => refetch(), SLOW_POLL_MS, { immediate: false });

  // A link to one card (a health warning's Fix) lands on it once the cards exist; the
  // hub's own scroll ran before they loaded.
  const scrolled = useRef("");
  useEffect(() => {
    if (!q.data || !hash || scrolled.current === hash) return;
    scrolled.current = hash;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [q.data, hash]);

  const runTest = async (it: ConnectionItem) => {
    setTests((t) => ({ ...t, [it.card]: { running: true } }));
    try {
      const r = await connectionsApi.test(it.kind, it.ref);
      setTests((t) => ({ ...t, [it.card]: { ok: r.ok, detail: r.detail } }));
      // The answer carries the refreshed card: update it in place.
      q.mutate((old) => (old ?? []).map((s) => ({ ...s, items: s.items.map((x) => (x.card === it.card ? r.item : x)) })));
    } catch (e) {
      setTests((t) => ({ ...t, [it.card]: { ok: false, detail: (e as Error).message } }));
    }
  };

  if (!q.data) {
    return error ? <ErrorState what="connections" message={error} onRetry={refetch} busy={q.loading} /> : <Skeleton variant="list" count={4} />;
  }

  return (
    <div className="flex flex-col gap-8">
      {error && <StaleBanner message={error} onRetry={refetch} />}
      {sections.map((s) => (
        <ConnectionSection key={s.id} id={SECTION_ANCHOR[s.id] ?? s.id} title={s.title} subtitle={SUBTITLES[s.id] ?? ""}>
          {s.items.length === 0 ? (
            <p className="m-0 text-[12px] text-ink-dim">{s.id === "downloads" ? "No download client yet." : "Nothing here yet."}</p>
          ) : (
            s.items.map((it) => (
              <ConnectionCard
                key={it.card}
                item={it}
                test={tests[it.card]}
                onTest={() => void runTest(it)}
                fix={fixFor(it)}
                targeted={hash === `#${it.card}`}
              />
            ))
          )}
        </ConnectionSection>
      ))}
    </div>
  );
}

// fixFor is where a card's Fix (or Set up) goes: the page that edits it today.
function fixFor(it: ConnectionItem): CardFix | undefined {
  switch (it.kind) {
    case "indexer":
    case "prowlarr":
      return { to: LINKS.indexers };
    case "download_client":
      return { to: LINKS.downloadClients };
    case "apikey":
    case "flaresolverr":
      return { to: LINKS.apiKeys };
  }
  return it.fix_path ? { to: it.fix_path } : undefined;
}
