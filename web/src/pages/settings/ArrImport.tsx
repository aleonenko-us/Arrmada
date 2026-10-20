import { useState } from "react";
import { Link } from "react-router-dom";
import { Note, Section, input, inputStyle } from "../../components/settings/ui";
import { foldersChanged, useLibraryFolders } from "../../components/FolderSelect";
import { Field } from "../../ui/Field";
import { Switch } from "../../ui/Switch";
import { Button, useToast } from "../../ui";
import { Tabs, TabPanel } from "../../ui/Tabs";
import { LINKS } from "../../lib/links";
import { fmtBytes } from "../../lib/disposal";
import { foldersApi, type ArrRoot, type FolderMedia, type LibraryFolder } from "../../lib/foldersApi";
import { arrImportApi, type ArrApp, type ArrImportChoices, type ArrPathMap, type ArrPreview, type ArrResult, type ArrSetup, type ArrSetupItem } from "../../lib/importApi";
import { jobFailed, useJob } from "../../lib/useJob";
import { FolderPicker } from "../Library";
import { arrCountsLine, arrResultLine, notesByOutcome, setupLine } from "./arrImportText";

// ArrImport brings a Radarr's or Sonarr's library over: connect, look at what would come over
// (folders, quality profiles, where the files are), then import in the background. It only
// reads Radarr or Sonarr, and the API key stays in this page — it goes with each request and
// is never saved. Several instances (Sonarr and Sonarr-anime, Radarr and Radarr-4K) are
// imported one after the other; a second run adds only what's new.

// An import of thousands of titles runs a long while; the card keeps following it.
const FOLLOW_MS = 6 * 60 * 60 * 1000;

