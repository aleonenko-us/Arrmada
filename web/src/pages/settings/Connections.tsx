import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { CategoryLine, ClientEditor, ClientForm, clientBody } from "../../components/connections/ClientEditor";
import { ConnectionCard, type CardFix, type CardTest } from "../../components/connections/ConnectionCard";
import { ConnectionSection } from "../../components/connections/ConnectionSection";
import { AddIndexerForm, IndexerEditor, indexerBody } from "../../components/connections/IndexerEditor";
import { DiscoveryRegion, KeyEditor, keyRowsFor, useAPIKeys } from "../../components/connections/KeyEditor";
import { ProwlarrPanel } from "../../components/connections/ProwlarrPanel";
import { api, type DownloadClient, type Indexer } from "../../lib/api";
import { connectionsApi, type ConnectionItem, type ConnectionSectionData, type ConnectionSectionId } from "../../lib/connectionsApi";
import { isAdmin, useMe } from "../../lib/me";
import { useQuery } from "../../lib/query";
import { useLiveRefresh } from "../../lib/useLiveQuery";
import { Button, ErrorState, Skeleton, StaleBanner, useConfirm, type MenuItem } from "../../ui";

const NO_SECTIONS: ConnectionSectionData[] = [];

// What each section holds, under its title.
const SUBTITLES: Record<ConnectionSectionId, string> = {
  search: "Where releases are found: Prowlarr, FlareSolverr for Cloudflare-protected trackers, and every indexer. Every module searches through these.",
  downloads: "Where grabbed releases are sent to download. qBittorrent for torrents.",
  metadata: "The API keys titles, ratings, books and subtitles come from. A key saved here works at once, no restart, and replaces one set at install; it's never shown back, only whether it's set.",
  other: "Plex, where alerts go, and the server listening apps connect to. Each is edited on its own page.",
};

// Each section's anchor. They differ from the API's section ids where those would clash
// with another card's anchor ("downloads" is a section, "metadata" a card under Naming);
// the metadata keys keep the API keys card's old anchor, so old links still land on them.
const SECTION_ANCHOR: Record<ConnectionSectionId, string> = {
  search: "search",
  downloads: "download-clients",
  metadata: "api-keys",
  other: "other-connections",
};

// Live refresh: a status change is announced on the tab's shared websocket; the list is read
// again a second after the first one, so a burst (three sweeps at once) is one read. A slow
// poll covers a dropped socket.
const SLOW_POLL_MS = 60_000;

