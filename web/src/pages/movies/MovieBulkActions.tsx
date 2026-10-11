import { useState } from "react";
import { api, ApiError, type MovieBulkUpdate, type MovieBulkUpdateReply, type MovieSummary } from "../../lib/api";
import { fmtBytes } from "../../lib/disposal";
import { Button, Modal, useToast } from "../../ui";
import { DeleteMoviesDialog } from "../../components/DeleteMovieDialog";
import { OrganizeModal } from "../../components/OrganizeModal";
import type { LibrarySelection } from "../../features/library/types";

const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;

const AVAILABILITY = [
  { key: "announced", label: "Announced" },
  { key: "inCinemas", label: "In cinemas" },
  { key: "released", label: "Released" },
];

type Exceeds = MovieBulkUpdateReply["exceeds_profile"];

// MovieBulkActions are the Movies mass editor (MOV-19) in the library's bulk bar, for the
// grid's selection in grid and table alike: monitor, profile, availability, search,
// rename and delete. Each is ONE request to the bulk endpoints: field changes commit for
// every film or for none, searches join the throttled movie search queue. On success the
// selection clears; on a partial failure the toast gives the counts and only the failures
// stay selected.
export function MovieBulkActions({ sel, profiles, onDone }: {
  sel: LibrarySelection<MovieSummary>;
  profiles: { key: string; name: string }[];
  onDone: () => void;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState<string | null>(null);
  const [exceeds, setExceeds] = useState<Exceeds | null>(null);
  const [renaming, setRenaming] = useState<number[] | null>(null);
  const [deleting, setDeleting] = useState<{ id: number; title: string }[] | null>(null);
  const ids = [...sel.ids];
  const none = ids.length === 0 || busy !== null;

  const update = async (key: string, change: Omit<MovieBulkUpdate, "ids">, done: (n: number) => string) => {
    setBusy(key);
    try {
      const r = await api.bulkUpdateMovies({ ids, ...change });
      const queued = r.queued > 0 ? ` ${plural(r.queued, "search", "searches")} queued — they run two at a time.` : "";
      if (r.errors.length > 0) {
        toast(`${done(ids.length - r.errors.length)}${queued} ${plural(r.errors.length, "movie")} couldn't be searched — still selected.`, { tone: "error" });
        sel.set(r.errors.map((e) => e.id));
      } else {
        toast(`${done(ids.length)}${queued}`);
        sel.clear();
      }
      if (r.exceeds_profile.length > 0) setExceeds(r.exceeds_profile);
      onDone();
    } catch (e) {
      // Nothing was changed. Films that are gone drop out of the selection; the rest stay.
      const missing = e instanceof ApiError ? ((e.body?.missing as number[] | undefined) ?? []) : [];
      if (missing.length) {
        sel.set(ids.filter((id) => !missing.includes(id)));
        onDone();
      }
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
    }
  };

  const selCls = "rounded-lg px-2.5 py-1.5 text-[11.5px] font-medium disabled:opacity-50";
  const selStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

  return (
    <>
      <Button size="sm" variant="secondary" disabled={none} busy={busy === "monitor"} busyLabel="Saving…"
        onClick={() => update("monitor", { monitored: true }, (n) => `${plural(n, "movie")} monitored.`)}>Monitor</Button>
      <Button size="sm" variant="ghost" disabled={none} busy={busy === "unmonitor"} busyLabel="Saving…"
        onClick={() => update("unmonitor", { monitored: false }, (n) => `${plural(n, "movie")} unmonitored.`)}>Unmonitor</Button>
      <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
      <select aria-label="Set the quality profile of the selected movies" value="" disabled={none} className={selCls} style={selStyle}
        onChange={(e) => {
          const key = e.target.value;
          if (key) void update("profile", { quality_profile: key, search: "missing_and_upgrade" }, (n) => `Quality profile set on ${plural(n, "movie")}.`);
        }}>
        <option value="">Set quality profile…</option>
        {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
      </select>
      <select aria-label="Set the minimum availability of the selected movies" value="" disabled={none} className={selCls} style={selStyle}
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
      <Button size="sm" variant="danger" disabled={none} onClick={() => setDeleting(sel.items.map((m) => ({ id: m.id, title: m.title })))}>Delete…</Button>

      {renaming && <OrganizeModal ids={renaming} onClose={() => setRenaming(null)} onDone={onDone} />}
      {deleting && (
        <DeleteMoviesDialog
          movies={deleting}
          onClose={() => setDeleting(null)}
          onDone={(r) => {
            const failed = r.results.filter((x) => !x.deleted).map((x) => x.id);
            if (failed.length) sel.set(failed);
            else sel.clear();
            onDone();
            if (r.failed === 0) toast(`Removed ${plural(r.deleted, "movie")}.`);
          }}
        />
      )}
      {exceeds && <ExceedsPrompt items={exceeds} onClose={() => setExceeds(null)} />}
    </>
  );
}

// ExceedsPrompt asks about the films whose file the new profile no longer accepts. Nothing
// happens on its own: download releases that fit (a smaller one, for a file only over the
// ceiling), or keep the files as they are.
function ExceedsPrompt({ items, onClose }: { items: Exceeds; onClose: () => void }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const allSmaller = items.every((it) => it.smaller);
  const regrab = async () => {
    setBusy(true);
    try {
      const r = await api.bulkRegrabMovies(items.map((it) => it.id));
      toast(`${plural(r.queued + r.duplicates, "search", "searches")} queued for releases that fit — each file is replaced when its new one imports.`);
      onClose();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
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