export function ArrImport() {
  const toast = useToast();
  const [app, setApp] = useState<ArrApp>("radarr");
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pv, setPv] = useState<ArrPreview | null>(null);
  // The person's choices, filled from the preview's suggestions.
  const [profiles, setProfiles] = useState<Record<number, string>>({});
  const [folders, setFolders] = useState<Record<string, number>>({});
  const [maps, setMaps] = useState<ArrPathMap[]>([]);
  const [importMissing, setImportMissing] = useState(false);
  // What to bring over: the library, the indexers and download clients, or both.
  const [withTitles, setWithTitles] = useState(true);
  const [withSetup, setWithSetup] = useState(true);
  const [starting, setStarting] = useState(false);
  const [jobId, setJobId] = useState<number | null>(null);
  const [result, setResult] = useState<ArrResult | null>(null);
  const job = useJob(jobId, {
    giveUpMs: FOLLOW_MS,
    onDone: (j) => {
      setJobId(null);
      foldersChanged();
      if (j.result) setResult(j.result as ArrResult);
      if (jobFailed(j) && !j.result) toast(j.error || "The import didn't finish.", { tone: "error", ms: 8000 });
    },
  });
  const running = job.running || starting;

  const choices = (withMaps: boolean): ArrImportChoices => ({
    profiles, folders, import_missing: importMissing, import_setup: withSetup, skip_titles: !withTitles, path_maps: withMaps ? maps.filter((m) => m.from.trim() || m.to.trim()) : undefined,
  });

  // connect reads the app the first time (suggested folders, maps and profiles); check reads
  // it again with the person's choices, to see what the path maps now find.
  const load = async (first: boolean) => {
    setBusy(true); setError(null);
    if (first) { setPv(null); setResult(null); }
    try {
      const p = await arrImportApi.preview(app, url.trim(), key.trim(), first ? {} : choices(true));
      setPv(p);
      if (first) {
        setProfiles(Object.fromEntries(p.preview.profiles.map((x) => [x.id, x.suggested])));
        setFolders(Object.fromEntries(p.roots.filter((r) => r.folder_id !== null).map((r) => [r.path, r.folder_id as number])));
        setMaps(p.path_maps.length ? p.path_maps : [{ from: "", to: "" }]);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const start = async () => {
    setStarting(true); setResult(null);
    try {
      const r = await arrImportApi.run(app, url.trim(), key.trim(), choices(true));
      if (r.job_id) setJobId(r.job_id);
      toast(`Importing from ${r.name} in the background — nothing is searched for now; wanted titles are searched a few at a time.`, { tone: "good", ms: 7000 });
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 8000 });
    } finally {
      setStarting(false);
    }
  };

  const reset = () => { setPv(null); setResult(null); setKey(""); setUrl(""); setError(null); };
  const media: FolderMedia = pv?.preview.media ?? (app === "sonarr" ? "series" : "movie");
  const noun = media === "series" ? "shows" : "films";

  return (
    <Section
      id="arr-import"
      title="Import from Radarr / Sonarr"
      subtitle="Moving from Radarr or Sonarr? Bring the library over: every title with its quality profile, what's monitored, the folder it's in and the files you already have — recorded where they are, nothing downloaded again. Its indexers and download clients can come too, switched off where the app keeps a key or password to itself. Nothing in Radarr or Sonarr changes, and the API key isn't kept. Tags, blocklists and history aren't brought over. Several Radarrs or Sonarrs? Import each in turn; a title already here is never added twice."
    >
      <div className="flex flex-wrap gap-3">
        <Field label="App">
          <select value={app} disabled={!!pv} onChange={(e) => { setApp(e.target.value as ArrApp); setPv(null); }} className="w-fit rounded-lg px-2.5 py-2 text-base" style={inputStyle}>
            <option value="radarr">Radarr (movies)</option>
            <option value="sonarr">Sonarr (TV)</option>
          </select>
        </Field>
        <Field className="min-w-[220px] flex-1" label={app === "radarr" ? "Radarr URL" : "Sonarr URL"}>
          <input value={url} disabled={!!pv} onChange={(e) => setUrl(e.target.value)} placeholder={app === "radarr" ? "http://192.168.1.10:7878" : "http://192.168.1.10:8989"} className={input} style={inputStyle} autoComplete="off" />
        </Field>
        <Field className="min-w-[220px] flex-1" label="API key">
          <input type="password" value={key} disabled={!!pv} onChange={(e) => setKey(e.target.value)} placeholder="from Settings → General → API Key" className={input} style={inputStyle} autoComplete="new-password" />
        </Field>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        {!pv ? (
          <Button variant="primary" onClick={() => load(true)} busy={busy} busyLabel="Reading…" disabled={!url.trim() || !key.trim()}>Connect</Button>
        ) : (
          <Button onClick={reset} disabled={running}>{result ? "Import another" : "Start over"}</Button>
        )}
        {error && <span role="alert" className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</span>}
      </div>

      {pv && !result && (
        <div className="flex flex-col gap-5">
          <Summary pv={pv} />
          {pv.not_ready && (
            <Note tone="warn">
              {pv.not_ready.charAt(0).toUpperCase() + pv.not_ready.slice(1)}. <Link to={LINKS.apiKeys} style={{ color: "var(--accent-text)" }}>Open Connections →</Link>
            </Note>
          )}

          <Step n={1} title="What to bring over">
            <Switch checked={withTitles} onChange={setWithTitles}>The {noun}: every title with its quality profile, folder, monitored state and the files you already have</Switch>
            <Switch checked={withSetup} onChange={setWithSetup}>Indexers and download clients (read-only copy of their addresses; secrets are never shared, so those come over switched off)</Switch>
            {pv.preview.setup_error && withSetup && <Note tone="warn">Couldn't read {pv.preview.name}'s indexers and download clients: {pv.preview.setup_error}</Note>}
          </Step>
          {withSetup && pv.preview.setup && <SetupStep n={2} setup={pv.preview.setup} name={pv.preview.name} />}

          {withTitles && <>
          <Step n={withSetup ? 3 : 2} title="Folders">
            <p className="m-0 text-sm text-ink-faint">
              Each of {pv.preview.name}'s root folders and the Arrmada folder its {noun} go to. A title whose files are found goes to the folder they're in. Rules for each folder are in <Link to="/settings/library#more-folders" style={{ color: "var(--accent-text)" }}>Settings → Library</Link>.
            </p>
            {pv.roots.length === 0 && <div className="text-sm text-ink-faint">No root folders there.</div>}
            {pv.roots.map((r) => (
              <RootRow key={r.path} r={r} media={media} value={folders[r.path]}
                onChange={(id) => setFolders((f) => { const next = { ...f }; if (id === undefined) delete next[r.path]; else next[r.path] = id; return next; })}
                onAdded={(path) => {
                  // A folder added at another path is the same share mounted elsewhere: map it.
                  if (path !== r.path && !maps.some((m) => m.from.trim() === r.path)) setMaps([...maps.filter((m) => m.from.trim() || m.to.trim()), { from: r.path, to: path }]);
                }} />
            ))}
          </Step>

          <Step n={withSetup ? 4 : 3} title="Where the files are">
            <p className="m-0 text-sm text-ink-faint">
              {pv.preview.name} and Arrmada may see the same folders at different paths — <span className="font-mono">/movies</span> there, <span className="font-mono">/storage/movies</span> here. Map the start of {pv.preview.name}'s paths to where they are inside Arrmada, then check again.
            </p>
            <PathMaps maps={maps} onChange={setMaps} app={pv.preview.name} />
            <div className="flex flex-wrap items-center gap-3">
              <Button size="sm" onClick={() => load(false)} busy={busy} busyLabel="Checking…">Check again</Button>
              <span className="text-sm text-ink-dim">{filesLine(pv)}</span>
            </div>
            {pv.preview.not_found.length > 0 && (
              <details className="text-sm text-ink-dim">
                <summary className="cursor-pointer">Not found ({pv.preview.counts.not_found.toLocaleString()})</summary>
                <ul className="mt-1 flex flex-col gap-1 pl-4">
                  {pv.preview.not_found.map((s) => <li key={s.title + s.path}><span className="font-semibold">{s.title}</span>{s.year ? ` (${s.year})` : ""} — {s.reason}</li>)}
                </ul>
              </details>
            )}
            <Switch checked={importMissing} onChange={setImportMissing}>
              Also bring over {noun} whose files Arrmada can't find (they're added without them and downloaded again)
            </Switch>
          </Step>

          <Step n={withSetup ? 5 : 4} title="Quality profiles">
            {pv.preview.profiles.filter((p) => p.titles > 0).map((p) => (
              <div key={p.id} className="flex flex-wrap items-center justify-between gap-2 text-base">
                <span className="min-w-0">{p.name} <span className="text-ink-faint">· {p.titles.toLocaleString()} {p.titles === 1 ? "title" : "titles"}</span></span>
                <select aria-label={`Arrmada profile for ${p.name}`} value={profiles[p.id] ?? ""} onChange={(e) => setProfiles({ ...profiles, [p.id]: e.target.value })} className="rounded-lg px-2 py-1 text-sm" style={inputStyle}>
                  <option value="">The default profile</option>
                  {pv.targets.map((t) => <option key={t.key} value={t.key}>{t.name}{t.default ? " (default)" : ""}</option>)}
                </select>
              </div>
            ))}
            {pv.preview.profiles.every((p) => p.titles === 0) && <div className="text-sm text-ink-faint">No titles to bring over.</div>}
          </Step>
          </>}

          <div className="flex flex-wrap items-center gap-3">
            <Button variant="primary" onClick={start} busy={running} busyLabel="Importing…" disabled={(withTitles && (!!pv.not_ready || pv.preview.counts.titles === 0)) || (!withTitles && !withSetup)}>
              Import from {pv.preview.name}
            </Button>
            <span className="text-xs text-ink-faint">Nothing is deleted or changed in {pv.preview.name}, and nothing is searched for now: wanted {noun} join the regular searches a few at a time. Running this again adds only what's new.</span>
          </div>
          {running && job.job && <Progress value={job.job.progress} message={job.message} />}
        </div>
      )}

      {result && <ResultView r={result} />}
    </Section>
  );
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5" aria-label={title}>
      <h3 className="m-0 text-base font-bold"><span className="font-mono text-ink-faint">{n}.</span> {title}</h3>
      {children}
    </section>
  );
}

