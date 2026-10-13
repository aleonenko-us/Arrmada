import { useState } from "react";
import type { QualityCustomFormat } from "../../lib/api";
import { qualityApi, type TrashImport } from "../../lib/qualityApi";
import { Button, Modal } from "../../ui";
import { formatSummary } from "./formatText";

// TrashImportDialog turns pasted TRaSH custom-format JSON into formats for the profile
// being edited. It shows each converted format with everything that couldn't be carried
// over, and adds the chosen ones with no score — nothing is saved until the profile is.
export function TrashImportDialog({ media, existing, onAdd, onClose }: {
  media: string; existing: string[]; onAdd: (formats: QualityCustomFormat[]) => void; onClose: () => void;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<TrashImport | null>(null);
  const [chosen, setChosen] = useState<Set<number>>(new Set());

  const convert = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await qualityApi.importTrash(text, media);
      setResult(r);
      setChosen(new Set(r.formats.map((_, i) => i)));
    } catch (e) {
      setResult(null);
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const toggle = (i: number) => setChosen((s) => {
    const next = new Set(s);
    if (next.has(i)) next.delete(i);
    else next.add(i);
    return next;
  });
  const taken = new Set(existing.map((n) => n.toLowerCase()));
  const picked = result?.formats.filter((_, i) => chosen.has(i)) ?? [];

  return (
    <Modal onClose={onClose} title="Import from TRaSH" size="lg" dismissible={!busy}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          {result ? (
            <Button variant="primary" disabled={picked.length === 0} onClick={() => onAdd(picked.map((f) => f.format))}>
              Add {picked.length} format{picked.length === 1 ? "" : "s"}
            </Button>
          ) : (
            <Button variant="primary" busy={busy} busyLabel="Converting…" disabled={!text.trim()} onClick={() => void convert()}>Convert</Button>
          )}
        </>
      }>
      <p className="text-sm text-ink-dim">
        Paste a custom format's JSON from the TRaSH guides — or several, as a JSON list. Formats are read with {media === "series" ? "Sonarr's" : "Radarr's"} numbering.
        They're added with no score for you to set, and nothing is saved until you save the profile.
      </p>
      <textarea value={text} onChange={(e) => { setText(e.target.value); setResult(null); }} aria-label="TRaSH custom-format JSON" rows={result ? 4 : 10} spellCheck={false}
        placeholder={'{\n  "name": "x265 (HD)",\n  "specifications": [ … ]\n}'}
        className="mt-3 w-full rounded-lg px-3 py-2 font-mono text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
      {error && <p role="alert" className="mt-2 text-sm" style={{ color: "var(--reject)" }}>{error}</p>}

      {result && (
        <div className="mt-3 flex flex-col gap-2">
          {result.warnings.length > 0 && (
            <div className="rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)" }}>
              <div className="font-semibold" style={{ color: "var(--reject)" }}>Not imported</div>
              <ul className="mt-1 flex flex-col gap-0.5 text-ink-dim">
                {result.warnings.map((w, i) => <li key={i}>{w}</li>)}
              </ul>
            </div>
          )}
          {result.formats.length === 0 && <p className="text-sm text-ink-dim">Nothing in that JSON can be imported.</p>}
          {result.formats.map((tf, i) => (
            <label key={i} className="flex items-start gap-2.5 rounded-lg p-2.5 text-sm" style={{ background: "var(--panel-2)" }}>
              <input type="checkbox" checked={chosen.has(i)} onChange={() => toggle(i)} className="mt-0.5" />
              <span className="min-w-0 flex-1">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-semibold">{tf.format.name}</span>
                  {tf.suggested_score != null && <span className="font-mono text-xs text-ink-faint">TRaSH suggests {tf.suggested_score > 0 ? "+" : ""}{tf.suggested_score}</span>}
                  {taken.has(tf.format.name.toLowerCase()) && <span className="text-xs" style={{ color: "var(--avoid-text)" }}>replaces the one in this profile</span>}
                </span>
                <span className="mt-0.5 block break-all font-mono text-xs text-ink-faint">{formatSummary(tf.format)}</span>
                {tf.warnings.length > 0 && (
                  <ul className="mt-1 flex flex-col gap-0.5 text-xs leading-[1.4]" style={{ color: "var(--avoid-text)" }}>
                    {tf.warnings.map((w, j) => <li key={j}>{w}</li>)}
                  </ul>
                )}
              </span>
            </label>
          ))}
        </div>
      )}
    </Modal>
  );
}
