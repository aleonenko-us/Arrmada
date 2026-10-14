import { useState } from "react";
import { type Indexer, type NewIndexer } from "../../lib/api";
import { indexerApi } from "../../lib/indexerApi";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { DEFAULT_SEED_DAYS, INDEXER_PRESETS, type IndexerPreset } from "../../lib/indexerPresets";
import { Button, Modal } from "../../ui";
import { ALL_MEDIA, Labeled, MEDIA_TYPES, SeedingRules, TestLine, TIE_BREAK_HELP, field, fieldStyle, type TestState } from "./IndexerEditor";

// AddIndexerModal is "+ Add indexer" on Settings → Connections: pick a tracker from the
// catalogue, fill in only what that tracker needs, and Test it before a row exists. Save
// is the main button once a test has passed with exactly what's in the form; saving
// untested stays possible, one step down. It's loaded on open, so the catalogue and this
// form aren't part of the Connections page's download.

export default function AddIndexerModal({ onClose, onAdded, onProwlarr }: {
  onClose: () => void;
  onAdded: (created: Indexer) => void;
  /** The Prowlarr tile: open the Prowlarr card instead of a form. */
  onProwlarr: () => void;
}) {
  const [preset, setPreset] = useState<IndexerPreset | null>(null);
  const [saving, setSaving] = useState(false);
  return (
    <Modal onClose={onClose} title={preset ? `Add ${preset.label}` : "Add an indexer"} size="md" dismissible={!saving}>
      {preset ? (
        <PresetForm key={preset.id} preset={preset} onBack={() => setPreset(null)} onAdded={onAdded} onSaving={setSaving} />
      ) : (
        <Catalogue onPick={(p) => (p.kind === "prowlarr" ? onProwlarr() : setPreset(p))} />
      )}
    </Modal>
  );
}