// SetupStep previews the app's indexers and download clients in two tabs, each row saying what
// would happen to it and why when it can't come over.
function SetupStep({ n, setup, name }: { n: number; setup: ArrSetup; name: string }) {
  const [tab, setTab] = useState<"indexers" | "clients">("indexers");
  const items: ArrSetupItem[] = tab === "indexers" ? setup.indexers : setup.clients;
  return (
    <Step n={n} title="Indexers and download clients">
      <Tabs
        tabs={[{ key: "indexers", label: "Indexers", count: setup.indexers.length }, { key: "clients", label: "Download clients", count: setup.clients.length }] as const}
        value={tab} onChange={setTab} idPrefix="arr-setup" label={`${name} indexers and download clients`} className="mb-1 border-b"
      />
      <TabPanel idPrefix="arr-setup" value={tab}>
        <p className="m-0 mb-2 text-sm text-ink-dim">{setupLine(items, false)}</p>
        {items.length === 0 && <div className="text-sm text-ink-faint">None in {name}.</div>}
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
          {items.map((it, i) => (
            <li key={it.name + i} className="rounded-lg px-3 py-2 text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <span className="font-semibold">{it.name} <span className="font-normal text-ink-faint">· {it.source}{it.address ? ` · ${it.address}` : ""}</span></span>
                <span className="text-ink-dim">{setupActionLabel(it)}</span>
              </div>
              {it.reason && <div className="mt-0.5 text-ink-faint">{it.reason}</div>}
            </li>
          ))}
        </ul>
      </TabPanel>
    </Step>
  );
}

