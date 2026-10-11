import { useState } from "react";
import { api, ApiError, type MovieBulkUpdate, type MovieBulkUpdateReply } from "../lib/api";
import { fmtBytes } from "../lib/disposal";
import { Button, Modal } from "../ui";
import { DeleteMoviesDialog } from "./DeleteMovieDialog";
import { OrganizeModal } from "./OrganizeModal";

const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;

const AVAILABILITY = [
  { key: "announced", label: "Announced" },
  { key: "inCinemas", label: "In cinemas" },
  { key: "released", label: "Released" },
];

export interface MovieBulkBarProps {
  /** The selected movie ids. */
  selected: Set<number>;
  /** Replaces the selection (cleared on success, the failures kept on a partial failure). */
  onSelect: (ids: Set<number>) => void;
  /** Every movie the page knows, for titles in dialogs. */
  movies: { id: number; title: string }[];
  /** The ids "Select all" picks: what the page currently shows. */
  visibleIds: number[];
  profiles: { key: string; name: string }[];
  /** Called after anything changed, so the page reads its list again. */
  onChanged: () => void;
  flash: (msg: string, err?: boolean) => void;
}

type Exceeds = MovieBulkUpdateReply["exceeds_profile"];

// MovieBulkBar is the Movies mass editor: monitor, profile, availability, search, rename
// and delete for a selection, each one request to the bulk endpoints. A profile or
// monitoring change commits for every film or for none; a partial failure says how many
// failed and leaves just those selected. It knows nothing about grid or table — the page
// hands it the selection, so any list with selection can mount it.
export function MovieBulkBar({ selected, onSelect, movies, visibleIds, profiles, onChanged, flash }: MovieBulkBarProps) {
  const [busy, setBusy] = useState<string | null>(null);
  const [exceeds, setExceeds] = useState<Exceeds | null>(null);
  const [renaming, setRenaming] = useState<number[] | null>(null);
  const [deleting, setDeleting] = useState(false);
  const ids = [...selected];
  const none = ids.length === 0 || busy !== null;

  const update = async (key: string, change: Omit<MovieBulkUpdate, "ids">, done: (n: number) => string) => {
    setBusy(key);
    try {
      const r = await api.bulkUpdateMovies({ ids, ...change });
      const queued = r.queued > 0 ? ` ${plural(r.queued, "search", "searches")} queued — they run two at a time.` : "";
      if (r.errors.length > 0) {
        flash(`${done(ids.length - r.errors.length)}${queued} ${plural(r.errors.length, "movie")} couldn't be searched — still selected.`, true);
        onSelect(new Set(r.errors.map((e) => e.id)));
      } else {
        flash(`${done(ids.length)}${queued}`);
        onSelect(new Set());
      }
      if (r.exceeds_profile.length > 0) setExceeds(r.exceeds_profile);
      onChanged();
    } catch (e) {
      // Nothing was changed. Films that are gone drop out of the selection; the rest stay.
      const missing = e instanceof ApiError ? ((e.body?.missing as number[] | undefined) ?? []) : [];
      if (missing.length) onSelect(new Set(ids.filter((id) => !missing.includes(id))));
      flash((e as Error).message, true);
      if (missing.length) onChanged();
    } finally {
      setBusy(null);
    }
  };

  const sel = "rounded-lg px-2.5 py-1.5 text-[11.5px] font-medium disabled:opacity-50";
  const selStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;
  const chosen = movies.filter((m) => selected.has(m.id));

  return (
    <div className="mb-4 flex flex-wrap items-center gap-2.5 rounded-xl p-3" style={{ background: "var(--panel)", border: "1px solid var(--accent-line)" }}>
      <span className="font-mono text-[11.5px] font-semibold" style={{ color: "var(--accent)" }}>{selected.size} selected</span>
      <Button size="sm" variant="ghost" onClick={() => onSelect(new Set(visibleIds))}>Select all ({visibleIds.length})</Button>
      <Button size="sm" variant="ghost" onClick={() => onSelect(new Set())} disabled={selected.size === 0}>Clear</Button>
      <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
      <Button size="sm" variant="secondary" disabled={none} busy={busy === "monitor"} busyLabel="Saving…"
        onClick={() => update("monitor", { monitored: true }, (n) => `${plural(n, "movie")} monitored.`)}>Monitor</Button>
      <Button size="sm" variant="ghost" disabled={none} busy={busy === "unmonitor"} busyLabel="Saving…"
        onClick={() => update("unmonitor", { monitored: false }, (n) => `${plural(n, "movie")} unmonitored.`)}>Unmonitor</Button>
      <select aria-label="Set quality profile" value="" disabled={none} className={sel} style={selStyle}
        onChange={(e) => {
          const key = e.target.value;
          if (key) void update("profile", { quality_profile: key, search: "missing_and_upgrade" }, (n) => `Quality profile set on ${plural(n, "movie")}.`);
        }}>
        <option value="">Set quality profile…</option>
        {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
      </select>
      <select aria-label="Set minimum availability" value="" disabled={none} className={sel} style={selStyle}
        onChange={(e) => {
          const key = e.target.value;
          if (key) void update("availability", { min_availability: key }, (n) => `Availability set on ${plural(n, "movie")}.`);
        }}>
        <option value="">Set availability…</option>
        {AVAILABILITY.map((a) => <option key={a.key} value={a.key}>{a.label}</option>)}
      </select>
      <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
      <Button size="sm" variant="secondary" disabled={none} busy={busy === "search"} busyLabel="Queuing…"
        onClick={() => update("search", { search: "missing_and_upgrade" }, () => "Searching the selected movies.")}>Search selected</Button>
      <Button size="sm" variant="ghost" disabled={none} onClick={() => setRenaming(ids)}>Rename…</Button>
      <Button size="sm" variant="danger" disabled={none} onClick={() => setDeleting(true)}>Delete…</Button>

      {renaming && <OrganizeModal ids={renaming} onClose={() => setRenaming(null)} onDone={onChanged} />}
      {deleting && (
        <DeleteMoviesDialog
          movies={chosen}
          onClose={() => setDeleting(false)}
          onDone={(r) => {
            onSelect(new Set(r.results.filter((x) => !x.deleted).map((x) => x.id)));
            onChanged();
            if (r.failed === 0) flash(`Removed ${plural(r.deleted, "movie")}.`);
          }}
        />
      )}
      {exceeds && <ExceedsPrompt items={exceeds} onClose={() => setExceeds(null)} flash={flash} />}
    </div>
  );
}

// ExceedsPrompt asks about the films whose file the new profile no longer accepts. Nothing
// happens on its own: download releases that fit (a smaller one, for a file only over the
// ceiling), or keep the files as they are.
function ExceedsPrompt({ items, onClose, flash }: { items: Exceeds; onClose: () => void; flash: (msg: string, err?: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const allSmaller = items.every((it) => it.smaller);
  const regrab = async () => {
    setBusy(true);
    try {
      const r = await api.bulkRegrabMovies(items.map((it) => it.id));
      flash(`${plural(r.queued + r.duplicates, "search", "searches")} queued for releases that fit — each file is replaced when its new one imports.`);
      onClose();
    } catch (e) {
      flash((e as Error).message, true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      onClose={onClose}
      size="md"
      title={`${plural(items.length, "file")} ${items.length === 1 ? "doesn't" : "don't"} fit this profile`}
      dismissible={!busy}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>Keep them</Button>
          <Button variant="primary" onClick={regrab} busy={busy} busyLabel="Queuing…">{allSmaller ? "Download smaller versions" : "Find releases that fit"}</Button>
        </>
      }
    >
      <p className="m-0 mb-3 text-[12px] text-ink-dim">These films' files are better than, or different from, what the new profile asks for. They were left alone.</p>
      <div className="thin-scroll max-h-[40vh] overflow-y-auto">
        {items.map((it) => (
          <div key={it.id} className="py-1.5" style={{ borderBottom: "1px solid var(--line-soft)" }}>
            <div className="text-[12px] font-semibold">{it.title} {it.size_bytes ? <span className="font-mono text-[10.5px] font-normal text-ink-faint">{fmtBytes(it.size_bytes)}</span> : null}</div>
            {it.reason && <div className="text-[11px] text-ink-dim">{it.reason}</div>}
          </div>
        ))}
      </div>
    </Modal>
  );
}
