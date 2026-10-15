import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import { isAdmin, useMe } from "../../lib/me";
import { qualityApi, type RankingShadowReport } from "../../lib/qualityApi";

// RankingCard is the Quality page's small Advanced switch between the ordered-key ranking
// (the default) and classic ranking (the rollback), with how often the two disagreed. Only
// an admin sees it: it changes what every profile grabs and upgrades. Whichever ranking
// decides, the other runs alongside and every disagreement is recorded for review.
export function RankingCard() {
  const { user } = useMe();
  const admin = isAdmin(user);
  const [mode, setMode] = useState<"v1" | "v2" | null>(null);
  const [report, setReport] = useState<RankingShadowReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!admin) return;
    api.settings().then((s) => setMode(modeOf(s.quality_ranking))).catch(() => setMode(null));
    qualityApi.rankingShadow(0).then(setReport).catch(() => setReport(null));
  }, [admin]);

  if (!admin || mode === null) return null;

  const change = async (next: "v1" | "v2") => {
    if (next === mode) return;
    setError(null);
    try {
      const s = await api.updateSettings({ quality_ranking: next });
      setMode(modeOf(s.quality_ranking));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't change the ranking");
    }
  };
  const opts: { v: "v1" | "v2"; label: string }[] = [{ v: "v2", label: "Ordered" }, { v: "v1", label: "Classic" }];
  const other = mode === "v2" ? "Classic ranking" : "The ordered ranking";

  return (
    <div id="ranking" className="mt-6 rounded-xl p-4" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div className="flex flex-wrap items-center gap-3">
        <span className="min-w-0 flex-1">
          <span className="block text-md font-semibold">Ranking</span>
          <span className="block text-xs text-ink-faint">
            Ordered, the default, compares releases one rule at a time — resolution, then your preferences, custom formats, source, bitrate — and records which rule decided. Upgrades follow the same rules. Classic adds every score together; switch back to it if Ordered picks badly.
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
      {report && (
        <p className="mt-2 text-xs text-ink-dim">
          {report.decisions === 0
            ? `${other} runs alongside on every automatic pick; nothing to compare yet.`
            : `${other} runs alongside on every automatic pick. The two picked differently ${report.disagreements} time${report.disagreements === 1 ? "" : "s"} in ${report.decisions} decision${report.decisions === 1 ? "" : "s"}.`}
          {report.disagreement_rows.length > 0 && (
            <> <a href="/api/v1/quality/ranking-shadow?format=csv" className="text-accent-text underline-offset-2 hover:underline">Download the differences (CSV)</a></>
          )}
        </p>
      )}
      {error && <p className="mt-2 text-xs" style={{ color: "var(--reject-text)" }}>{error}</p>}
    </div>
  );
}

// modeOf reads the setting as the server does: ordered (v2) unless classic was chosen.
function modeOf(v: string | undefined): "v1" | "v2" {
  return v === "v1" ? "v1" : "v2";
}
