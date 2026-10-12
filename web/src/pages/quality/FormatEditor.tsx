import { useEffect, useMemo, useState } from "react";
import type { QualityCondition, QualityCustomFormat, StoredProfile } from "../../lib/api";
import { qualityApi, type FormatCheck, type FormatProblem, type VocabValue } from "../../lib/qualityApi";
import { useQuery } from "../../lib/query";

// The custom-format editor in the builder's Advanced area: one card per format, each with
// several conditions (NOT, match all/any), a score, and an edit form that changes the
// format in place. Nothing here is saved until the profile is — it only patches the
// profile being edited.

const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };

export const CONDITION_TYPES: { v: string; l: string }[] = [
  { v: "source", l: "Source" },
  { v: "resolution", l: "Resolution" },
  { v: "codec", l: "Codec" },
  { v: "dynamic_range", l: "Dynamic range" },
  { v: "audio", l: "Audio" },
  { v: "edition", l: "Edition" },
  { v: "release_group", l: "Release group" },
  { v: "release_title", l: "Name matches (regex)" },
  { v: "lossless", l: "Lossless audio" },
];

const TYPE_LABEL: Record<string, string> = Object.fromEntries(CONDITION_TYPES.map((t) => [t.v, t.l]));

// What a free-text value looks like, per type. The enumerable types are dropdowns.
const PLACEHOLDER: Record<string, string> = {
  release_group: "FLUX — or several: FLUX|NTb",
  release_title: "\\bFraMeSToR\\b",
};

const NO_VOCAB: Record<string, VocabValue[]> = {};
const NO_FORMATS: QualityCustomFormat[] = [];

type Draft = { index: number; original: string; format: QualityCustomFormat; score: number }; // index -1 = a new format

function emptyCondition(type: string, vocab: Record<string, VocabValue[]>): QualityCondition {
  return { type, value: vocab[type]?.[0]?.value ?? "" };
}

// summary reads a format back as one line: "Source BluRay AND NOT Release group YTS".
function summary(f: QualityCustomFormat): string {
  const join = f.match === "any" ? " OR " : " AND ";
  return f.conditions
    .map((c) => `${c.negate ? "NOT " : ""}${TYPE_LABEL[c.type] ?? c.type}${c.type === "lossless" ? "" : ` ${c.value}`}`)
    .join(join);
}

// tidy is a format as it's stored: names and values trimmed (a pattern is kept exactly),
// and the defaults (match all, not negated) left out.
function tidy(f: QualityCustomFormat): QualityCustomFormat {
  const out: QualityCustomFormat = {
    name: f.name.trim(),
    conditions: f.conditions.map((c) => {
      const cond: QualityCondition = { type: c.type, value: c.type === "release_title" ? c.value : c.type === "lossless" ? "" : c.value.trim() };
      if (c.negate) cond.negate = true;
      return cond;
    }),
  };
  if (f.match === "any") out.match = "any";
  return out;
}

