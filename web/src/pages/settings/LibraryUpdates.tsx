import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { AutoToggle } from "../../components/settings/auto";
import { Note, Section, input, inputStyle } from "../../components/settings/ui";
import { SERVER_LABEL, type MediaServerKind } from "../../components/brand/servers";
import { useAutoSave } from "../../lib/useAutoSave";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { LINKS } from "../../lib/links";
import { jellyfinApi } from "../../lib/jellyfinApi";
import { plexLibraryApi, type PlexPathMap, type PlexScanRoot, type PlexScanStatus, type PlexScanView } from "../../lib/plexLibraryApi";
import { ago } from "../../lib/taskTime";
import { Button } from "../../ui";

// A media server's "library updates" card: after Arrmada imports, upgrades, renames,
// deletes or converts a file it asks the server (Plex or Jellyfin) to scan just that
// folder. Each library folder's line shows where its scans go on the server's side and how
// that was worked out, so a wrong guess is visible. Self-contained (its own load and
// save); it sits in the server's group on Settings → Media servers. The on/off switch
// saves itself; path mappings are folder paths, so they keep an explicit Save. Both
// servers' routes answer the same shapes (plex_path is the folder on the server's side).

interface ScanApi {
  scanView: () => Promise<PlexScanView>;
  saveScan: (body: { enabled: boolean; path_map: PlexPathMap[] }) => Promise<PlexScanView>;
  testScan: (kind: "movie" | "show", run: boolean) => Promise<{ root: PlexScanRoot; last_scan: PlexScanStatus }>;
}

const SCAN_API: Record<MediaServerKind, ScanApi> = { plex: plexLibraryApi, jellyfin: jellyfinApi };
const CONNECTION: Record<MediaServerKind, string> = { plex: LINKS.plexConnection, jellyfin: LINKS.jellyfinConnection };
// Where the server keeps the same folder, for the mapping's placeholder.
const SERVER_FOLDER: Record<MediaServerKind, string> = { plex: "/data/media/movies", jellyfin: "/media/movies" };

export const libraryUpdatesId = (server: MediaServerKind) => `${server}-library-updates`;

const KIND_LABEL: Record<string, string> = { movie: "Movies", show: "TV" };

function howLabel(r: PlexScanRoot, name: string): string {
  switch (r.how) {
    case "direct": return "same path";
    case "mapped": return "your mapping";
    case "guessed": return "found automatically";
    case "section": return "whole library";
    case "none": return `no ${name} library`;
    default: return "";
  }
}

function RootLine({ r, name, busy, onTest }: { r: PlexScanRoot; name: string; busy: string; onTest: (kind: "movie" | "show", run: boolean) => void }) {
  const label = KIND_LABEL[r.kind] ?? r.kind;
  if (!r.arrmada_root) {
    return <div className="text-sm text-ink-faint">{label}: no folder set</div>;
  }
  const target = r.how === "section" || !r.plex_path ? null : r.plex_path;
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
        <span className="font-semibold">{label}:</span>
        <span className="font-mono text-sm">{r.arrmada_root}</span>
        {r.how && r.how !== "none" && (
          <>
            <span className="text-ink-faint">→</span>
            <span className="font-mono text-sm" style={{ color: "var(--accent-text)" }}>{target ?? "the whole library"}</span>
            <span className="text-ink-faint">
              · {r.sections.length === 1 ? "library" : "libraries"} {r.sections.join(", ")} ({howLabel(r, name)})
            </span>
          </>
        )}
        <span className="ml-auto flex gap-1.5">
          <Button size="sm" busy={busy === `${r.kind}:test`} onClick={() => onTest(r.kind, false)}>Test</Button>
          <Button size="sm" busy={busy === `${r.kind}:run`} busyLabel="Scanning…" onClick={() => onTest(r.kind, true)}>Scan now</Button>
        </span>
      </div>
      {r.how === "section" && (
        <Note tone="warn">
          Arrmada couldn't match {r.arrmada_root} to a folder {name} knows, so each change scans the whole {r.sections.join(", ")} library.
          That works, but it's slow on a big library: add a path mapping below.{r.note ? ` (${r.note})` : ""}
        </Note>
      )}
      {r.how === "none" && <Note tone="warn">{r.note || `${name} has no ${label} library.`}</Note>}
    </div>
  );
}

