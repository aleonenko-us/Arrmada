import { useRef, useState } from "react";
import { api, type Job } from "../lib/api";
import { scanLine, useLibraryScan } from "../lib/useLibraryScan";
import { jobDone } from "../lib/useJob";
import { UnmatchedReview } from "./UnmatchedReview";
import { Button } from "../ui";

// "Import the library you already have": starts the same Movies and TV library scans as
// Settings → Library's Scan buttons and the Series page's Tools → Scan library, follows
// their progress, and when they end says "N added, M need you" with each folder that needs
// you listed under it, with why and how to fix it. Shown under the step in Getting started
// (Dashboard card and the setup wizard's last step).

const n = (x: number) => x.toLocaleString();
const resultOf = (j: Job | null, key: "imported" | "unmatched"): number => {
  const v = (j?.result as Record<string, unknown> | null | undefined)?.[key];
  return j?.status === "succeeded" && typeof v === "number" ? v : 0;
};

// reportLine is the one-line result: "12 added, 3 need you".
export function reportLine(added: number, needYou: number): string {
  return `${n(added)} added, ${n(needYou)} need${needYou === 1 ? "s" : ""} you`;
}

export function LibraryImport({ onChanged }: { onChanged?: () => void }) {
  const [reloadKey, setReloadKey] = useState(0);
  const [ran, setRan] = useState(false);
  const changed = useRef(onChanged);
  changed.current = onChanged;
  const finished = () => { setReloadKey((k) => k + 1); changed.current?.(); };
  const movies = useLibraryScan("movie.scan", () => api.scanLibrary(), finished, "all");
  const tv = useLibraryScan("series.scan", () => api.scanSeries(), finished, "all");
  const running = movies.running || tv.running;

  const start = async () => {
    setRan(true);
    // One scan runs at a time; the second waits its turn and says so.
    await Promise.all([tv.start(), movies.start()]);
  };

  const lines = [scanLine("TV Shows", tv), scanLine("Movies", movies)].filter((l) => !!l);
  const done = ran && !running && [movies.job, tv.job].some((j) => j && jobDone(j.status));
  const added = resultOf(movies.job, "imported") + resultOf(tv.job, "imported");
  const need = resultOf(movies.job, "unmatched") + resultOf(tv.job, "unmatched");

  return (
    <div className="mx-2 mb-2 mt-1 rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="primary" size="sm" onClick={() => void start()} disabled={running} busy={running} busyLabel="Importing…">
          {done ? "Scan again" : "Import my library"}
        </Button>
        <span className="text-sm text-ink-dim">Reads your Movies and TV folders and adds what's there, unmonitored, so nothing is downloaded or changed.</span>
      </div>
      {lines.length > 0 && (
        <div role="status" className="mt-2 flex flex-col gap-0.5 text-sm">
          {lines.map((l, i) => (
            <span key={i} style={{ color: l!.tone === "good" ? "var(--good-text)" : l!.tone === "error" ? "var(--reject-text)" : "var(--ink-dim)" }}>{l!.text}</span>
          ))}
        </div>
      )}
      {done && (
        <p role="status" className="m-0 mt-2 text-base font-semibold">
          {reportLine(added, need)}{need === 0 ? " — all done." : ""}
        </p>
      )}
      {done && need > 0 && (
        <>
          <UnmatchedReview media="series" reloadKey={reloadKey} onChanged={onChanged} />
          <UnmatchedReview media="movie" reloadKey={reloadKey} onChanged={onChanged} />
        </>
      )}
    </div>
  );
}
