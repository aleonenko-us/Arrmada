import { useEffect, useId, useMemo, useState } from "react";
import { releaseErrorMessage, type RankedRelease, type ReleaseList } from "../lib/api";
import { formatReleaseAge } from "../lib/format";
import { isUsenetRelease, noClientFor } from "../lib/network";
import { IndexerIssues, emptyReleasesMessage } from "./IndexerIssues";
import { UsenetChip } from "./UsenetChip";
import { Button, IconButton, Modal } from "../ui";

// BookReleaseModal is the book-specific interactive search: results split into
// Audiobooks / Ebooks tabs, each showing the raw release title (so the narrator
// and other detail a tracker puts in the name are visible), with the best match
// for the book's quality profile marked ★ Recommended.
export function BookReleaseModal({
  title,
  fetchReleases,
  onGrab,
  onClose,
  targets,
  defaultTarget,
  audioOnly,
}: {
  title: string;
  fetchReleases: () => Promise<ReleaseList>;
  // versionId: 0 = the standard audiobook, >0 = file the grab as that audiobook version.
  onGrab: (rel: RankedRelease, versionId: number) => Promise<void>;
  onClose: () => void;
  /** The book's extra audiobook versions; when present, audiobook rows get a "grab as" picker. */
  targets?: { id: number; label: string }[];
  /** Pre-select this version in every audiobook row's picker (a version's own search). */
  defaultTarget?: number;
  /** Show only the Audiobooks tab. */
  audioOnly?: boolean;
}) {
  const [list, setList] = useState<ReleaseList | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [grabbed, setGrabbed] = useState<Set<string>>(new Set());
  const [tab, setTab] = useState<"audiobook" | "ebook">("audiobook");
  const titleId = useId();

  const run = async () => {
    setLoading(true);
    setError(null);
    try {
      setList(await fetchReleases());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    run();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const releases = list?.releases ?? [];
  const ebooks = useMemo(() => releases.filter((r) => r.edition === "ebook"), [releases]);
  const audiobooks = useMemo(() => releases.filter((r) => r.edition === "audiobook"), [releases]);
  // Land on whichever tab actually has results (audiobooks preferred).
  useEffect(() => {
    if (!audioOnly && !loading && audiobooks.length === 0 && ebooks.length > 0) setTab("ebook");
  }, [audioOnly, loading, audiobooks.length, ebooks.length]);

  const rows = tab === "ebook" ? ebooks : audiobooks;

  const grab = async (rel: RankedRelease, versionId: number) => {
    // Releases are keyed by their token: two editions can share a display title, and the
    // token is unique per result.
    const key = rel.token ?? rel.title;
    setBusy(key);
    setError(null);
    try {
      await onGrab(rel, versionId);
      setGrabbed((s) => new Set(s).add(key));
    } catch (e) {
      setError(releaseErrorMessage(e));
    } finally {
      setBusy(null);
    }
  };

  const TabBtn = ({ k, label, count }: { k: "audiobook" | "ebook"; label: string; count: number }) => (
    <button
      onClick={() => setTab(k)}
      className="rounded-lg px-3.5 py-1.5 text-sm font-semibold"
      style={{ background: tab === k ? "var(--accent)" : "var(--panel-2)", color: tab === k ? "var(--accent-ink)" : "var(--ink-dim)", border: "1px solid var(--line)" }}
    >
      {label} <span className="font-mono text-xs opacity-70">{count}</span>
    </button>
  );

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="lg">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 id={titleId} className="m-0 truncate text-lg font-bold">{title}</h2>
            <p className="m-0 mt-0.5 text-sm text-ink-dim">Best match for your quality profile is marked ★ Recommended.</p>
          </div>
          <div className="flex flex-none items-center gap-2">
            <Button size="sm" onClick={run} busy={loading} busyLabel="Searching…">Search again</Button>
            <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
          </div>
        </div>
        {error && <div className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}
        {!loading && <IndexerIssues list={list} />}
        {!loading && (
          <div className="mb-3 flex items-center gap-2 border-b pb-3" style={{ borderColor: "var(--line)" }}>
            <TabBtn k="audiobook" label="Audiobooks" count={audiobooks.length} />
            {!audioOnly && <TabBtn k="ebook" label="Ebooks" count={ebooks.length} />}
          </div>
        )}
        <div className="thin-scroll max-h-[58vh] overflow-y-auto">
          {loading ? (
            <div className="p-8 text-center text-base text-ink-dim">Searching your indexers…</div>
          ) : rows.length === 0 ? (
            <div className="p-8 text-center text-base text-ink-dim">{emptyReleasesMessage(list, tab === "ebook" ? "ebook releases" : "audiobook releases")}</div>
          ) : (
            <div className="flex flex-col gap-2">
              {rows.map((rel, i) => (
                <BookReleaseRow key={rel.token ?? `${rel.title}#${i}`} rel={rel} busy={busy === (rel.token ?? rel.title)} grabbed={grabbed.has(rel.token ?? rel.title)} targets={rel.edition === "audiobook" ? targets : undefined} defaultTarget={defaultTarget} onGrab={(vid) => grab(rel, vid)} />
              ))}
            </div>
          )}
        </div>
    </Modal>
  );
}

function BookReleaseRow({ rel, busy, grabbed, targets, defaultTarget, onGrab }: { rel: RankedRelease; busy: boolean; grabbed: boolean; targets?: { id: number; label: string }[]; defaultTarget?: number; onGrab: (versionId: number) => void }) {
  // Default the target to the version being searched for, else the version the
  // release's own words point at.
  const [target, setTarget] = useState<number>(defaultTarget ?? rel.version_id ?? 0);
  // Usenet has no seeders to count. A release on a network with no download client set up
  // for it can't be grabbed at all; its reject reason says so.
  const usenet = isUsenetRelease(rel);
  const noClient = noClientFor(rel);
  const age = formatReleaseAge(rel.published_at);
  return (
    <div
      className="rounded-xl p-3"
      style={{
        background: rel.recommended ? "var(--accent-soft)" : "var(--panel-2)",
        border: `1px solid ${rel.recommended ? "var(--accent)" : "var(--line)"}`,
        opacity: rel.eligible ? 1 : 0.6,
      }}
    >
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            {rel.recommended && <span className="rounded px-1.5 py-0.5 text-xs font-bold uppercase" style={{ background: "var(--accent)", color: "var(--accent-ink)" }}>★ Recommended{rel.version ? ` · ${rel.version}` : targets && targets.length > 0 && rel.edition === "audiobook" ? " · Standard" : ""}</span>}
            {rel.version && !rel.recommended && <span className="rounded px-1.5 py-0.5 text-xs font-bold uppercase" style={{ background: "var(--panel)", color: "var(--accent-text)", border: "1px solid var(--accent-line)" }}>{rel.version}</span>}
            {rel.format && <span className="rounded px-1.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: "var(--panel)", color: "var(--ink-dim)" }}>{rel.format}</span>}
            {usenet && <UsenetChip />}
            {!rel.eligible && <span className="rounded px-1.5 py-0.5 text-xs uppercase" style={{ background: "var(--panel)", color: "var(--ink-faint)" }}>{noClient ? "can't grab" : rel.reject_reason ? "not grabbed automatically" : "not in profile"}</span>}
          </div>
          {/* The raw torrent name — narrator and edition detail live here. */}
          <div className="mt-1 break-words text-base font-medium">{rel.title}</div>
          {/* Another language: the automation skips it, a hand pick is still allowed. No
              client for its network: it can't be grabbed, and this says why. */}
          {rel.reject_reason && <div className="mt-0.5 text-xs" style={{ color: "var(--avoid-text)" }}>{rel.reject_reason}</div>}
          {/* Structured metadata (rich on trackers like MyAnonaMouse). */}
          {(rel.narrator || rel.series || rel.language) && (
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs">
              {rel.narrator && <span style={{ color: "var(--accent-text)" }}>🎙 {rel.narrator}</span>}
              {rel.series && <span className="text-ink-dim">📚 {rel.series}</span>}
              {rel.language && <span className="text-ink-faint">🌐 {rel.language}</span>}
            </div>
          )}
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-xs text-ink-faint">
            <span>{rel.size_gb.toFixed(2)} GB</span>
            {!usenet && <span title="Seeders / peers, as the indexer reports them">{rel.seeders}/{rel.peers ?? 0} seeders/peers</span>}
            {age && <span>{age} old</span>}
            <span>{rel.indexer}</span>
            {rel.info_url && <a href={rel.info_url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="font-semibold hover:underline" style={{ color: "var(--accent-text)" }}>view ↗</a>}
          </div>
        </div>
        <div className="flex flex-none flex-col items-end gap-1.5">
        {targets && targets.length > 0 && !grabbed && (
          <select
            value={target}
            onChange={(e) => setTarget(Number(e.target.value))}
            title="Which audiobook this download is filed as"
            aria-label="File the audio as"
            className="rounded-md px-1.5 py-1 text-xs"
            style={{ background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" }}
          >
            <option value={0}>as Standard</option>
            {targets.map((t) => <option key={t.id} value={t.id}>as {t.label}</option>)}
          </select>
        )}
        <Button
          variant="primary"
          onClick={() => onGrab(target)}
          disabled={grabbed || noClient || !rel.token}
          busy={busy}
          busyLabel="Grabbing…"
          className="flex-none disabled:opacity-60"
          style={grabbed ? { background: "var(--good)" } : undefined}
        >
          {grabbed ? "Grabbed ✓" : "Grab"}
        </Button>
        </div>
      </div>
    </div>
  );
}
