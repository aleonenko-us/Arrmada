import { useState } from "react";
import { api, type Indexer, type NewIndexer } from "../../lib/api";

// The indexer forms, moved from the old Indexers page into the cards on Settings →
// Connections: adding one, editing one (with its seed rules), testing settings before they
// are saved, and the "Used for" pills. INT-04's fixes stay: a new indexer is named after
// its kind until someone types a name, the pills read "searched for this area", and the
// grids are one column on a phone.

type TestState = { loading?: boolean; ok?: boolean; error?: string; caps?: string };

// Priority only orders results that are otherwise level, so say so wherever it shows.
export const TIE_BREAK_HELP = "Only breaks ties between equally seeded results, such as the same release from two indexers. 1 = preferred.";

const field = "w-full rounded-lg px-3 py-2 text-[13px]";
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

// TestLine is a Test's answer: connected (with what the indexer supports) or why not.
function TestLine({ state }: { state: TestState }) {
  return (
    <div className="mt-2.5 break-words font-mono text-[11px]" style={{ color: state.ok ? "var(--good)" : "var(--reject)" }}>
      {state.ok ? `✓ Connected${state.caps ? ` · ${state.caps}` : ""}` : `✕ ${state.error ?? "failed"}`}
    </div>
  );
}

// TestSettings checks what's in a form without saving it. A typed key or password goes
// out for this one check only; with an id, blanks mean the saved ones.
function TestSettings({ body }: { body: () => NewIndexer & { id?: number } }) {
  const [state, setState] = useState<TestState>({});
  const run = async () => {
    setState({ loading: true });
    try {
      const r = await api.testIndexerSettings(body());
      setState({ ok: r.ok, error: r.error, caps: r.caps_summary });
    } catch (e) {
      setState({ ok: false, error: (e as Error).message });
    }
  };
  return (
    <div className="mt-3">
      <button type="button" onClick={run} disabled={state.loading} className="rounded-lg px-3 py-1.5 text-[12px]" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>
        {state.loading ? "Testing…" : "Test these settings"}
      </button>
      {!state.loading && state.ok !== undefined && <TestLine state={state} />}
    </div>
  );
}

// The name a new indexer gets from its kind, until the user types their own.
const KIND_NAMES: Record<string, string> = {
  "1337x": "1337x",
  torrentleech: "TorrentLeech",
  myanonamouse: "MyAnonaMouse",
  torznab: "Torznab",
};

