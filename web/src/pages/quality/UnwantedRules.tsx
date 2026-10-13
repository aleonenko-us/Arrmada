import { useState } from "react";
import type { IdealFile, UnwantedAction, UnwantedItemInfo, UnwantedSettings } from "../../lib/api";

// The Unwanted pack in a profile's Rules: kinds of release almost nobody wants, each
// refused, avoided (taken only when nothing else is eligible) or allowed. Mirrors
// quality/unwanted.go; the rows and their defaults come from the server's catalog.

const ACTIONS: { v: UnwantedAction; label: string; means: string; bg: string; fg: string }[] = [
  { v: "reject", label: "Reject", means: "Never grabbed.", bg: "var(--reject)", fg: "#fff" },
  { v: "avoid", label: "Avoid", means: "Grabbed only when nothing else is available.", bg: "var(--avoid)", fg: "#fff" },
  { v: "allow", label: "Allow", means: "No opinion either way.", bg: "var(--line)", fg: "var(--ink)" },
];

const DV_NO_FALLBACK = "dv_no_fallback";
const LOW_QUALITY_GROUP = "low_quality_group";

// effectiveDefault is an item's default for this profile: Dolby Vision without a fallback
// is allowed when the target prefers or requires Dolby Vision (the server's exception).
export function effectiveDefault(item: UnwantedItemInfo, ideal: IdealFile | undefined): UnwantedAction {
  const dv = ideal?.hdr?.DV;
  if (item.key === DV_NO_FALLBACK && (dv === "want" || dv === "must")) return "allow";
  return item.default;
}

function actionOf(u: UnwantedSettings | undefined, item: UnwantedItemInfo, ideal: IdealFile | undefined): UnwantedAction {
  return u?.items?.[item.key] ?? effectiveDefault(item, ideal);
}

const sameList = (a: string[], b: string[]) =>
  a.length === b.length && [...a].map((x) => x.toLowerCase()).sort().join("\n") === [...b].map((x) => x.toLowerCase()).sort().join("\n");

// unwantedSummary names the profile's non-default choices for the Rules summary line:
// "3D allowed, upscaled refused".
export function unwantedSummary(u: UnwantedSettings | undefined, catalog: UnwantedItemInfo[], ideal: IdealFile | undefined, defaultGroups: string[]): string {
  const word: Record<UnwantedAction, string> = { reject: "refused", avoid: "avoided", allow: "allowed" };
  const parts: string[] = [];
  for (const item of catalog) {
    const set = u?.items?.[item.key];
    if (set && set !== effectiveDefault(item, ideal)) parts.push(`${item.label.charAt(0).toLowerCase()}${item.label.slice(1).replace(/ \(.*\)$/, "")} ${word[set]}`);
  }
  const groups = u?.low_quality_groups;
  if (groups && !sameList(groups, defaultGroups)) parts.push("edited low-quality groups");
  return parts.join(", ");
}

export function UnwantedRules({ value, onChange, catalog, ideal, defaultGroups }: {
  value: UnwantedSettings | undefined;
  onChange: (u: UnwantedSettings) => void;
  catalog: UnwantedItemInfo[];
  ideal: IdealFile | undefined;
  defaultGroups: string[];
}) {
  if (catalog.length === 0) return null;
  const u: UnwantedSettings = { items: value?.items ?? {}, low_quality_groups: value?.low_quality_groups ?? null };
  const setAction = (item: UnwantedItemInfo, a: UnwantedAction) => {
    const items = { ...(u.items ?? {}) };
    // The default is stored as no choice, so the profile keeps following it.
    if (a === effectiveDefault(item, ideal)) delete items[item.key];
    else items[item.key] = a;
    onChange({ ...u, items });
  };
  const groups = u.low_quality_groups ?? defaultGroups;
  const setGroups = (g: string[]) => onChange({ ...u, low_quality_groups: sameList(g, defaultGroups) ? null : g });

  return (
    <div className="mt-4">
      <div className="mb-0.5 text-sm font-semibold">Unwanted</div>
      <p className="mb-2 text-xs text-ink-faint">On for every profile. Avoid means grabbed only when nothing else is available.</p>
      <div className="flex flex-col gap-1.5">
        {catalog.map((item) => {
          const cur = actionOf(u, item, ideal);
          return (
            <div key={item.key} className="rounded-lg p-2.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
              <div className="flex flex-wrap items-center gap-2">
                <span className="min-w-0 flex-1">
                  <span className="block text-base font-semibold">{item.label}</span>
                  <span className="block text-xs text-ink-faint">{item.description}</span>
                </span>
                <ActionPicker value={cur} onChange={(a) => setAction(item, a)} label={item.label} />
              </div>
              {item.key === LOW_QUALITY_GROUP && cur !== "allow" && <GroupChips groups={groups} onChange={setGroups} />}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ActionPicker is the Reject / Avoid / Allow segmented control, in the PrefPicker style.
function ActionPicker({ value, onChange, label }: { value: UnwantedAction; onChange: (a: UnwantedAction) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex flex-none rounded-lg p-0.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      {ACTIONS.map((o) => {
        const on = value === o.v;
        return (
          <button key={o.v} role="radio" aria-checked={on} onClick={() => onChange(o.v)} className="rounded-md px-2 py-1 text-xs font-semibold"
            title={`${o.label}: ${o.means}`} aria-label={`${o.label} — ${o.means}`}
            style={{ background: on ? o.bg : "transparent", color: on ? o.fg : "var(--ink-faint)", minWidth: 46 }}>
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// GroupChips edits the low-quality group list. Groups match exactly, never as part of a name.
function GroupChips({ groups, onChange }: { groups: string[]; onChange: (g: string[]) => void }) {
  const [term, setTerm] = useState("");
  const add = () => {
    const g = term.trim();
    setTerm("");
    if (!g || groups.some((x) => x.toLowerCase() === g.toLowerCase())) return;
    onChange([...groups, g]);
  };
  return (
    <div className="mt-2">
      <div className="mb-1.5 flex flex-wrap gap-1.5">
        {groups.map((g) => (
          <span key={g} className="flex items-center gap-1.5 rounded-lg px-2 py-1 text-sm" style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }}>
            {g}
            <button onClick={() => onChange(groups.filter((x) => x !== g))} aria-label={`Remove ${g} from the low-quality groups`}>✕</button>
          </span>
        ))}
        {groups.length === 0 && <span className="text-xs text-ink-faint">No groups — nothing is matched.</span>}
      </div>
      <div className="flex items-center gap-2">
        <input value={term} onChange={(e) => setTerm(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} placeholder="Group name"
          aria-label="Add a low-quality group" className="min-w-0 flex-1 rounded-lg px-2.5 py-1.5 text-base"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
        <button onClick={add} className="rounded-lg px-3 py-1.5 text-sm font-semibold" style={{ border: "1px solid var(--avoid)", color: "var(--avoid-text)" }}>Add</button>
      </div>
    </div>
  );
}
