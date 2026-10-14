import { useState } from "react";
import type { GroupTiers, StoredProfile } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { useQuery } from "../../lib/query";

// GroupTiersEditor is the Advanced area's "Release groups": two tiers of groups the profile
// trusts most and next. Under ranking v2 they order otherwise-equal releases after source;
// they never refuse anything. A profile that hasn't edited them runs the suggestions the
// server ships (presets.go), shown here as its starting point.

const NO_TIERS: GroupTiers = { tier1: [], tier2: [] };
const NO_GROUPS: string[] = [];

export function GroupTiersEditor({ sp, patch }: { sp: StoredProfile; patch: (p: Partial<StoredProfile>) => void }) {
  // The suggestions and the default low-quality list come with the page's profile list.
  const listQ = useQuery(`quality:${sp.media_type}`, () => qualityApi.qualityProfiles(sp.media_type), { staleMs: 60_000 });
  const defaults = listQ.data?.group_tiers ?? NO_TIERS;
  const lowGroups = sp.unwanted?.low_quality_groups ?? listQ.data?.unwanted_groups ?? NO_GROUPS;
  const tiers = sp.group_tiers ?? defaults;
  const [note, setNote] = useState<string | null>(null);

  const set = (next: GroupTiers) => { setNote(null); patch({ group_tiers: next }); };
  const add = (tier: "tier1" | "tier2", name: string) => {
    const g = name.trim();
    if (!g) return;
    const lc = g.toLowerCase();
    if (lowGroups.some((x) => x.toLowerCase() === lc)) {
      setNote(`${g} is one of your low-quality groups (Rules → Unwanted) — take it out there first.`);
      return;
    }
    const other = tier === "tier1" ? "tier2" : "tier1";
    // Adding a group already in the other tier moves it.
    set({ ...tiers, [tier]: tiers[tier].some((x) => x.toLowerCase() === lc) ? tiers[tier] : [...tiers[tier], g], [other]: tiers[other].filter((x) => x.toLowerCase() !== lc) } as GroupTiers);
  };
  const remove = (tier: "tier1" | "tier2", g: string) => set({ ...tiers, [tier]: tiers[tier].filter((x) => x !== g) } as GroupTiers);

  return (
    <div>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-sm font-semibold">Release groups</span>
        {sp.group_tiers && (
          <button onClick={() => { setNote(null); patch({ group_tiers: undefined }); }} className="text-xs text-ink-dim hover:text-[var(--ink)]">Use the suggestions</button>
        )}
      </div>
      <p className="mb-2 text-xs text-ink-faint">
        With ranking v2, a tier-1 group ranks above tier 2, and tier 2 above the rest, when releases are otherwise level after source. Classic ranking ignores tiers.
        Tiers never refuse a release — groups to stay away from go in Rules → Unwanted.
        {!sp.group_tiers && " These are suggestions until you change them."}
      </p>
      <TierRow label="Tier 1" groups={tiers.tier1} onAdd={(g) => add("tier1", g)} onRemove={(g) => remove("tier1", g)} />
      <TierRow label="Tier 2" groups={tiers.tier2} onAdd={(g) => add("tier2", g)} onRemove={(g) => remove("tier2", g)} />
      {note && <p role="alert" className="mt-1 text-xs" style={{ color: "var(--avoid-text)" }}>{note}</p>}
    </div>
  );
}

function TierRow({ label, groups, onAdd, onRemove }: { label: string; groups: string[]; onAdd: (g: string) => void; onRemove: (g: string) => void }) {
  const [term, setTerm] = useState("");
  const add = () => { onAdd(term); setTerm(""); };
  return (
    <div className="mb-2.5">
      <div className="mb-1 font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{label}</div>
      <div className="mb-1.5 flex flex-wrap gap-1.5">
        {groups.map((g) => (
          <span key={g} className="flex items-center gap-1.5 rounded-lg px-2 py-1 text-sm" style={{ background: "var(--good-soft)", color: "var(--good-text)" }}>
            {g}
            <button onClick={() => onRemove(g)} aria-label={`Remove ${g} from ${label}`}>✕</button>
          </span>
        ))}
        {groups.length === 0 && <span className="text-xs text-ink-faint">No groups.</span>}
      </div>
      <div className="flex items-center gap-2">
        <input value={term} onChange={(e) => setTerm(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} placeholder="Group name"
          aria-label={`Add a group to ${label}`} className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 text-base"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
        <button onClick={add} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>Add</button>
      </div>
    </div>
  );
}