export function CustomFormatsEditor({ sp, patch }: { sp: StoredProfile; patch: (p: Partial<StoredProfile>) => void }) {
  // The vocabulary comes with the profile list the page already loaded (same cache entry).
  const listQ = useQuery(`quality:${sp.media_type}`, () => qualityApi.qualityProfiles(sp.media_type), { staleMs: 60_000 });
  const vocab = listQ.data?.vocabulary ?? NO_VOCAB;
  const formats = sp.custom_formats ?? NO_FORMATS;
  const [draft, setDraft] = useState<Draft | null>(null);
  const [check, setCheck] = useState<FormatCheck | null>(null);

  // What's checked is the formats as they'd be with the open edit applied, so a bad
  // pattern is flagged while it's typed, before Done.
  const checked = useMemo(() => {
    if (!draft) return formats;
    const f = tidy(draft.format);
    return draft.index < 0 ? [...formats, f] : formats.map((x, i) => (i === draft.index ? f : x));
  }, [formats, draft]);

  useEffect(() => {
    if (checked.length === 0) return;
    let alive = true;
    const t = window.setTimeout(() => {
      qualityApi.checkFormats(checked).then((c) => { if (alive) setCheck(c); }).catch(() => {});
    }, 300);
    return () => { alive = false; window.clearTimeout(t); };
  }, [checked]);

  // A check answered for a different number of formats would put its problems on the
  // wrong cards; it's ignored until the next one lands.
  const live = check && check.matches.length === checked.length ? check : null;
  const problemsFor = (index: number) => ({
    errors: live?.errors.filter((p) => p.index === index) ?? [],
    warnings: live?.warnings.filter((p) => p.index === index) ?? [],
  });

  const setScore = (name: string, score: number) => {
    const next = { ...sp.format_scores };
    if (score === 0) delete next[name];
    else next[name] = score;
    patch({ format_scores: next });
  };
  const remove = (index: number) => {
    const name = formats[index].name;
    const next = { ...sp.format_scores };
    delete next[name];
    patch({
      custom_formats: formats.filter((_, i) => i !== index),
      format_scores: next,
      required_formats: (sp.required_formats ?? []).filter((n) => n !== name),
    });
    if (draft && draft.index === index) setDraft(null);
  };
  const startNew = () => setDraft({ index: -1, original: "", score: 50, format: { name: "", match: "all", conditions: [emptyCondition("release_group", vocab)] } });
  const startEdit = (index: number) => {
    const f = formats[index];
    setDraft({ index, original: f.name, score: sp.format_scores[f.name] ?? 0, format: { ...f, match: f.match === "any" ? "any" : "all", conditions: f.conditions.map((c) => ({ ...c })) } });
  };

  // Done writes the edit into the profile in place. A rename carries the score (and a
  // "must" on it) over to the new name.
  const commit = () => {
    if (!draft) return;
    const f = tidy(draft.format);
    const scores = { ...sp.format_scores };
    let required = sp.required_formats ?? [];
    if (draft.original && draft.original !== f.name) {
      delete scores[draft.original];
      required = required.map((n) => (n === draft.original ? f.name : n));
    }
    if (draft.score === 0) delete scores[f.name];
    else scores[f.name] = draft.score;
    const list = draft.index < 0 ? [...formats, f] : formats.map((x, i) => (i === draft.index ? f : x));
    patch({ custom_formats: list, format_scores: scores, required_formats: required });
    setDraft(null);
  };

  const draftIndex = draft ? (draft.index < 0 ? formats.length : draft.index) : -1;

  return (
    <div>
      <div className="text-[12px] font-semibold">Custom formats</div>
      <p className="mb-2 text-[10.5px] text-ink-faint">
        Match anything the parser reads — a source, a release group, a pattern in the name — and score it. Values like HEVC, Dolby Vision or Blu-ray are understood however they're written.
      </p>
      <div className="flex flex-col gap-2">
        {formats.map((f, i) =>
          draft && draft.index === i ? (
            <FormatForm key={`edit-${i}`} draft={draft} vocab={vocab} others={formats.filter((_, j) => j !== i).map((x) => x.name)}
              problems={problemsFor(draftIndex)} onChange={setDraft} onCancel={() => setDraft(null)} onDone={commit} />
          ) : (
            <FormatCard key={`${i}-${f.name}`} format={f} score={sp.format_scores[f.name] ?? 0} required={(sp.required_formats ?? []).includes(f.name)}
              matches={live?.matches[i]} samples={live?.samples} problems={problemsFor(i)} editing={!!draft}
              onScore={(s) => setScore(f.name, s)} onEdit={() => startEdit(i)} onRemove={() => remove(i)} />
          ),
        )}
        {draft && draft.index < 0 && (
          <FormatForm draft={draft} vocab={vocab} others={formats.map((x) => x.name)} problems={problemsFor(draftIndex)}
            onChange={setDraft} onCancel={() => setDraft(null)} onDone={commit} />
        )}
      </div>
      {!draft && (
        <div className="mt-2 flex flex-wrap gap-2">
          <button onClick={startNew} className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>+ New custom format</button>
        </div>
      )}
    </div>
  );
}

function Problems({ errors, warnings }: { errors: FormatProblem[]; warnings: FormatProblem[] }) {
  if (errors.length === 0 && warnings.length === 0) return null;
  return (
    <ul className="mt-1.5 flex flex-col gap-0.5 text-[10.5px] leading-[1.4]">
      {errors.map((p, i) => <li key={`e${i}`} style={{ color: "var(--reject)" }}>{p.message}</li>)}
      {warnings.map((p, i) => <li key={`w${i}`} style={{ color: "var(--avoid-text)" }}>{p.message}</li>)}
    </ul>
  );
}

