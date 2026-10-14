import { useEffect, useId, useState } from "react";
import { type Series as SeriesT } from "../../lib/api";
import { seriesApi } from "../../lib/seriesApi";
import { qualityApi } from "../../lib/qualityApi";

export function ProfileSelector({ series, onChange }: { series: SeriesT; onChange: () => void }) {
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [fallbackName, setFallbackName] = useState<string | null>(null);
  const profileId = useId();
  const cur = series.quality_profile;
  useEffect(() => { qualityApi.qualityProfiles("series").then((r) => setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })))).catch(() => {}); }, []);
  useEffect(() => {
    if (!cur || cur === "n/a" || profiles.some((p) => p.key === cur)) { setFallbackName(null); return; }
    qualityApi.qualityProfile(cur).then((sp) => setFallbackName(sp.name || cur)).catch(() => setFallbackName(cur));
  }, [cur, profiles]);
  const change = async (p: string) => { if (p === cur) return; await seriesApi.setSeriesProfile(series.id, p); onChange(); };
  const opts = [...profiles];
  if (cur && !opts.some((o) => o.key === cur)) opts.unshift({ key: cur, name: cur === "n/a" ? "Not set" : (fallbackName ?? "…") });
  return (
    <div className="flex items-center gap-2">
      <label htmlFor={profileId} className="font-mono text-2xs uppercase text-ink-faint">Quality</label>
      <select id={profileId} value={series.quality_profile} onChange={(e) => change(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-sm font-medium" style={{ background: "var(--accent-soft)", border: "1px solid var(--line)", color: "var(--accent-text)" }}>
        {opts.map((p) => <option key={p.key} value={p.key} style={{ background: "var(--panel)", color: "var(--ink)" }}>{p.name}</option>)}
      </select>
    </div>
  );
}
