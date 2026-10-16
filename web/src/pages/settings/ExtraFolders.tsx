import { useState } from "react";
import { foldersChanged, useLibraryFolders } from "../../components/FolderSelect";
import { FOLDER_RULES, foldersApi, type FolderMedia, type FolderRule, type LibraryFolder, type LibraryFolderSet } from "../../lib/foldersApi";
import { isAdmin, useMe } from "../../lib/me";
import { Button, IconButton, useConfirm, useToast } from "../../ui";
import { FolderPicker } from "../Library";

const field = { background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

// ExtraFolders is Settings → Library's "More folders" card: extra Movies and TV folders
// next to the main one (a Kids, Animation or Anime folder, often its own Plex or Jellyfin
// library). Each can carry a rule; a new title goes to the first folder, top to bottom,
// whose rule matches it, otherwise to the main folder. Admins change them; managers see
// them. Removing one that still holds titles is refused and says what to do.
export function ExtraFolders() {
  const { user } = useMe();
  const admin = isAdmin(user);
  const { data, error } = useLibraryFolders();
  if (error && !data) return <div className="text-sm" style={{ color: "var(--reject-text)" }}>Couldn't load the folders: {error.message}</div>;
  if (!data) return <div className="text-base text-ink-dim">Loading…</div>;
  return (
    <div className="flex flex-col gap-5">
      <FolderList media="movie" title="Movies" set={data.movie} admin={admin} />
      <FolderList media="series" title="TV Shows" set={data.series} admin={admin} />
      <p className="m-0 text-sm text-ink-faint">
        A new folder has to be inside a folder mounted into the container. Under the same mount as your other libraries (say <span className="font-mono">/storage</span>), just pick it; anywhere else, add its mount to <span className="font-mono">docker-compose.override.yml</span> first (see <span className="font-mono">docker-compose.override.example.yml</span>) and recreate the container, then pick it here. Moving a title between folders moves its files: use <b>Move to folder</b> on the title's page.
      </p>
      {!admin && <p className="m-0 text-sm text-ink-faint">Only an admin can change these folders.</p>}
    </div>
  );
}

function FolderList({ media, title, set, admin }: { media: FolderMedia; title: string; set: LibraryFolderSet; admin: boolean }) {
  const toast = useToast();
  const [picking, setPicking] = useState(false);
  const [busy, setBusy] = useState(false);
  const extras = set.folders.filter((f) => !f.default);
  const main = set.folders.find((f) => f.default);

  const add = async (path: string) => {
    setPicking(false);
    setBusy(true);
    try {
      const f = await foldersApi.add({ media, path });
      foldersChanged();
      toast(`Added ${f.label} to ${title}.`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 8000 });
    } finally {
      setBusy(false);
    }
  };
  // Move one folder up or down; the order is the order rules are tried in.
  const move = async (id: number, dir: -1 | 1) => {
    const ids = extras.map((f) => f.id);
    const i = ids.indexOf(id), j = i + dir;
    if (i < 0 || j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    setBusy(true);
    try {
      await foldersApi.order(media, ids);
      foldersChanged();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <span className="text-md font-semibold">{title}</span>
        {admin && <Button size="sm" variant="secondary" onClick={() => setPicking(true)} busy={busy} busyLabel="Saving…">Add a folder…</Button>}
      </div>
      <div className="flex flex-col gap-2">
        {main && (
          <div className="rounded-xl p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-base font-semibold">{main.label} <span className="font-normal text-ink-faint">— the main folder</span></span>
              <span className="text-xs text-ink-faint">{titles(main.titles)}</span>
            </div>
            <div className="mt-0.5 font-mono text-xs text-ink-faint">{main.path || "Not set — choose it in Media folders above"}</div>
            {extras.length > 0 && <div className="mt-1 text-xs text-ink-faint">Anything no rule below matches goes here.</div>}
          </div>
        )}
        {extras.map((f, i) => (
          <ExtraRow key={f.id} f={f} admin={admin} first={i === 0} last={i === extras.length - 1} busy={busy} onMove={move} />
        ))}
        {extras.length === 0 && <div className="text-sm text-ink-faint">No other {title} folders. Everything goes to the main one.</div>}
      </div>
      {picking && <FolderPicker onClose={() => setPicking(false)} onSelect={(p) => void add(p)} />}
    </div>
  );
}

function titles(n: number) {
  return `${n.toLocaleString()} title${n === 1 ? "" : "s"}`;
}

function ExtraRow({ f, admin, first, last, busy, onMove }: {
  f: LibraryFolder;
  admin: boolean;
  first: boolean;
  last: boolean;
  busy: boolean;
  onMove: (id: number, dir: -1 | 1) => void;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const [label, setLabel] = useState(f.label);
  const [saving, setSaving] = useState(false);

  const save = async (body: { label?: string; rule?: FolderRule }) => {
    setSaving(true);
    try {
      await foldersApi.update(f.id, body);
      foldersChanged();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
      setLabel(f.label);
    } finally {
      setSaving(false);
    }
  };
  const commitLabel = () => {
    const v = label.trim();
    if (v && v !== f.label) void save({ label: v });
    else setLabel(f.label);
  };
  const remove = async () => {
    const yes = await confirm({
      title: `Remove the “${f.label}” folder from Arrmada?`,
      body: f.titles > 0
        ? `${titles(f.titles)} still belong to it. Move them to another folder first (Move to folder on each title's page) — Arrmada won't leave them pointing at a folder it no longer knows.`
        : "Only Arrmada forgets it: the folder and anything in it stay on disk.",
      confirmLabel: "Remove",
      tone: "danger",
    });
    if (!yes) return;
    setSaving(true);
    try {
      await foldersApi.remove(f.id);
      foldersChanged();
      toast(`Removed ${f.label}.`);
    } catch (e) {
      toast((e as Error).message, { tone: "error", ms: 8000 });
    } finally {
      setSaving(false);
    }
  };
  const rule = FOLDER_RULES.find((r) => r.value === f.rule) ?? FOLDER_RULES[0];

  return (
    <div className="rounded-xl p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-2">
        {admin ? (
          <input
            aria-label={`Name of the ${f.label} folder`}
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            onBlur={commitLabel}
            onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); commitLabel(); } if (e.key === "Escape") setLabel(f.label); }}
            disabled={saving}
            className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 text-base font-semibold"
            style={field}
          />
        ) : (
          <span className="min-w-0 flex-1 text-base font-semibold">{f.label}</span>
        )}
        <span className="text-xs text-ink-faint">{titles(f.titles)}</span>
        {admin && (
          <>
            <IconButton label="Try this folder's rule earlier" onClick={() => onMove(f.id, -1)} disabled={first || busy || saving} className="h-[30px] w-[30px] rounded-lg disabled:opacity-40" style={field}>↑</IconButton>
            <IconButton label="Try this folder's rule later" onClick={() => onMove(f.id, 1)} disabled={last || busy || saving} className="h-[30px] w-[30px] rounded-lg disabled:opacity-40" style={field}>↓</IconButton>
            <Button size="sm" variant="ghost" onClick={remove} disabled={saving}>Remove</Button>
          </>
        )}
      </div>
      <div className="mt-1 font-mono text-xs text-ink-faint">{f.path}</div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        {admin ? (
          <label className="flex items-center gap-2 text-sm">
            <span className="font-mono text-2xs uppercase text-ink-faint">Rule</span>
            <select value={f.rule} onChange={(e) => void save({ rule: e.target.value as FolderRule })} disabled={saving} className="rounded-lg px-2 py-1 text-sm" style={field}>
              {FOLDER_RULES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
            </select>
          </label>
        ) : (
          <span className="text-sm text-ink-dim">Rule: {rule.label}</span>
        )}
        <span className="text-xs text-ink-faint">{rule.help}</span>
      </div>
    </div>
  );
}
