import { useState } from "react";
import { musicSearchApi } from "../../lib/musicSearchApi";
import { jobFailed, jobToast, useJob } from "../../lib/useJob";
import { Button, useToast } from "../../ui";

// ArtistSearchButton is the artist page's Search monitored (MUS-09): one job that searches
// each of the artist's monitored albums still missing tracks, one at a time, and says what
// it found. A second click while it runs gets the same job back.
export function ArtistSearchButton({ artistId, onDone }: { artistId: number; onDone: () => void }) {
  const toast = useToast();
  const [jobId, setJobId] = useState<number | null>(null);
  const [starting, setStarting] = useState(false);
  const job = useJob(jobId, {
    // An artist with many wanted albums is searched one album at a time.
    giveUpMs: 30 * 60 * 1000,
    onDone: (j) => {
      setJobId(null);
      toast(jobToast(j, "Search finished."), { tone: jobFailed(j) ? "error" : "info" });
      onDone();
    },
  });
  const start = async () => {
    setStarting(true);
    try {
      const r = await musicSearchApi.searchArtist(artistId);
      if (r.job_id) setJobId(r.job_id);
      toast(r.existing ? "Already searching this artist's albums." : "Searching this artist's wanted albums…");
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setStarting(false);
    }
  };
  return (
    <Button size="sm" onClick={start} busy={starting || job.running} busyLabel="Searching…"
      title="Search the indexers now for every monitored album that's missing tracks">
      Search monitored
    </Button>
  );
}
