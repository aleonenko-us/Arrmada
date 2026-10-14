import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import { isAdmin, useMe } from "../../lib/me";
import { qualityApi, type RankingShadowReport } from "../../lib/qualityApi";

// RankingCard is the Quality page's small Advanced switch between classic ranking and the
// ordered-key ranking (preview), with what the preview would have changed so far. Only an
// admin sees it: it changes what every profile grabs. While classic ranking decides, the
// ordered ranking runs alongside and every disagreement is recorded for review.
export function RankingCard() {
  const { user } = useMe();
  const admin = isAdmin(user);
  const [mode, setMode] = useState<"v1" | "v2" | null>(null);
  const [report, setReport] = useState<RankingShadowReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!admin) return;
    api.settings().then((s) => setMode(s.quality_ranking === "v2" ? "v2" : "v1")).catch(() => setMode(null));
    qualityApi.rankingShadow(0).then(setReport).catch(() => setReport(null));
  }, [admin]);

  if (!admin || mode === null) return null;

  const change = async (next: "v1" | "v2") => {
    if (next === mode) return;
    setError(null);
    try {
      const s = await api.updateSettings({ quality_ranking: next });
      setMode(s.quality_ranking === "v2" ? "v2" : "v1");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't change the ranking");
    }
  };
  const opts: { v: "v1" | "v2"; label: string }[] = [{ v: "v1", label: "Classic" }, { v: "v2", label: "Ordered (preview)" }];

  return (
    <div id="ranking" className="mt-6 rounded-xl p-4" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div className="flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-1">
          <span className="block text-md font-semibold">Ranking</span>
          <span className="block text-xs text-ink-faint">
            Classic adds every score together. Ordered compares releases one rule at a time — resolution, then your preferences, custom formats, source, bitrate — and records which rule decided.
          </span>
        </span>
        <div role="radiogroup" aria-label="Ranking" className="inline-flex flex-none rounded-lg p-0.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
          {opts.map((o) => {
            const on = mode === o.v;
            return (
              <button key={o.v} role="radio" aria-checked={on} onClick={() => void change(o.v)} className="rounded-md px-2.5 py-1 text-xs font-semibold"
                style={{ background: on ? "var(--accent)" : "transparent", color: on ? "var(--accent-ink)" : "var(--ink-faint)" }}>
                {o.label}
              </button>
            );
          })}
        </div>
      </div>
      {report && mode === "v1" && (
        <p className="mt-2 text-xs text-ink-dim">
          {report.decisions === 0
            ? "The ordered ranking runs alongside classic on every automatic pick; nothing to compare yet."
            : `Alongside classic, the ordered ranking would have picked differently ${report.disagreements} time${report.disagreements === 1 ? "" : "s"} in ${report.decisions} decision${report.decisions === 1 ? "" : "s"}.`}
          {report.disagreement_rows.length > 0 && (
            <> <a href="/api/v1/quality/ranking-shadow?format=csv" className="text-accent-text underline-offset-2 hover:underline">Download the differences (CSV)</a></>
          )}
        </p>
      )}
      {error && <p className="mt-2 text-xs" style={{ color: "var(--reject-text)" }}>{error}</p>}
    </div>
  );
}