function Catalogue({ onPick }: { onPick: (p: IndexerPreset) => void }) {
  return (
    <>
      <p className="mb-3 mt-1.5 text-sm text-ink-dim">Pick where releases come from. You can test it before anything is saved.</p>
      <ul className="m-0 grid list-none grid-cols-1 gap-2 p-0 min-[400px]:grid-cols-2">
        {INDEXER_PRESETS.map((p) => (
          <li key={p.id}>
            <button
              type="button"
              onClick={() => onPick(p)}
              className="flex w-full items-center gap-3 rounded-xl border border-line bg-panel-2 p-3 text-left transition-colors hover:border-accent"
            >
              <span aria-hidden className="grid h-9 w-9 flex-none place-items-center rounded-lg font-mono text-sm font-bold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>
                {p.monogram}
              </span>
              <span className="min-w-0">
                <span className="block text-md font-semibold">{p.label}</span>
                <span className="block text-xs text-ink-faint">{p.blurb}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </>
  );
}

function PresetForm({ preset, onBack, onAdded, onSaving }: {
  preset: IndexerPreset;
  onBack: () => void;
  onAdded: (created: Indexer) => void;
  onSaving: (busy: boolean) => void;
}) {
  const has = (f: IndexerPreset["fields"][number]) => preset.fields.includes(f);
  const [name, setName] = useState(preset.defaultName);
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState(""); // API key or mam_id session
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [scope, setScope] = useState<string[]>(preset.mediaTypes.length ? preset.mediaTypes : ALL_MEDIA);
  const [priority, setPriority] = useState(25);
  const [minSeeders, setMinSeeders] = useState(0);
  const [seedEnabled, setSeedEnabled] = useState(true);
  const [seedRatio, setSeedRatio] = useState(0);
  const [seedTime, setSeedTime] = useState(DEFAULT_SEED_DAYS);
  const [seedUnit, setSeedUnit] = useState<"hours" | "days">("days");
  const [test, setTest] = useState<TestState & { sig?: string }>({});
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const body = (): NewIndexer => ({
    name: name.trim() || preset.defaultName,
    kind: preset.kind,
    url: has("url") ? url.trim() : undefined,
    api_key: has("api_key") || has("mam_session") ? secret.trim() : undefined,
    username: has("username") ? username.trim() : undefined,
    password: has("password") ? password : undefined,
    // Every area lit is stored as [] ("all areas"), as the card's pills do.
    media_types: scope.length === ALL_MEDIA.length ? [] : ALL_MEDIA.filter((k) => scope.includes(k)),
    priority,
    min_seeders: minSeeders,
    seed_enabled: seedEnabled,
    seed_ratio: seedEnabled ? seedRatio : 0,
    seed_hours: seedEnabled ? (seedUnit === "days" ? seedTime * 24 : seedTime) : 0,
  });
  // A test answers for exactly what was in the form; change anything and it's stale.
  const sig = JSON.stringify(body());
  // Dirty once the form differs from the preset it opened with.
  const [opened] = useState(sig);
  const markClean = useUnsavedGuard(sig !== opened);
  const current = test.sig === sig ? test : {};
  const passed = current.ok === true;

  const missing =
    (has("url") && !preset.urlOptional && !url.trim()) ||
    (has("username") && !username.trim()) ||
    (has("password") && !password) ||
    (has("mam_session") && !secret.trim());

  const runTest = async () => {
    const b = body();
    const at = JSON.stringify(b);
    setTest({ loading: true, sig: at });
    try {
      const r = await indexerApi.testIndexerSettings(b);
      setTest({ ok: r.ok, error: r.error, caps: r.caps_summary, sig: at });
    } catch (e) {
      setTest({ ok: false, error: (e as Error).message, sig: at });
    }
  };

  const save = async () => {
    setSaving(true);
    onSaving(true);
    setError(null);
    try {
      const added = await indexerApi.createIndexer(body());
      markClean();
      onAdded(added);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
      onSaving(false);
    }
  };

  const toggleScope = (key: string) => {
    if (scope.includes(key) && scope.length === 1) return; // an indexer has to search for something
    setScope(scope.includes(key) ? scope.filter((k) => k !== key) : [...scope, key]);
  };

  return (
    <form
      aria-label="Add an indexer"
      onSubmit={(e) => { e.preventDefault(); if (!missing) void (passed ? save() : runTest()); }}
      className="mt-2"
    >
      <button type="button" onClick={onBack} disabled={saving} className="mb-2 text-sm text-ink-dim hover:text-ink">
        ← All trackers
      </button>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {has("username") && (
          <Labeled label="Username">
            <input className={field} style={fieldStyle} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" required />
          </Labeled>
        )}
        {has("password") && (
          <Labeled label="Password">
            <input type="password" className={field} style={fieldStyle} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
          </Labeled>
        )}
        {has("mam_session") && (
          <Labeled label="mam_id session" span2>
            <input className={field} style={fieldStyle} value={secret} onChange={(e) => setSecret(e.target.value)} placeholder="paste the mam_id session string from MyAnonaMouse" autoComplete="off" required />
          </Labeled>
        )}
        {has("url") && (
          <Labeled label={preset.urlOptional ? "Site URL (optional mirror)" : "Feed URL"} span2>
            <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder={preset.urlPlaceholder} required={!preset.urlOptional} inputMode="url" autoComplete="off" />
          </Labeled>
        )}
        {has("api_key") && (
          <Labeled label="API key" span2>
            <input type="password" className={field} style={fieldStyle} value={secret} onChange={(e) => setSecret(e.target.value)} autoComplete="off" />
          </Labeled>
        )}
      </div>

      <p className="mb-0 mt-3 text-xs text-ink-faint">{preset.help}</p>

      <div className="mt-3 flex flex-wrap items-center gap-1.5" role="group" aria-label="Used for">
        <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Used for</span>
        {MEDIA_TYPES.map((m) => {
          const on = scope.includes(m.key);
          return (
            <button
              key={m.key}
              type="button"
              aria-pressed={on}
              onClick={() => toggleScope(m.key)}
              className="rounded-full px-2.5 py-0.5 text-xs font-semibold transition-colors"
              style={{
                border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`,
                background: on ? "var(--accent-soft)" : "var(--panel-2)",
                color: on ? "var(--accent-text)" : "var(--ink-faint)",
              }}
            >
              {m.label}
            </button>
          );
        })}
      </div>

      <SeedingRules enabled={seedEnabled} ratio={seedRatio} time={seedTime} unit={seedUnit} onEnabled={setSeedEnabled} onRatio={setSeedRatio} onTime={setSeedTime} onUnit={setSeedUnit} />
      {preset.privateTracker && (
        <p className="mb-0 mt-2 text-xs text-ink-faint">
          A private tracker counts how long you seed. Keep these at least as long as its rules ask; {DEFAULT_SEED_DAYS} days covers most.
        </p>
      )}

      <details className="mt-3">
        <summary className="cursor-pointer text-sm text-ink-dim">More options: name, tie-break, minimum seeders</summary>
        <div className="mt-2.5 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Labeled label="Name" span2>
            <input className={field} style={fieldStyle} value={name} onChange={(e) => setName(e.target.value)} />
          </Labeled>
          <Labeled label="Tie-break priority (1–50)" hint={TIE_BREAK_HELP}>
            <input type="number" min={1} max={50} className={field} style={fieldStyle} value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
          </Labeled>
          <Labeled label="Min seeders (0 = off)">
            <input type="number" min={0} className={field} style={fieldStyle} value={minSeeders} onChange={(e) => setMinSeeders(Math.max(0, Number(e.target.value)))} />
          </Labeled>
        </div>
      </details>

      <div aria-live="polite">
        {!current.loading && current.ok !== undefined && <TestLine state={current} />}
      </div>
      {error && <div role="alert" className="mt-2 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}

      <div className="mt-4 flex flex-wrap justify-end gap-2.5">
        {passed ? (
          <>
            <Button onClick={runTest} disabled={saving || missing}>Test again</Button>
            <Button type="submit" variant="primary" busy={saving} busyLabel="Saving…" disabled={missing}>Save indexer</Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={save} busy={saving} busyLabel="Saving…" disabled={missing || current.loading}>Save without testing</Button>
            <Button type="submit" variant="primary" busy={current.loading} busyLabel="Testing…" disabled={missing || saving}>Test</Button>
          </>
        )}
      </div>
    </form>
  );
}