export function AddIndexerForm({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState(KIND_NAMES["1337x"]);
  const [nameTouched, setNameTouched] = useState(false);
  const [kind, setKind] = useState("1337x");
  const changeKind = (k: string) => {
    setKind(k);
    if (!nameTouched) setName(KIND_NAMES[k] ?? k);
  };
  const [url, setUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [priority, setPriority] = useState(25);
  const [minSeeders, setMinSeeders] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const isTL = kind === "torrentleech";
  const is1337 = kind === "1337x";
  const isMAM = kind === "myanonamouse";

  const body = (): NewIndexer =>
    isTL
      ? { name, kind, username, password, api_key: apiKey, priority, min_seeders: minSeeders }
      : isMAM
        ? { name, kind, api_key: apiKey, media_types: ["book"], priority, min_seeders: minSeeders }
        : is1337
          ? { name, kind, url, priority, min_seeders: minSeeders }
          : { name, kind, url, api_key: apiKey, priority, min_seeders: minSeeders };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await api.createIndexer(body());
      onAdded();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} aria-label="Add an indexer" className="rounded-xl p-4" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Labeled label="Name">
          <input className={field} style={fieldStyle} value={name} onChange={(e) => { setName(e.target.value); setNameTouched(true); }} required />
        </Labeled>
        <Labeled label="Kind">
          <select className={field} style={fieldStyle} value={kind} onChange={(e) => changeKind(e.target.value)}>
            <option value="1337x">1337x (public torrents)</option>
            <option value="torrentleech">TorrentLeech (native)</option>
            <option value="myanonamouse">MyAnonaMouse (native, books)</option>
            <option value="torznab">Torznab (torrent, via Prowlarr/Jackett)</option>
          </select>
        </Labeled>

        {isMAM ? (
          <Labeled label="mam_id session" span2>
            <input className={field} style={fieldStyle} value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="paste the mam_id session string from MyAnonaMouse" autoComplete="off" required />
          </Labeled>
        ) : isTL ? (
          <>
            <Labeled label="Username">
              <input className={field} style={fieldStyle} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" required />
            </Labeled>
            <Labeled label="Password">
              <input type="password" className={field} style={fieldStyle} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
            </Labeled>
          </>
        ) : is1337 ? (
          <Labeled label="Site URL (optional mirror)" span2>
            <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://1337x.to (leave blank for default)" />
          </Labeled>
        ) : (
          <>
            <Labeled label="API URL" span2>
              <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://tracker.example/api" required />
            </Labeled>
            <Labeled label="API key">
              <input className={field} style={fieldStyle} value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="••••••••" />
            </Labeled>
          </>
        )}

        <Labeled label="Tie-break priority (1–50)" hint={TIE_BREAK_HELP}>
          <input type="number" min={1} max={50} className={field} style={fieldStyle} value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
        </Labeled>
        <Labeled label="Min seeders (0 = off)">
          <input type="number" min={0} className={field} style={fieldStyle} value={minSeeders} onChange={(e) => setMinSeeders(Math.max(0, Number(e.target.value)))} />
        </Labeled>
      </div>

      {is1337 && (
        <p className="mt-3 text-[11px] text-ink-faint">
          Public tracker — no login, no ratio. Uses FlareSolverr to get past Cloudflare and hands qBittorrent a magnet, so there’s nothing to seed. Great for testing.
        </p>
      )}
      {isTL && (
        <p className="mt-3 text-[11px] text-ink-faint">
          Native — no Prowlarr/Jackett needed. Credentials stored on your server. Uses FlareSolverr for Cloudflare. New uploads are checked every 15 minutes. 2FA not supported yet.
        </p>
      )}
      {isMAM && (
        <p className="mt-3 text-[11px] text-ink-faint">
          Native books/audiobooks integration. In MyAnonaMouse go to <b>Preferences → Security → Create session</b>, allow your server's IP (ASN-locked is fine), and paste the <span className="font-mono">mam_id</span> here. Pulls full metadata — narrator, author, series, language, format — straight from MAM's API. Scoped to Books automatically. If you already have MAM in Prowlarr, remove that entry to avoid duplicate results.
        </p>
      )}
      <TestSettings body={body} />
      {error && <div className="mt-3 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
      <button
        type="submit"
        disabled={saving}
        className="mt-3.5 rounded-lg px-4 py-2 text-[12.5px] font-semibold"
        style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: saving ? 0.6 : 1 }}
      >
        {saving ? "Saving…" : "Save indexer"}
      </button>
    </form>
  );
}

// IndexerEditor opens inside an indexer's card: what it says it supports, its tie-break,
// the "Used for" pills (saved at once) and the full form.
export function IndexerEditor({ idx, onSaved, onChanged }: { idx: Indexer; onSaved: () => void; onChanged: () => void | Promise<void> }) {
  return (
    <div className="mt-1 flex flex-col gap-1">
      {idx.kind === "newznab" && (
        // Usenet: nothing could download what it finds, so searches skip it.
        <div className="text-[11.5px] text-ink-dim">Not searched: needs a usenet download client.</div>
      )}
      {idx.caps_summary && <div className="break-words text-[11px] text-ink-faint" title="What this indexer says it supports">{idx.caps_summary}</div>}
      <div className="font-mono text-[11px] text-ink-faint" title={TIE_BREAK_HELP}>tie-break {idx.priority}</div>
      <MediaPills idx={idx} onChange={onChanged} />
      <EditForm idx={idx} onSaved={onSaved} />
    </div>
  );
}