function setupActionLabel(it: ArrSetupItem): string {
  switch (it.action) {
    case "add": return it.enabled ? "Will be added" : "Will be added, switched off";
    case "added": return it.enabled ? "Added" : "Added, switched off";
    case "exists": return "Already in Arrmada";
    case "failed": return "Failed";
    default: return "Not brought over";
  }
}

function Summary({ pv }: { pv: ArrPreview }) {
  return (
    <div role="status" className="rounded-lg p-3 text-base" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <div className="font-semibold">{pv.preview.name}{pv.preview.version ? ` ${pv.preview.version}` : ""}</div>
      <div className="mt-0.5 text-ink-dim">{arrCountsLine(pv.preview.media, pv.preview.counts)}</div>
      {pv.preview.unmatched.length > 0 && (
        <details className="mt-1 text-sm text-ink-dim">
          <summary className="cursor-pointer">Can't be matched ({pv.preview.counts.unmatchable.toLocaleString()})</summary>
          <ul className="mt-1 flex flex-col gap-1 pl-4">
            {pv.preview.unmatched.map((s) => <li key={s.title}><span className="font-semibold">{s.title}</span>{s.year ? ` (${s.year})` : ""} — {s.reason}</li>)}
          </ul>
        </details>
      )}
    </div>
  );
}

function filesLine(pv: ArrPreview): string {
  const c = pv.preview.counts;
  const where = pv.preview.media === "series" ? "show folders" : "files";
  if (c.with_files === 0) return `None of the new ${pv.preview.media === "series" ? "shows" : "films"} has files yet.`;
  return `${c.files_found.toLocaleString()} of ${c.with_files.toLocaleString()} ${where} found${c.not_found ? ` · ${c.not_found.toLocaleString()} not found` : ""}.`;
}

function Progress({ value, message }: { value: number; message: string }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="h-1.5 overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }} role="progressbar" aria-label="Import progress" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)}>
        <div className="h-full rounded-full" style={{ width: `${Math.round(value * 100)}%`, background: "var(--accent)" }} />
      </div>
      {message && <span className="text-xs text-ink-faint">{message}</span>}
    </div>
  );
}

function ResultView({ r }: { r: ArrResult }) {
  const groups = notesByOutcome(r.notes);
  return (
    <div role="status" className="flex flex-col gap-2 rounded-lg p-3 text-base" style={{ border: "1px solid var(--good)" }}>
      <div className="font-semibold">From {r.name}</div>
      {r.total > 0 && <div>{arrResultLine(r)}</div>}
      {r.setup && (
        <div className="flex flex-col gap-1 text-sm">
          <div>Indexers: {setupLine(r.setup.indexers, true)}</div>
          <div>Download clients: {setupLine(r.setup.clients, true)}</div>
          <div className="text-ink-faint">Add the missing keys and passwords in <Link to={LINKS.apiKeys} style={{ color: "var(--accent-text)" }}>Settings → Connections</Link>, test each one, then switch it on.</div>
        </div>
      )}
      {groups.map((g) => (
        <details key={g.outcome} className="text-sm text-ink-dim">
          <summary className="cursor-pointer">{g.label} ({g.notes.length.toLocaleString()})</summary>
          <ul className="mt-1 flex flex-col gap-1 pl-4">
            {g.notes.map((n, i) => <li key={i}><span className="font-semibold">{n.title}</span>{n.year ? ` (${n.year})` : ""} — {n.reason}</li>)}
          </ul>
        </details>
      ))}
      {!!r.more && <div className="text-xs text-ink-faint">…and {r.more.toLocaleString()} more notes in the log.</div>}
    </div>
  );
}

