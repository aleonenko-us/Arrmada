import { useEffect, useState } from "react";
import { musicApi, type AlbumEditionView } from "../../lib/musicApi";
import { jobFailed, jobToast, useJob } from "../../lib/useJob";
import { Button, Modal, useToast } from "../../ui";
import { Switch } from "../../ui/Switch";

// EditionPicker lists every edition of an album (MUS-30) and makes one its target: the
// edition its completeness and imports are measured against. The album's files move to
// the matching tracks of the chosen edition; a file that fits none of them stays on its
// old edition and is listed under "Other files". Nothing on disk is touched.
//
// Pinning keeps automatic switching off the album (by the edition policy, or by the
// matcher to fit a download); unpinned, a correct download of any acceptable edition can
// still move the album to that edition.

export function EditionPicker({ albumId, onClose, onChanged }: { albumId: number; onClose: () => void; onChanged: () => void }) {
  const toast = useToast();
  const [eds, setEds] = useState<AlbumEditionView[] | null>(null);
  const [pinned, setPinned] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [jobId, setJobId] = useState<number | null>(null);
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    let live = true;
    musicApi
      .albumEditions(albumId)
      .then((res) => {
        if (!live) return;
        setEds(res.editions);
        setPinned(res.pinned);
      })
      .catch((e: Error) => live && setError(e.message));
    return () => {
      live = false;
    };
  }, [albumId]);

  useJob(jobId, {
    onDone: (job) => {
      setJobId(null);
      setBusy(null);
      toast(jobToast(job, "Switched the edition."), { tone: jobFailed(job) ? "error" : undefined });
      if (!jobFailed(job)) {
        onChanged();
        onClose();
      }
    },
  });

  const choose = async (e: AlbumEditionView, pin: boolean) => {
    setBusy(e.id);
    try {
      const res = await musicApi.setAlbumEdition(albumId, e.id, pin);
      if (res.job_id) {
        setJobId(res.job_id); // reading its tracks from MusicBrainz first
        return;
      }
      const kept = res.kept ? `, ${res.kept} kept as other files` : "";
      toast(`Switched to ${e.words}${res.moved ? `: ${res.moved} file(s) moved${kept}` : ""}.`);
      setBusy(null);
      onChanged();
      onClose();
    } catch (err) {
      setBusy(null);
      toast((err as Error).message, { tone: "error" });
    }
  };

  const target = eds?.find((e) => e.target);
  // Unacceptable editions (bootlegs, video discs) only on request: they're rarely wanted.
  const shown = (eds ?? []).filter((e) => showAll || e.acceptable || e.target);
  const hidden = (eds?.length ?? 0) - shown.length;

  return (
    <Modal title="Edition" onClose={onClose} size="lg">
      <p className="m-0 mb-3 text-sm text-ink-dim">
        The album's tracks and completeness follow the edition chosen here. Files move with it; a file that fits no track of
        the new edition stays listed under Other files. Nothing on disk changes.
      </p>
      <div className="mb-3">
        <Switch
          size="sm"
          checked={pinned}
          disabled={!target || busy !== null}
          onChange={(next) => {
            setPinned(next);
            if (target) void choose(target, next);
          }}
        >
          Keep this edition (no automatic switching)
        </Switch>
      </div>
      {error && <div role="alert" className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
      {eds === null && !error && <div className="text-sm text-ink-dim">Loading the editions…</div>}
      {eds && eds.length === 0 && <div className="text-sm text-ink-dim">No editions listed yet: open the album again once MusicBrainz has answered.</div>}
      <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
        {shown.map((e) => (
          <li key={e.id} className="flex items-center gap-3 rounded-lg px-3 py-2" style={{ border: `1px solid ${e.target ? "var(--accent)" : "var(--line)"}`, background: "var(--panel)" }}>
            <div className="min-w-0 flex-1">
              <div className="truncate text-base">{e.words}</div>
              <div className="flex flex-wrap gap-x-3 font-mono text-xs text-ink-faint">
                {e.label && <span>{e.label}</span>}
                {e.status && e.status !== "Official" && <span>{e.status}</span>}
                {e.same_as_target && <span style={{ color: "var(--good-text)" }}>same tracks as the current edition</span>}
                {e.files > 0 && <span>{e.files} file(s) on it</span>}
                {!e.acceptable && <span>not used automatically</span>}
              </div>
            </div>
            {e.target ? (
              <span className="flex-none font-mono text-2xs font-bold uppercase" style={{ color: "var(--accent-text)" }}>
                {pinned ? "kept" : "current"}
              </span>
            ) : (
              <Button size="sm" busy={busy === e.id} busyLabel="Switching…" disabled={busy !== null} onClick={() => void choose(e, pinned)}>
                Use this
              </Button>
            )}
          </li>
        ))}
      </ul>
      {hidden > 0 && (
        <button className="mt-2 text-sm font-semibold" style={{ color: "var(--accent-text)" }} onClick={() => setShowAll(true)}>
          Show {hidden} more (promotions, bootlegs, video releases)
        </button>
      )}
    </Modal>
  );
}