function EditForm({ idx, onSaved }: { idx: Indexer; onSaved: () => void }) {
  const isTL = idx.kind === "torrentleech";
  const is1337 = idx.kind === "1337x";
  const isMAM = idx.kind === "myanonamouse";
  const managed = Boolean(idx.prowlarr_id);
  const [name, setName] = useState(idx.name);
  const [url, setUrl] = useState(idx.url ?? "");
  const [username, setUsername] = useState(idx.username ?? "");
  const [password, setPassword] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [priority, setPriority] = useState(idx.priority);
  const [minSeeders, setMinSeeders] = useState(idx.min_seeders ?? 0);
  const initHours = idx.seed_hours ?? 0;
  const initRatio = idx.seed_ratio ?? 0;
  const [seedEnabled, setSeedEnabled] = useState(idx.seed_enabled ?? true);
  const [seedRatio, setSeedRatio] = useState(initRatio || 0);
  const [seedTime, setSeedTime] = useState(initHours % 24 === 0 && initHours > 0 ? initHours / 24 : initHours);
  const [seedUnit, setSeedUnit] = useState<"hours" | "days">(initHours % 24 === 0 && initHours > 0 ? "days" : "hours");
  const [enabled, setEnabled] = useState(idx.enabled);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const body = (): NewIndexer => ({
    name,
    kind: idx.kind,
    url: isTL ? undefined : url,
    username: isTL ? username : undefined,
    password: isTL ? password : undefined,
    api_key: apiKey, // blank = keep existing
    categories: idx.categories,
    media_types: idx.media_types, // scoping is edited via the pills; preserve it here
    priority,
    min_seeders: minSeeders,
    seed_enabled: seedEnabled,
    seed_ratio: seedEnabled ? seedRatio : 0,
    seed_hours: seedEnabled ? (seedUnit === "days" ? seedTime * 24 : seedTime) : 0,
    enabled,
  });

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      await api.updateIndexer(idx.id, body());
      onSaved();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} aria-label={`Edit ${idx.name}`} className="mt-3 rounded-lg p-3.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      {managed && (
        <p className="mb-3 mt-0 text-[11.5px] text-ink-dim">
          Managed by Prowlarr: its name, URL and key come from Prowlarr. Change them in Prowlarr; it syncs hourly. Scope, seeding, minimum seeders, tie-break and on/off below stay yours.
        </p>
      )}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Labeled label="Name" hint={managed ? "Change this in Prowlarr; it syncs hourly" : undefined}>
          <input className={field} style={{ ...fieldStyle, ...(managed ? { opacity: 0.6, cursor: "not-allowed" } : {}) }} value={name} onChange={(e) => setName(e.target.value)} required readOnly={managed} aria-readonly={managed} />
        </Labeled>
        <Labeled label="Tie-break priority (1–50)" hint={TIE_BREAK_HELP}>
          <input type="number" min={1} max={50} className={field} style={fieldStyle} value={priority} onChange={(e) => setPriority(Number(e.target.value))} />
        </Labeled>
        <Labeled label="Min seeders (0 = off)">
          <input type="number" min={0} className={field} style={fieldStyle} value={minSeeders} onChange={(e) => setMinSeeders(Math.max(0, Number(e.target.value)))} />
        </Labeled>
        {isTL ? (
          <>
            <Labeled label="Username">
              <input className={field} style={fieldStyle} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
            </Labeled>
            <Labeled label="Password (blank = keep)">
              <input type="password" className={field} style={fieldStyle} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
            </Labeled>
          </>
        ) : is1337 ? (
          <Labeled label="Site URL (optional mirror)" span2>
            <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://1337x.to" />
          </Labeled>
        ) : isMAM ? (
          <Labeled label="mam_id session (blank = keep)" span2>
            <input className={field} style={fieldStyle} value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="paste a fresh mam_id to replace the stored session" autoComplete="off" />
          </Labeled>
        ) : managed ? null : (
          <>
            <Labeled label="API URL" span2>
              <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} required />
            </Labeled>
            <Labeled label="API key (blank = keep)">
              <input className={field} style={fieldStyle} value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="leave blank to keep current" />
            </Labeled>
          </>
        )}
      </div>

      <SeedingRules enabled={seedEnabled} ratio={seedRatio} time={seedTime} unit={seedUnit} onEnabled={setSeedEnabled} onRatio={setSeedRatio} onTime={setSeedTime} onUnit={setSeedUnit} />

      <label className="mt-3 flex items-center gap-2 text-[12.5px]">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        Enabled
      </label>
      <TestSettings body={() => ({ ...body(), id: idx.id })} />
      {error && <div className="mt-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
      <button
        type="submit"
        disabled={saving}
        className="mt-3 rounded-lg px-4 py-2 text-[12.5px] font-semibold"
        style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: saving ? 0.6 : 1 }}
      >
        {saving ? "Saving…" : "Save changes"}
      </button>
    </form>
  );
}