function PathMaps({ maps, onChange, app }: { maps: ArrPathMap[]; onChange: (m: ArrPathMap[]) => void; app: string }) {
  const set = (i: number, part: Partial<ArrPathMap>) => onChange(maps.map((m, j) => (j === i ? { ...m, ...part } : m)));
  return (
    <div className="flex flex-col gap-2">
      {maps.map((m, i) => (
        <div key={i} className="flex flex-wrap items-center gap-2">
          <input aria-label={`Path in ${app}`} value={m.from} onChange={(e) => set(i, { from: e.target.value })} placeholder={`In ${app} (/movies)`} className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 font-mono text-sm" style={inputStyle} />
          <span aria-hidden className="text-ink-faint">→</span>
          <input aria-label="Path inside Arrmada" value={m.to} onChange={(e) => set(i, { to: e.target.value })} placeholder="Inside Arrmada (/storage/movies)" className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 font-mono text-sm" style={inputStyle} />
          <Button size="sm" onClick={() => onChange(maps.filter((_, j) => j !== i))} aria-label="Remove this mapping">Remove</Button>
        </div>
      ))}
      <div>
        <Button size="sm" onClick={() => onChange([...maps, { from: "", to: "" }])}>Add a mapping</Button>
      </div>
    </div>
  );
}

// RootRow is one of the app's root folders: the Arrmada folder its titles go to, or — when
// Arrmada has no such folder yet — a way to add it.
function RootRow({ r, media, value, onChange, onAdded }: {
  r: ArrRoot;
  media: FolderMedia;
  value: number | undefined;
  onChange: (id: number | undefined) => void;
  onAdded: (path: string) => void;
}) {
  const toast = useToast();
  const { data } = useLibraryFolders();
  const list: LibraryFolder[] = data?.[media]?.folders ?? [];
  const lib = media === "series" ? "TV" : "Movies";
  const [adding, setAdding] = useState(false);
  const [path, setPath] = useState(r.suggested_path);
  const [label, setLabel] = useState(baseName(r.path));
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const add = async () => {
    setBusy(true);
    try {
      const f = await foldersApi.add({ media, path: path.trim(), label: label.trim() || undefined });
      foldersChanged();
      onChange(f.id);
      onAdded(f.path);
      setAdding(false);
      toast(`Added ${f.label} to ${lib}.`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 8000 });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="rounded-xl p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span className="font-mono font-semibold">{r.path}</span>
          <span className="text-ink-faint">· {r.titles.toLocaleString()} title{r.titles === 1 ? "" : "s"}{r.free_bytes > 0 ? ` · ${fmtBytes(r.free_bytes)} free` : ""}</span>
        </div>
        <select aria-label={`Arrmada folder for ${r.path}`} value={value === undefined ? "" : String(value)} onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))} className="rounded-lg px-2 py-1 text-sm" style={inputStyle}>
          <option value="">By the folder rules</option>
          {list.map((f) => <option key={f.id} value={f.id}>{f.label}{f.default ? " (main folder)" : ""}</option>)}
        </select>
      </div>
      {r.matched_by && value === r.folder_id && (
        <div className="mt-1 text-sm" style={{ color: "var(--good-text)" }}>
          ✓ {r.matched_by === "name" ? "An Arrmada folder of the same name" : "Already an Arrmada folder"}
        </div>
      )}
      {r.folder_id === null && !adding && (
        <div className="mt-1 text-sm text-ink-faint">
          Not an Arrmada folder yet. <button type="button" onClick={() => setAdding(true)} className="font-semibold" style={{ color: "var(--accent-text)" }}>Add it as a {lib} folder…</button>
        </div>
      )}
      {adding && (
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <input aria-label={`Where ${r.path} is inside Arrmada`} value={path} onChange={(e) => setPath(e.target.value)} placeholder="Where it is inside Arrmada (/storage/media/…)" className="min-w-0 flex-[2] rounded-lg px-2.5 py-1.5 font-mono text-sm" style={inputStyle} />
          <input aria-label={`Name for ${r.path}`} value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Name" className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 text-sm" style={inputStyle} />
          <Button size="sm" onClick={() => setPicking(true)}>Browse…</Button>
          <Button size="sm" variant="primary" onClick={add} busy={busy} busyLabel="Adding…" disabled={!path.trim()}>Add as a {lib} folder</Button>
        </div>
      )}
      {picking && <FolderPicker initial={path || undefined} onClose={() => setPicking(false)} onSelect={(p) => { setPicking(false); setPath(p); }} />}
    </div>
  );
}

function baseName(p: string) {
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? p;
}
