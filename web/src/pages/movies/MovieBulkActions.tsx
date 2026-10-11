import { useState } from "react";
import { api, type MovieSummary } from "../../lib/api";
import { useToast } from "../../ui";
import { bulkFailure, runBulk } from "../../features/library/bulk";
import type { LibrarySelection } from "../../features/library/types";

// MovieBulkActions are the Movies mass-editor controls in the library's bulk bar: they act
// on the grid's selection (grid and table alike) and, when done, clear it or keep the
// failures selected (runBulk). The mass editor (MOV-19) grows here.
export function MovieBulkActions({ sel, profiles, onDone }: {
  sel: LibrarySelection<MovieSummary>;
  profiles: { key: string; name: string }[];
  onDone: () => void;
}) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const none = sel.ids.size === 0 || busy;

  const monitor = async (mon: boolean) => {
    setBusy(true);
    try {
      const r = await runBulk(sel, (id) => api.setMonitored(id, mon));
      toast(r.failed.length ? bulkFailure(r) : `${r.ok.length} ${mon ? "monitored" : "unmonitored"}.`, { tone: r.failed.length ? "error" : "info" });
      onDone();
    } finally { setBusy(false); }
  };
  const profile = async (key: string) => {
    setBusy(true);
    try {
      const r = await runBulk(sel, (id) => api.setQualityProfile(id, key));
      const queued = r.ok.filter((x) => x.queued).length;
      if (r.failed.length) toast(bulkFailure(r), { tone: "error" });
      else toast(`Quality profile set on ${r.ok.length} movies.${queued ? ` ${queued} ${queued === 1 ? "search" : "searches"} queued — they run two at a time.` : ""}`);
      onDone();
    } finally { setBusy(false); }
  };

  return (
    <>
      <button onClick={() => monitor(true)} disabled={none} className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>Monitor</button>
      <button onClick={() => monitor(false)} disabled={none} className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Unmonitor</button>
      <span className="mx-1 h-5 w-px" style={{ background: "var(--line)" }} />
      <select
        value=""
        disabled={none}
        onChange={(e) => { if (e.target.value) void profile(e.target.value); }}
        aria-label="Set the quality profile of the selected movies"
        className="rounded-lg px-2.5 py-1.5 text-[11.5px] font-medium"
        style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
      >
        <option value="">Set quality profile…</option>
        {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
      </select>
    </>
  );
}
