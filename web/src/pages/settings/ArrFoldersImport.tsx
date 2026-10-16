import { useState } from "react";
import { Link } from "react-router-dom";
import { Section, input, inputStyle } from "../../components/settings/ui";
import { foldersChanged } from "../../components/FolderSelect";
import { Field } from "../../ui/Field";
import { Button, useToast } from "../../ui";
import { api } from "../../lib/api";
import { fmtBytes } from "../../lib/disposal";
import { foldersApi, type ArrRoot, type ArrRoots } from "../../lib/foldersApi";
import { FolderPicker } from "../Library";

// ArrFoldersImport sets up the same library folders a Radarr or Sonarr has — Movies and
// Kids movies, TV and Anime — then scans them, so every title Arrmada finds lands in the
// folder it's already in. It only reads Radarr or Sonarr, and the API key isn't kept.
export function ArrFoldersImport() {
  const toast = useToast();
  const [app, setApp] = useState<"radarr" | "sonarr">("radarr");
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [found, setFound] = useState<ArrRoots | null>(null);
  const [scanning, setScanning] = useState(false);

  const read = async () => {
    setBusy(true); setError(null); setFound(null);
    try {
      setFound(await foldersApi.arrRoots(app, url.trim(), key.trim()));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const added = (path: string, id: number) =>
    setFound((f) => f && { ...f, roots: f.roots.map((r) => (r.path === path ? { ...r, folder_id: id } : r)) });
  const scan = async () => {
    if (!found) return;
    setScanning(true);
    try {
      await (found.media === "movie" ? api.scanLibrary() : api.scanSeries());
      toast("Scanning every folder — each title found is filed under the folder it's in. Follow it in Settings → Library.", { tone: "good", ms: 7000 });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setScanning(false);
    }
  };
  const lib = found?.media === "series" ? "TV" : "Movies";
  const allSet = !!found && found.roots.every((r) => r.folder_id !== null);

  return (
    <Section id="arr-folders" title="Folders from Radarr or Sonarr" subtitle="Had more than one root folder in Radarr or Sonarr — Movies and Kids, TV and Anime? Read them here, add the ones Arrmada doesn't have yet, then scan: every title found is filed under the folder it's in. Nothing in Radarr or Sonarr changes, and the API key isn't kept.">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">App</span>
          <select value={app} onChange={(e) => { setApp(e.target.value as "radarr" | "sonarr"); setFound(null); }} className="rounded-lg px-2.5 py-2 text-base" style={inputStyle}>
            <option value="radarr">Radarr (movies)</option>
            <option value="sonarr">Sonarr (TV)</option>
          </select>
        </label>
      </div>
      <Field label={app === "radarr" ? "Radarr URL" : "Sonarr URL"}>
        <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder={app === "radarr" ? "http://192.168.1.10:7878" : "http://192.168.1.10:8989"} className={input} style={inputStyle} autoComplete="off" />
      </Field>
      <Field label="API key">
        <input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder="from Settings → General → API Key" className={input} style={inputStyle} autoComplete="off" />
      </Field>
      <div className="flex items-center gap-3">
        <Button variant="primary" onClick={read} busy={busy} busyLabel="Reading…" disabled={!url.trim() || !key.trim()}>Read root folders</Button>
        {error && <span className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</span>}
      </div>
      {found && (
        <div className="flex flex-col gap-2">
          {found.roots.length === 0 && <div className="text-sm text-ink-faint">No root folders there.</div>}
          {found.roots.map((r) => <RootRow key={r.path} r={r} media={found.media} lib={lib} onAdded={added} />)}
          {found.roots.length > 0 && (
            <div className="mt-1 flex flex-wrap items-center gap-3">
              <Button onClick={scan} busy={scanning} busyLabel="Starting…" disabled={!allSet} title={allSet ? undefined : "Add (or skip) each folder first"}>Scan the {lib} folders</Button>
              <span className="text-xs text-ink-faint">
                {allSet ? "Every root folder is an Arrmada folder now." : "A folder left out is simply not scanned."} Rules for each folder are in <Link to="/settings/library#more-folders" style={{ color: "var(--accent-text)" }}>Settings → Library</Link>.
              </span>
            </div>
          )}
        </div>
      )}
    </Section>
  );
}

function RootRow({ r, media, lib, onAdded }: { r: ArrRoot; media: "movie" | "series"; lib: string; onAdded: (path: string, id: number) => void }) {
  const toast = useToast();
  const [path, setPath] = useState(r.suggested_path);
  const [label, setLabel] = useState(baseName(r.path));
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const add = async () => {
    setBusy(true);
    try {
      const f = await foldersApi.add({ media, path: path.trim(), label: label.trim() || undefined });
      foldersChanged();
      onAdded(r.path, f.id);
      toast(`Added ${f.label} to ${lib}.`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 8000 });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="rounded-xl p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
        <span className="font-mono font-semibold">{r.path}</span>
        <span className="text-ink-faint">· {r.titles.toLocaleString()} title{r.titles === 1 ? "" : "s"}{r.free_bytes > 0 ? ` · ${fmtBytes(r.free_bytes)} free` : ""}</span>
      </div>
      {r.folder_id !== null ? (
        <div className="mt-1 text-sm" style={{ color: "var(--good-text)" }}>
          ✓ {r.matched_by === "name" ? "Matches an Arrmada folder of the same name" : r.matched_by === "path" ? "Already an Arrmada folder" : "Added"}
        </div>
      ) : (
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
