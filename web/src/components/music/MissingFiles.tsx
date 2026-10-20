import { useState } from "react";
import { musicApi } from "../../lib/musicApi";
import { jobFailed, jobToast, useJob } from "../../lib/useJob";
import { Button, useToast } from "../../ui";

// MissingFiles is the album page's note about track files the file check found gone from
// disk (MUS-15). Nothing downloads them again by itself — they may only be renamed, or on a
// disk that's away — so the search is a person's choice, here.
export function MissingFiles({ albumId, count, onDone }: { albumId: number; count: number; onDone: () => void }) {
  const toast = useToast();
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const job = useJob(jobId, {
    onDone: (j) => {
      setJobId(null);
      toast(jobToast(j, "Search finished."), { tone: jobFailed(j) ? "error" : "info" });
      onDone();
    },
  });
  const search = async () => {
    setStarting(true);
    try {
      const r = await musicApi.searchMissingTracks(albumId);
      if (r.job_id) setJobId(r.job_id);
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setStarting(false);
    }
  };
  return (
    <section aria-label="Track files gone from disk" className="mt-6 rounded-xl p-4" style={{ border: "1px solid var(--reject)", background: "var(--panel)" }}>
      <h2 className="m-0 mb-1 text-md font-bold">
        {count === 1 ? "1 track file is" : `${count} track files are`} gone from disk
      </h2>
      <p className="m-0 mb-3 text-sm text-ink-dim">
        Arrmada doesn't download them again by itself: they may only be renamed, or on a disk that's away, and their records are kept so they're linked
        again if they come back. Search for them if they're really gone.
      </p>
      <Button size="sm" onClick={() => void search()} busy={starting || job.running} busyLabel="Searching…">
        Search for missing tracks
      </Button>
    </section>
  );
}