function ToggleSwitch({ checked, onChange, label }: { checked: boolean; onChange: () => void; label: string }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} onClick={onChange} className="relative inline-block h-[22px] w-[38px] flex-none rounded-full transition-colors" style={{ background: checked ? "var(--accent)" : "var(--line)" }}>
      <span className="absolute top-[3px] h-[16px] w-[16px] rounded-full bg-white transition-all" style={{ left: checked ? "19px" : "3px" }} />
    </button>
  );
}

function Segmented<T extends string>({ value, options, onChange }: { value: T; options: { v: T; l: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-lg p-0.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      {options.map((o) => (
        <button key={o.v} type="button" onClick={() => onChange(o.v)} className="rounded-md px-3 py-1 text-[11.5px] font-semibold transition-colors" style={{ background: value === o.v ? "var(--accent)" : "transparent", color: value === o.v ? "var(--accent-ink)" : "var(--ink-faint)" }}>
          {o.l}
        </button>
      ))}
    </div>
  );
}

function SeedingRules({ enabled, ratio, time, unit, onEnabled, onRatio, onTime, onUnit }: {
  enabled: boolean; ratio: number; time: number; unit: "hours" | "days";
  onEnabled: (v: boolean) => void; onRatio: (v: number) => void; onTime: (v: number) => void; onUnit: (v: "hours" | "days") => void;
}) {
  return (
    <div className="mt-3 rounded-lg p-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div className="flex items-center justify-between">
        <div>
          <div className="text-[12.5px] font-semibold">Seed rules</div>
          <div className="text-[10.5px] text-ink-faint">{enabled ? "Keep seeding until a ratio or time limit" : "Off — remove the download as soon as it's imported"}</div>
        </div>
        <ToggleSwitch checked={enabled} onChange={() => onEnabled(!enabled)} label="Seed rules" />
      </div>
      {enabled && (
        <>
          <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Labeled label="Seed ratio (0 = none)">
              <input type="number" min={0} step={0.1} className={field} style={fieldStyle} value={ratio} onChange={(e) => onRatio(Math.max(0, Number(e.target.value)))} placeholder="e.g. 2.5" />
            </Labeled>
            <Labeled label="Or after time (0 = none)">
              <div className="flex items-center gap-2">
                <input type="number" min={0} className={field} style={fieldStyle} value={time} onChange={(e) => onTime(Math.max(0, Number(e.target.value)))} />
                <Segmented value={unit} options={[{ v: "hours", l: "Hours" }, { v: "days", l: "Days" }]} onChange={onUnit} />
              </div>
            </Labeled>
          </div>
          <p className="mt-2 text-[10.5px] text-ink-faint">Removed when either limit is hit (both 0 = seed forever). The library keeps its own copy, so removing the torrent never touches your media.</p>
        </>
      )}
    </div>
  );
}

function Labeled({ label, hint, span2, children }: { label: string; hint?: string; span2?: boolean; children: React.ReactNode }) {
  return (
    <label className={`flex flex-col gap-1.5 ${span2 ? "sm:col-span-2" : ""}`}>
      <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">{label}</span>
      {children}
      {hint && <span className="text-[10.5px] text-ink-faint">{hint}</span>}
    </label>
  );
}

const MEDIA_TYPES = [
  { key: "movie", label: "Movies" },
  { key: "series", label: "TV" },
  { key: "book", label: "Books" },
  { key: "music", label: "Music" },
] as const;
const ALL_MEDIA = MEDIA_TYPES.map((m) => m.key as string);

// indexerBody is an indexer's saved settings as a PUT body, with what changes swapped in.
// Its key and password go back blank, which keeps the stored ones.
export function indexerBody(idx: Indexer, change: Partial<NewIndexer>): NewIndexer {
  return {
    name: idx.name,
    kind: idx.kind,
    url: idx.url,
    username: idx.username,
    categories: idx.categories,
    media_types: idx.media_types,
    priority: idx.priority,
    min_seeders: idx.min_seeders,
    seed_enabled: idx.seed_enabled,
    seed_ratio: idx.seed_ratio,
    seed_hours: idx.seed_hours,
    enabled: idx.enabled,
    ...change,
  };
}

// MediaPills scopes an indexer to specific areas. A lit pill means "searched for
// this area": an unscoped indexer (empty media_types) is used for everything, so
// every pill is lit. Clicking a lit pill excludes that area; lighting them all
// again stores [] ("all areas"). The last lit pill can't be turned off — that
// would be an indexer used for nothing, which is what disabling it is for.
function MediaPills({ idx, onChange }: { idx: Indexer; onChange: () => void | Promise<void> }) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The optimistic choice while a save is out; null = show what the server has.
  const [pending, setPending] = useState<string[] | null>(null);
  const scoped = Boolean(idx.media_types && idx.media_types.length > 0);
  const saved = scoped ? ALL_MEDIA.filter((k) => (idx.media_types as string[]).includes(k)) : ALL_MEDIA;
  const lit = pending ?? saved;
  const allLit = lit.length === ALL_MEDIA.length;

  const toggle = async (key: string) => {
    if (lit.includes(key) && lit.length === 1) {
      setError("An indexer has to search for something. Disable the indexer instead.");
      return;
    }
    const next = lit.includes(key) ? lit.filter((k) => k !== key) : [...lit, key];
    const ordered = ALL_MEDIA.filter((k) => next.includes(k));
    const media_types = ordered.length === ALL_MEDIA.length ? [] : ordered; // every area = unscoped
    setPending(ordered);
    setError(null);
    setSaving(true);
    try {
      await api.updateIndexer(idx.id, indexerBody(idx, { media_types }));
      await onChange();
    } catch (e) {
      setError(`Couldn't save: ${(e as Error).message}`);
    } finally {
      // Back to the server's answer: the refreshed row after a save, the old one after a failure.
      setPending(null);
      setSaving(false);
    }
  };

  return (
    <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
      <span className="font-mono text-[9px] font-bold uppercase tracking-[0.1em] text-ink-faint">Used for</span>
      {MEDIA_TYPES.map((m) => {
        const active = lit.includes(m.key);
        return (
          <button
            key={m.key}
            type="button"
            disabled={saving}
            aria-pressed={active}
            title={active ? `Stop using it for ${m.label}` : `Use it for ${m.label} too`}
            onClick={() => toggle(m.key)}
            className="rounded-full px-2.5 py-0.5 text-[10.5px] font-semibold transition-colors disabled:opacity-50"
            style={{
              border: `1px solid ${active ? "var(--accent)" : "var(--line)"}`,
              background: active ? "var(--accent-soft)" : "var(--panel-2)",
              color: active ? "var(--accent)" : "var(--ink-faint)",
            }}
          >
            {m.label}
          </button>
        );
      })}
      {allLit && <span className="text-[10px] text-ink-faint">· all areas</span>}
      {error && <span role="alert" className="basis-full text-[11px]" style={{ color: "var(--reject)" }}>{error}</span>}
    </div>
  );
}
