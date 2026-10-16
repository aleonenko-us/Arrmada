import { useState } from "react";
import { foldersApi, type FolderMedia, type LibraryFolderSet } from "../lib/foldersApi";
import { jobFailed, useJob } from "../lib/useJob";
import { Button, Modal } from "../ui";
import { foldersChanged } from "./FolderSelect";

const field = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

// MoveFolderDialog moves a film's or a show's files to another folder of its library —
// the "Move to folder" action, offered only when the library has more than one folder. It
// is its own confirmation: it names where the files go and how, and nothing moves until
// Move is pressed. The move runs as a job (followed here): a rename on the same drive,
// otherwise a checked copy whose originals then go to the recycle bin.
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

  const move = async () => {
    setStarting(true); setError(null);
    try {
      const r = await foldersApi.move(media, id, to);
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setStarting(false);
    }
  };

  const what = media === "movie" ? "film" : "show";
  return (
    <Modal onClose={onClose} title="Move to another folder" size="md">
      <p className="m-0 mb-3 text-base text-ink-dim">
        Moves <b className="text-ink">{title}</b> and every file of it{from ? <> out of <b className="text-ink">{from.label}</b></> : null}. Plex and Jellyfin are told about both folders.
      </p>
      {choices.length === 0 ? (
        <p className="text-sm text-ink-faint">There's no other folder to move it to.</p>
      ) : (
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">Move to</span>
          <select value={to} onChange={(e) => setTo(Number(e.target.value))} disabled={running || !!done} className="rounded-lg px-2.5 py-1.5 text-base" style={field}>
            {choices.map((f) => <option key={f.id} value={f.id}>{f.label}{f.default ? " (default)" : ""}</option>)}
          </select>
          {dest && <span className="font-mono text-xs text-ink-faint">{dest.path}</span>}
        </label>
      )}
      <p className="mb-0 mt-3 text-sm text-ink-faint">
        On the same drive this is instant. To another drive each file is copied and checked first; only then do the originals go to the recycle bin. Arrmada waits while a download for this {what} is still on its way in.
      </p>
      {running && <div role="status" className="mt-3 text-sm text-ink-dim">{job.message || "Moving…"}</div>}
      {done && <div role="status" className="mt-3 text-sm" style={{ color: "var(--good-text)" }}>{done}</div>}
      {error && <div className="mt-3 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
      <div className="mt-5 flex justify-end gap-2">
        {/* Closing never stops the move: it carries on in the background. */}
        <Button variant="ghost" onClick={onClose}>{done ? "Close" : running ? "Close — it keeps going" : "Cancel"}</Button>
        {!done && <Button variant="primary" onClick={move} disabled={running || choices.length === 0}>{running ? "Moving…" : "Move"}</Button>}
      </div>
    </Modal>
  );
}
