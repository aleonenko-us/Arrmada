import { useEffect, useState } from "react";
import { foldersApi, type FolderMedia, type LibraryFolderSet, type MovePreview } from "../lib/foldersApi";
import { formatBytes } from "../lib/format";
import { jobFailed, useJob } from "../lib/useJob";
import { Button, Modal } from "../ui";
import { foldersChanged } from "./FolderSelect";

const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };
const bad = { color: "var(--reject-text)" };

// MoveFolderDialog moves a film's or a show's files to another folder of its library —
// the "Move to folder" action, offered only when the library has more than one folder. It
// is its own confirmation: it names where the files go and how, and nothing moves until
// Move is pressed. The move runs as a job (followed here): a rename on the same drive,
// otherwise a checked copy whose originals then go to the recycle bin.
//
// For a film it first shows exactly what would move — each folder, whole, with every file
// in it — or why nothing will (its folder isn't provably its own). The move is sent with
// that list and never takes more: a folder holding anything else by then doesn't move.
export function MoveFolderDialog({ media, id, title, current, folders, onClose, onMoved }: {
  media: FolderMedia;
  id: number;
  title: string;
  /** The folder it belongs to now. */
  current: number;
  folders: LibraryFolderSet;
  onClose: () => void;
  /** The move finished well: the page reloads the title. */
  onMoved: () => void;
}) {
  const choices = folders.folders.filter((f) => f.id !== current);
  const [to, setTo] = useState<number>(choices[0]?.id ?? 0);
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const film = media === "movie";
  // The film's preview, for the folder it was asked about (shows don't have one).
  const [preview, setPreview] = useState<{ to: number; p?: MovePreview; err?: string } | null>(null);
  const job = useJob(jobId, {
    // A copy onto another drive can take a long while; keep following it.
    giveUpMs: 6 * 60 * 60 * 1000,
    onDone: (j) => {
      setJobId(null);
      if (jobFailed(j)) {
        setError(j.error || "The move stopped.");
        return;
      }
      setDone(j.message || "Moved.");
      foldersChanged();
      onMoved();
    },
  });
  const dest = choices.find((f) => f.id === to);
  const from = folders.folders.find((f) => f.id === current);
  const running = starting || job.running;
  const canPick = choices.length > 0;

  useEffect(() => {
    if (!film || !canPick || done) return;
    let live = true;
    foldersApi.previewMove(id, to)
      .then((p) => { if (live) setPreview({ to, p }); })
      .catch((e: Error) => { if (live) setPreview({ to, err: e.message }); });
    return () => { live = false; };
  }, [film, canPick, id, to, done]);
  const pv = film && preview?.to === to ? preview : null;
  const files = pv?.p && !pv.p.refused ? pv.p.moves.flatMap((m) => m.files) : null;

  const move = async () => {
    setStarting(true); setError(null);
    try {
      const r = await foldersApi.move(media, id, to, film ? files ?? [] : undefined);
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setStarting(false);
    }
  };

  const what = film ? "film" : "show";
  return (
    <Modal onClose={onClose} title="Move to another folder" size="md">
      <p className="m-0 mb-3 text-base text-ink-dim">
        Moves <b className="text-ink">{title}</b> {film ? "with the folder it's in" : "and every file of it"}{from ? <> out of <b className="text-ink">{from.label}</b></> : null}. Plex and Jellyfin are told about both folders.
      </p>
      {canPick ? (
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">Move to</span>
          <select value={to} onChange={(e) => setTo(Number(e.target.value))} disabled={running || !!done} className="rounded-lg px-2.5 py-1.5 text-base" style={field}>
            {choices.map((f) => <option key={f.id} value={f.id}>{f.label}{f.default ? " (default)" : ""}</option>)}
          </select>
          {dest && <span className="font-mono text-xs text-ink-faint">{dest.path}</span>}
        </label>
      ) : (
        <p className="text-sm text-ink-faint">There's no other folder to move it to.</p>
      )}
      {film && canPick && !done && (
        <section className="mt-3 text-sm" aria-label="What moves">
          {!pv ? <p className="m-0 text-ink-faint">Checking what would move…</p>
            : pv.err ? <p className="m-0" style={bad}>{pv.err}</p>
            : pv.p?.refused ? <p className="m-0" style={bad}>Nothing will move: {pv.p.refused}</p>
            : !pv.p?.moves.length ? <p className="m-0 text-ink-faint">Its files are already there: no file moves, the film is just filed under that folder.</p>
            : pv.p.moves.map((m) => (
              <div key={m.from} className="mb-2">
                <p className="m-0 text-ink-dim">
                  {m.files.length} file{m.files.length === 1 ? "" : "s"} ({formatBytes(m.bytes)}) move with their folder to <span className="break-all font-mono text-xs">{m.to}</span> — nothing else:
                </p>
                <ul className="m-0 mt-1 max-h-40 list-none overflow-y-auto rounded-lg px-2.5 py-1 font-mono text-xs text-ink-faint" style={field}>
                  {m.files.map((f) => <li key={f} className="break-all">{f}</li>)}
                </ul>
              </div>
            ))}
        </section>
      )}
      <p className="mb-0 mt-3 text-sm text-ink-faint">
        On the same drive this is instant. To another drive each file is copied and checked first; only then do the originals go to the recycle bin. Arrmada waits while a download for this {what} is still on its way in.
      </p>
      {running && <div role="status" className="mt-3 text-sm text-ink-dim">{job.message || "Moving…"}</div>}
      {done && <div role="status" className="mt-3 text-sm" style={{ color: "var(--good-text)" }}>{done}</div>}
      {error && <div className="mt-3 text-sm" style={bad}>{error}</div>}
      <div className="mt-5 flex justify-end gap-2">
        {/* Closing never stops the move: it carries on in the background. */}
        <Button variant="ghost" onClick={onClose}>{done ? "Close" : running ? "Close — it keeps going" : "Cancel"}</Button>
        {!done && <Button variant="primary" onClick={move} disabled={running || !canPick || (film && !files)}>{running ? "Moving…" : "Move"}</Button>}
      </div>
    </Modal>
  );
}