function FormatCard({ format, score, required, matches, samples, problems, editing, onScore, onEdit, onRemove }: {
  format: QualityCustomFormat; score: number; required: boolean; matches?: number; samples?: number;
  problems: { errors: FormatProblem[]; warnings: FormatProblem[] }; editing: boolean;
  onScore: (s: number) => void; onEdit: () => void; onRemove: () => void;
}) {
  return (
    <div className="rounded-lg p-2.5 text-[11.5px]" style={{ background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 break-words font-semibold">{format.name}</span>
        {format.match === "any" && <span className="rounded px-1.5 py-px font-mono text-[9.5px] font-bold uppercase" style={{ background: "var(--panel)", color: "var(--ink-dim)" }}>any</span>}
        {required && <span className="rounded px-1.5 py-px font-mono text-[9.5px] font-bold uppercase" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>must</span>}
        <span className="ml-auto flex items-center gap-2">
          <input type="number" aria-label={`${format.name} score`} value={score} onChange={(e) => onScore(Number(e.target.value))}
            className="w-[64px] rounded-lg px-2 py-0.5 text-right font-mono text-[11.5px]" style={{ ...fieldStyle, color: score >= 0 ? "var(--good)" : "var(--reject)" }} />
          <button onClick={onEdit} disabled={editing} className="text-[11px] font-semibold text-ink-dim hover:text-[var(--ink)] disabled:opacity-40">Edit</button>
          <button onClick={onRemove} aria-label={`Remove ${format.name}`} className="text-ink-faint hover:text-[var(--reject)]">✕</button>
        </span>
      </div>
      <div className="mt-1 break-all font-mono text-[10.5px] text-ink-faint">{summary(format)}</div>
      {matches != null && samples != null && (
        <div className="mt-1 text-[10.5px] text-ink-faint">Matches {matches} of the {samples} sample releases</div>
      )}
      <Problems {...problems} />
    </div>
  );
}

function FormatForm({ draft, vocab, others, problems, onChange, onCancel, onDone }: {
  draft: Draft; vocab: Record<string, VocabValue[]>; others: string[];
  problems: { errors: FormatProblem[]; warnings: FormatProblem[] };
  onChange: (d: Draft) => void; onCancel: () => void; onDone: () => void;
}) {
  const f = draft.format;
  const set = (p: Partial<QualityCustomFormat>) => onChange({ ...draft, format: { ...f, ...p } });
  const setCond = (j: number, c: QualityCondition) => set({ conditions: f.conditions.map((x, k) => (k === j ? c : x)) });
  const name = f.name.trim();
  // The checks the form can make itself; the server's (a pattern that doesn't compile, a
  // built-in's name) arrive with the live check.
  const local = !name ? "Give the format a name."
    : others.some((o) => o.trim().toLowerCase() === name.toLowerCase()) ? "Another custom format already has this name."
      : f.conditions.length === 0 ? "Add at least one condition."
        : f.conditions.some((c) => c.type !== "lossless" && !c.value.trim()) ? "Every condition needs a value."
          : null;
  const blocked = !!local || problems.errors.length > 0;
  const formErrors = problems.errors.filter((p) => p.condition < 0);
  return (
    <div className="rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--accent-line)" }}>
      <div className="flex flex-wrap items-center gap-2">
        <input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="Tier 1 groups" aria-label="Format name"
          className="min-w-0 flex-1 basis-[180px] rounded-lg px-2.5 py-1.5 text-[12px]" style={fieldStyle} />
        <div className="inline-flex rounded-lg p-0.5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }} role="group" aria-label="How the conditions combine">
          {(["all", "any"] as const).map((m) => {
            const on = (f.match ?? "all") === m;
            return (
              <button key={m} onClick={() => set({ match: m })} aria-pressed={on} className="rounded-md px-2.5 py-1 text-[11px] font-semibold"
                style={{ background: on ? "var(--accent)" : "transparent", color: on ? "var(--accent-ink)" : "var(--ink-faint)" }}>
                {m === "all" ? "Match all" : "Match any"}
              </button>
            );
          })}
        </div>
      </div>

      <div className="mt-2.5 flex flex-col gap-2">
        {f.conditions.map((c, j) => (
          <ConditionRow key={j} index={j} cond={c} vocab={vocab} only={f.conditions.length === 1}
            errors={problems.errors.filter((p) => p.condition === j)} warnings={problems.warnings.filter((p) => p.condition === j)}
            onChange={(next) => setCond(j, next)} onRemove={() => set({ conditions: f.conditions.filter((_, k) => k !== j) })} />
        ))}
      </div>
      <button onClick={() => set({ conditions: [...f.conditions, emptyCondition("source", vocab)] })}
        className="mt-2 text-[11px] font-semibold" style={{ color: "var(--accent)" }}>+ Add condition</button>

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t pt-2.5" style={{ borderColor: "var(--line)" }}>
        <label className="flex items-center gap-1.5 text-[11px] text-ink-dim">
          Score
          <input type="number" value={draft.score} onChange={(e) => onChange({ ...draft, score: Number(e.target.value) })}
            className="w-[72px] rounded-lg px-2 py-1 text-right font-mono text-[12px]" style={{ ...fieldStyle, color: draft.score >= 0 ? "var(--good)" : "var(--reject)" }} />
        </label>
        <span className="ml-auto flex items-center gap-2">
          <button onClick={onCancel} className="rounded-lg px-3 py-1.5 text-[11.5px] text-ink-dim">Cancel</button>
          <button onClick={onDone} disabled={blocked} className="rounded-lg px-3 py-1.5 text-[11.5px] font-semibold disabled:opacity-40"
            style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>Done</button>
        </span>
      </div>
      {local && <p className="mt-1.5 text-[10.5px] text-ink-faint">{local}</p>}
      <Problems errors={formErrors} warnings={problems.warnings.filter((p) => p.condition < 0)} />
      <p className="mt-1.5 text-[10.5px] text-ink-faint">Applies to the profile when you press Done; Save the profile to keep it.</p>
    </div>
  );
}