export function LibraryUpdates({ server }: { server: MediaServerKind }) {
  const name = SERVER_LABEL[server];
  const scan = SCAN_API[server];
  const id = libraryUpdatesId(server);
  const title = `${name} library updates`;
  const [view, setView] = useState<PlexScanView | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [maps, setMaps] = useState<PlexPathMap[]>([]);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState<{ tone: "ok" | "err"; text: string } | null>(null);

  const apply = (v: PlexScanView) => {
    setView(v);
    setMaps(v.path_map);
    setDirty(false);
  };
  useEffect(() => {
    scan.scanView().then(apply).catch((e: Error) => setLoadErr(e.message));
  }, [scan]);
  // The switch sends the saved mappings with it, and keeps any mapping edits in progress.
  const enabled = useAutoSave(view?.enabled ?? true, async (on) => {
    if (!view) return;
    const v = await scan.saveScan({ enabled: on, path_map: view.path_map });
    setView(v);
  });
  useUnsavedGuard(dirty);

  if (loadErr) return <Section id={id} title={title} subtitle={`Tell ${name} what changed.`}><Note tone="warn">{loadErr}</Note></Section>;
  if (!view) return <Section id={id} title={title} subtitle={`Tell ${name} what changed.`}><div className="text-sm text-ink-faint">Loading…</div></Section>;

  const edit = (next: PlexPathMap[]) => { setMaps(next); setDirty(true); };
  const save = async () => {
    setBusy("save");
    setMsg(null);
    try {
      apply(await scan.saveScan({ enabled: view.enabled, path_map: maps.filter((m) => m.from.trim() || m.to.trim()) }));
      setMsg({ tone: "ok", text: "Saved." });
    } catch (e) {
      setMsg({ tone: "err", text: (e as Error).message });
    } finally {
      setBusy("");
    }
  };
  const test = async (kind: "movie" | "show", run: boolean) => {
    setBusy(`${kind}:${run ? "run" : "test"}`);
    setMsg(null);
    try {
      const res = await scan.testScan(kind, run);
      setView((v) => v && { ...v, roots: v.roots.map((r) => (r.kind === kind ? res.root : r)), last_scan: run ? res.last_scan : v.last_scan });
      setMsg({ tone: "ok", text: run ? `Asked ${name} to scan ${res.root.plex_path || "the whole library"}.` : `${KIND_LABEL[kind]} resolves to ${res.root.plex_path || "the whole library"} (${howLabel(res.root, name)}).` });
    } catch (e) {
      setMsg({ tone: "err", text: (e as Error).message });
    } finally {
      setBusy("");
    }
  };

  const last = view.last_scan;
  return (
    <Section
      id={id}
      title={title}
      subtitle={`After Arrmada imports, upgrades, renames, deletes or converts a file, it asks ${name} to scan just that folder, so the change shows up in ${name} within a minute. Arrmada's folders usually have different names inside ${name}'s container; each line shows where its scans go.`}
    >
      {!view.configured && (
        <Note tone="info">
          Connect your {name} server first, on its <Link to={CONNECTION[server]} style={{ color: "var(--accent-text)" }}>card in Settings → Connections</Link>. Until then nothing is sent.
        </Note>
      )}
      <AutoToggle
        label={`Tell ${name} to scan after changes`}
        hint={`One quick scan of the changed folder, a few seconds after things settle. Off leaves it to ${name}'s own watcher and scheduled scans.`}
        save={enabled}
      />
      {view.configured && view.error && <Note tone="warn">Couldn't read {name}'s libraries: {view.error}</Note>}
      {view.configured && !view.error && (
        <div className="flex flex-col gap-3">
          {view.roots.map((r) => <RootLine key={r.kind} r={r} name={name} busy={busy} onTest={test} />)}
        </div>
      )}

      <div className="flex flex-col gap-2">
        <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Path mappings</span>
        <span className="text-xs text-ink-faint">Only needed when a line above says "whole library" or points somewhere wrong. The Arrmada folder is as Arrmada sees it; the {name} folder is the same place as {name} sees it (for example {SERVER_FOLDER[server]}, or D:\Media\Movies on Windows).</span>
        {maps.map((m, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <input aria-label="Arrmada folder" placeholder="/movies" value={m.from} onChange={(e) => edit(maps.map((x, j) => (j === i ? { ...x, from: e.target.value } : x)))} className={`${input} min-w-0 flex-1`} style={inputStyle} />
            <span className="text-ink-faint">→</span>
            <input aria-label={`${name} folder`} placeholder={SERVER_FOLDER[server]} value={m.to} onChange={(e) => edit(maps.map((x, j) => (j === i ? { ...x, to: e.target.value } : x)))} className={`${input} min-w-0 flex-1`} style={inputStyle} />
            <Button size="sm" variant="ghost" onClick={() => edit(maps.filter((_, j) => j !== i))}>Remove</Button>
          </div>
        ))}
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => edit([...maps, { from: "", to: "" }])}>Add mapping</Button>
          <Button size="sm" variant="primary" disabled={!dirty} busy={busy === "save"} busyLabel="Saving…" onClick={save}>Save mappings</Button>
        </div>
      </div>

      <div className="text-sm text-ink-dim">
        {last ? (
          last.error
            ? <span style={{ color: "var(--reject-text)" }}>Last scan {ago(last.at)} failed: {last.error}</span>
            : <>Last scan: {ago(last.at)} · <span className="font-mono">{last.path || "whole library"}</span>{last.section ? ` · ${last.section}` : ""}</>
        ) : "No scans asked for since Arrmada started."}
        {view.pending > 0 && ` · ${view.pending} waiting`}
      </div>
      {msg && <div className="text-sm" style={{ color: msg.tone === "err" ? "var(--reject-text)" : "var(--good-text)" }}>{msg.text}</div>}
    </Section>
  );
}