// Settings → Connections: every integration Arrmada talks to as a card with a status dot,
// its last success or error, and a Test. The list comes from what the server already knows,
// so opening the page calls nothing outside Arrmada; Test asks the one connection. Indexers,
// download clients and Prowlarr are added, edited, switched off and deleted here (the old
// Indexers and Download clients pages redirect here); admins edit the metadata keys and the
// FlareSolverr URL inside their cards. Plex, alerts and the audiobook server link to their
// own pages.
export function ConnectionsSettings() {
  const { user } = useMe();
  const admin = isAdmin(user);
  const confirm = useConfirm();
  const q = useQuery("connections", () => connectionsApi.list(), { staleMs: 0 });
  // The full rows the editors and the Disable/Delete actions need.
  const ix = useQuery("indexers", () => api.indexers(), { staleMs: 0 });
  const dl = useQuery("download-clients", () => api.downloadClientList(), { staleMs: 0 });
  const sections = q.data ?? NO_SECTIONS;
  const error = q.error?.message ?? null;
  const [tests, setTests] = useState<Record<string, CardTest>>({});
  const [notices, setNotices] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState<"indexer" | "client" | null>(null);
  const [restoring, setRestoring] = useState(false);
  const [restoreErr, setRestoreErr] = useState<string | null>(null);
  const { hash } = useLocation();

  const refetch = q.refetch;
  const refetchIx = ix.refetch;
  const refetchDl = dl.refetch;
  const refresh = useCallback(() => { void refetch(); }, [refetch]);
  const refreshAll = useCallback(async () => { await Promise.all([refetch(), refetchIx(), refetchDl()]); }, [refetch, refetchIx, refetchDl]);
  const keys = useAPIKeys(admin, refresh);

  // The tab's one shared socket (lib/live): an integration's status changing, or the
  // health checks' findings, re-read the list.
  useLiveRefresh(refetch, { topics: ["integration.status", "health.changed"], debounceMs: 1000, fallbackMs: SLOW_POLL_MS });

  // A link to one card (a health warning's Fix) lands on it once the cards exist; the
  // hub's own scroll ran before they loaded.
  const scrolled = useRef("");
  useEffect(() => {
    if (!q.data || !hash || scrolled.current === hash) return;
    scrolled.current = hash;
    document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
  }, [q.data, hash]);

  const setNotice = (card: string, msg: string | null) =>
    setNotices((n) => { const next = { ...n }; if (msg) next[card] = msg; else delete next[card]; return next; });

  const runTest = async (kind: ConnectionItem["kind"], ref: string, card: string) => {
    setTests((t) => ({ ...t, [card]: { running: true } }));
    try {
      const r = await connectionsApi.test(kind, ref);
      setTests((t) => ({ ...t, [card]: { ok: r.ok, detail: r.detail } }));
      // The answer carries the refreshed card: update it in place.
      q.mutate((old) => (old ?? []).map((s) => ({ ...s, items: s.items.map((x) => (x.card === card ? r.item : x)) })));
    } catch (e) {
      setTests((t) => ({ ...t, [card]: { ok: false, detail: (e as Error).message } }));
    }
  };

  // A saved client is tested at once, so a wrong URL or password shows now instead of at
  // the first grab.
  const clientSaved = (c: DownloadClient) => {
    setAdding(null);
    setEditing(null);
    void refreshAll().then(() => runTest("download_client", String(c.id), `download_client-${c.id}`));
  };

  const restoreBundled = async () => {
    setRestoring(true);
    setRestoreErr(null);
    try {
      await api.restoreBundledClient();
      await refreshAll();
    } catch (e) {
      setRestoreErr(`Couldn't restore it: ${(e as Error).message}`);
    } finally {
      setRestoring(false);
    }
  };

  if (!q.data) {
    return error ? <ErrorState what="connections" message={error} onRetry={refetch} busy={q.loading} /> : <Skeleton variant="list" count={4} />;
  }

  const indexerOf = (it: ConnectionItem) => ix.data?.find((i) => String(i.id) === it.ref);
  const clientOf = (it: ConnectionItem) => dl.data?.clients.find((c) => String(c.id) === it.ref);

  // Switching an indexer or client off or on saves at once; its key or password goes back
  // blank, which keeps the stored one. A Prowlarr row switched off here stays off through
  // syncs (the server marks it switched off by you).
  const toggleIndexer = async (it: ConnectionItem, idx: Indexer) => {
    setNotice(it.card, null);
    try {
      await api.updateIndexer(idx.id, indexerBody(idx, { enabled: !idx.enabled }));
      await refreshAll();
    } catch (e) {
      setNotice(it.card, `Couldn't ${idx.enabled ? "disable" : "enable"} it: ${(e as Error).message}`);
    }
  };
  const toggleClient = async (it: ConnectionItem, c: DownloadClient) => {
    setNotice(it.card, null);
    try {
      await api.updateDownloadClient(c.id, clientBody(c, { enabled: !c.enabled }));
      await refreshAll();
    } catch (e) {
      setNotice(it.card, `Couldn't ${c.enabled ? "disable" : "enable"} it: ${(e as Error).message}`);
    }
  };

  // Deleting asks first and says what happens to what's already there; a refusal from the
  // server shows on the card instead of vanishing.
  const removeIndexer = async (it: ConnectionItem, idx: Indexer) => {
    const ok = await confirm({
      title: `Delete ${idx.name}?`,
      body: "Searches stop using it. Seed rules on downloads already grabbed through it fall back to the 14-day default.",
      confirmLabel: "Delete",
      tone: "danger",
    });
    if (!ok) return;
    setNotice(it.card, null);
    try {
      await api.deleteIndexer(idx.id);
      if (editing === it.card) setEditing(null);
      await refreshAll();
    } catch (e) {
      setNotice(it.card, `Couldn't delete: ${(e as Error).message}`);
    }
  };
  const removeClient = async (it: ConnectionItem, c: DownloadClient) => {
    const ok = await confirm({
      title: `Remove ${c.name}?`,
      body: c.bundled
        ? "Arrmada stops sending downloads to it. Torrents already there are untouched. It stays removed after a restart; you can restore it from this page."
        : "Arrmada stops sending downloads to it. Torrents already there are untouched.",
      confirmLabel: "Remove",
      tone: "danger",
    });
    if (!ok) return;
    setNotice(it.card, null);
    try {
      await api.deleteDownloadClient(c.id);
      if (editing === it.card) setEditing(null);
      await refreshAll();
    } catch (e) {
      setNotice(it.card, `Couldn't remove: ${(e as Error).message}`);
    }
  };

  // The editor a card opens inside itself, if it has one for this viewer.
  const editorFor = (it: ConnectionItem): ReactNode | undefined => {
    const done = () => { setEditing(null); void refreshAll(); };
    switch (it.kind) {
      case "indexer": {
        const idx = indexerOf(it);
        return idx ? <IndexerEditor key={idx.id} idx={idx} onSaved={done} onChanged={refreshAll} /> : undefined;
      }
      case "download_client": {
        const c = clientOf(it);
        return c ? <ClientEditor key={c.id} client={c} onSaved={clientSaved} /> : undefined;
      }
      case "prowlarr":
        return <ProwlarrPanel onSynced={() => void refreshAll()} />;
      case "apikey":
        return admin ? <KeyEditor ids={keyRowsFor(it.ref)} ctl={keys} /> : undefined;
      case "flaresolverr":
        return admin ? <KeyEditor ids={["flaresolverr"]} ctl={keys} /> : undefined;
    }
    return undefined;
  };

  const menuFor = (it: ConnectionItem, open: boolean, toggle: () => void): MenuItem[] | undefined => {
    const idx = it.kind === "indexer" ? indexerOf(it) : undefined;
    const c = it.kind === "download_client" ? clientOf(it) : undefined;
    const row = idx ?? c;
    if (!row) return undefined;
    return [
      { label: open ? "Close editor" : "Edit", onSelect: toggle },
      {
        label: row.enabled ? "Disable" : "Enable",
        busyLabel: row.enabled ? "Disabling…" : "Enabling…",
        onSelect: () => (idx ? toggleIndexer(it, idx) : toggleClient(it, c as DownloadClient)),
      },
      { label: "Delete", tone: "danger", onSelect: () => void (idx ? removeIndexer(it, idx) : removeClient(it, c as DownloadClient)) },
    ];
  };

  const card = (it: ConnectionItem) => {
    const editor = editorFor(it);
    const open = editing === it.card && !!editor;
    const toggle = () => setEditing(open ? null : it.card);
    return (
      <ConnectionCard
        key={it.card}
        item={it}
        test={tests[it.card]}
        onTest={() => void runTest(it.kind, it.ref, it.card)}
        fix={editor ? { onClick: () => setEditing(it.card) } : linkFor(it)}
        onEdit={editor ? toggle : undefined}
        editing={open}
        menu={menuFor(it, open, toggle)}
        notice={notices[it.card]}
        targeted={hash === `#${it.card}`}
      >
        {open && editor}
      </ConnectionCard>
    );
  };

  const headerActions = (id: ConnectionSectionId): ReactNode => {
    switch (id) {
      case "search":
        return (
          <Button size="sm" variant={adding === "indexer" ? "secondary" : "primary"} onClick={() => setAdding(adding === "indexer" ? null : "indexer")}>
            {adding === "indexer" ? "Cancel" : "+ Add indexer"}
          </Button>
        );
      case "downloads":
        return (
          <>
            {dl.data?.can_restore_bundled && (
              <Button size="sm" variant="secondary" onClick={restoreBundled} busy={restoring} busyLabel="Restoring…">Restore bundled qBittorrent</Button>
            )}
            <Button size="sm" variant={adding === "client" ? "secondary" : "primary"} onClick={() => setAdding(adding === "client" ? null : "client")}>
              {adding === "client" ? "Cancel" : "+ Add client"}
            </Button>
          </>
        );
    }
    return undefined;
  };

  const above = (id: ConnectionSectionId): ReactNode => {
    if (id === "search" && adding === "indexer") return <AddIndexerForm onAdded={() => { setAdding(null); void refreshAll(); }} />;
    if (id === "downloads") {
      return (
        <>
          {restoreErr && <p role="alert" className="m-0 text-[12px]" style={{ color: "var(--reject)" }}>{restoreErr}</p>}
          {dl.data?.can_restore_bundled && (
            <p className="m-0 text-[12px] text-ink-dim">The qBittorrent that comes with Arrmada was removed, so it gets no downloads.</p>
          )}
          {adding === "client" && <ClientForm onSaved={clientSaved} />}
        </>
      );
    }
    return undefined;
  };

  const below = (id: ConnectionSectionId): ReactNode => {
    if (id === "downloads" && dl.data?.categories) return <CategoryLine c={dl.data.categories} />;
    if (id === "metadata" && admin) return <DiscoveryRegion />;
    return undefined;
  };

  return (
    <div className="flex flex-col gap-8">
      {error && <StaleBanner message={error} onRetry={refetch} />}
      {sections.map((s) => (
        <ConnectionSection
          key={s.id}
          id={SECTION_ANCHOR[s.id] ?? s.id}
          title={s.title}
          subtitle={SUBTITLES[s.id] ?? ""}
          actions={headerActions(s.id)}
          above={above(s.id)}
          below={below(s.id)}
        >
          {s.items.length === 0 ? (
            <p className="m-0 text-[12px] text-ink-dim">
              {s.id === "downloads" ? "No download clients yet. Add qBittorrent to start downloading." : "Nothing here yet."}
            </p>
          ) : (
            s.items.map(card)
          )}
        </ConnectionSection>
      ))}
    </div>
  );
}

// linkFor is where a card that isn't edited here goes: Plex, alerts and the audiobook
// server have their own pages. A manager's key cards have none — keys are the admin's.
function linkFor(it: ConnectionItem): CardFix | undefined {
  return it.fix_path ? { to: it.fix_path } : undefined;
}