function ConditionRow({ index, cond, vocab, only, errors, warnings, onChange, onRemove }: {
  index: number; cond: QualityCondition; vocab: Record<string, VocabValue[]>; only: boolean;
  errors: FormatProblem[]; warnings: FormatProblem[];
  onChange: (c: QualityCondition) => void; onRemove: () => void;
}) {
  const options = vocab[cond.type] ?? [];
  const n = index + 1;
  const changeType = (type: string) => onChange({ ...emptyCondition(type, vocab), negate: cond.negate });
  let value: React.ReactNode;
  if (cond.type === "lossless") {
    value = <span className="min-w-0 flex-1 text-[11px] text-ink-faint">has a lossless audio track</span>;
  } else if (options.length > 0) {
    // A value saved by hand ("HEVC", "TrueHD|DTS-HD") stays selectable as it was saved.
    const known = options.some((o) => o.value === cond.value);
    value = (
      <select value={cond.value} onChange={(e) => onChange({ ...cond, value: e.target.value })} aria-label={`Condition ${n} value`}
        className="min-w-0 flex-1 basis-[140px] rounded-lg px-2 py-1.5 text-[12px]" style={fieldStyle}>
        {!known && <option value={cond.value}>{cond.value ? `${cond.value} (as saved)` : "Choose…"}</option>}
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    );
  } else {
    value = (
      <input value={cond.value} onChange={(e) => onChange({ ...cond, value: e.target.value })} placeholder={PLACEHOLDER[cond.type] ?? ""}
        aria-label={`Condition ${n} value`} spellCheck={false} aria-invalid={errors.length > 0}
        className={`min-w-0 flex-1 basis-[140px] rounded-lg px-2.5 py-1.5 text-[12px] ${cond.type === "release_title" ? "font-mono" : ""}`}
        style={{ ...fieldStyle, borderColor: errors.length > 0 ? "var(--reject)" : "var(--line)" }} />
    );
  }
  return (
    <div>
      <div className="flex flex-wrap items-center gap-1.5">
        <select value={cond.type} onChange={(e) => changeType(e.target.value)} aria-label={`Condition ${n} type`}
          className="w-full rounded-lg px-2 py-1.5 text-[12px] sm:w-[160px]" style={fieldStyle}>
          {CONDITION_TYPES.map((t) => <option key={t.v} value={t.v}>{t.l}</option>)}
          {!TYPE_LABEL[cond.type] && <option value={cond.type}>{cond.type}</option>}
        </select>
        {value}
        <button onClick={() => onChange({ ...cond, negate: !cond.negate })} aria-pressed={!!cond.negate} aria-label={`Condition ${n}: NOT`}
          title="Match releases that DON'T have this" className="rounded-md px-2 py-1 font-mono text-[10.5px] font-bold"
          style={{ background: cond.negate ? "var(--reject)" : "var(--panel)", color: cond.negate ? "#fff" : "var(--ink-faint)", border: "1px solid var(--line)" }}>NOT</button>
        <button onClick={onRemove} disabled={only} aria-label={`Remove condition ${n}`} className="px-1 text-ink-faint hover:text-[var(--reject)] disabled:opacity-30">✕</button>
      </div>
      <Problems errors={errors} warnings={warnings} />
    </div>
  );
}
