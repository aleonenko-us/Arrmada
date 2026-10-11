import { useCallback, useEffect, useState } from "react";
import { api, type APIKeyStatus } from "../../lib/api";
import { useMe } from "../../lib/me";
import { ago } from "../../lib/taskTime";
import { useLoadedSettings } from "../../lib/useSettings";
import { useConfirm } from "../../ui";

// The metadata keys and the FlareSolverr URL, edited inside their cards on Settings →
// Connections (admin only, like the /apikeys routes). A saved value is never shown back,
// only whether it's set; a blank Save never reaches the server (clearing is Clear's job,
// confirmed); a typed value can be tested before it replaces a working one.

// What stops working when a key is cleared and nothing takes its place, for the confirm.
const KEY_CLEAR_EFFECT: Record<string, string> = {
  tmdb: "Discover, Movies and TV stop finding anything.",
  tvdb: "Anime episode numbering goes back to TMDB's.",
  omdb: "IMDb, Rotten Tomatoes and Metacritic scores stop showing.",
  hardcover: "New book lookups go back to Open Library. Books already matched keep what they have.",
  opensubtitles_api: "Subtitle search stops.",
  opensubtitles_username: "Subtitle downloads stop.",
  opensubtitles_password: "Subtitle downloads stop.",
  flaresolverr: "TorrentLeech and 1337x can't get past Cloudflare.",
};

// The OpenSubtitles username and password are checked through the API key's Test, which
// signs in with them; every other key tests itself.
export const testTarget = (id: string) => (id === "opensubtitles_username" || id === "opensubtitles_password" ? "opensubtitles_api" : id);

// The rows each card edits: OpenSubtitles' card holds its login too.
export function keyRowsFor(ref: string): string[] {
  return ref === "opensubtitles_api" ? ["opensubtitles_api", "opensubtitles_username", "opensubtitles_password"] : [ref];
}

export interface KeyTest {
  ok: boolean;
  detail: string;
  candidate?: boolean;
}

export interface KeyControl {
  keys: APIKeyStatus[] | null;
  error: string | null;
  busy: string | null;
  drafts: Record<string, string>;
  tests: Record<string, KeyTest>;
  clearErr: Record<string, string>;
  setDraft: (id: string, v: string) => void;
  save: (id: string) => Promise<void>;
  test: (id: string, candidate?: string) => Promise<void>;
  clear: (k: APIKeyStatus) => Promise<void>;
}

// useAPIKeys loads the key statuses (when enabled: admins only) and runs Save, Test and
// Clear. onChanged runs after anything that changes what the cards show.
export function useAPIKeys(enabled: boolean, onChanged: () => void): KeyControl {
  const { setMetadataReady } = useMe();
  const confirm = useConfirm();
  const [keys, setKeys] = useState<APIKeyStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [tests, setTests] = useState<Record<string, KeyTest>>({});
  const [clearErr, setClearErr] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!enabled) return;
    api.apiKeys().then(setKeys).catch((e: Error) => setError(e.message));
  }, [enabled]);

  const drop = <T,>(m: Record<string, T>, id: string) => { const n = { ...m }; delete n[id]; return n; };

  const test = useCallback(async (id: string, candidate?: string) => {
    setBusy("test:" + id);
    setTests((t) => drop(t, id));
    try {
      const r = await api.testAPIKey(id, candidate);
      setTests((t) => ({ ...t, [id]: { ...r, candidate: !!candidate } }));
    } catch (e) {
      setTests((t) => ({ ...t, [id]: { ok: false, detail: (e as Error).message, candidate: !!candidate } }));
    } finally {
      setBusy(null);
    }
    // A test of the saved key is recorded on its card.
    if (!candidate) onChanged();
  }, [onChanged]);

  const save = async (id: string) => {
    // A blank Save never reaches the server: clearing a key is Clear's job.
    const value = drafts[id]?.trim();
    if (!value) return;
    setBusy(id); setError(null);
    try {
      const next = await api.setAPIKey(id, value);
      setKeys(next);
      // The key works the moment it's saved, so let Movies and Discover know without a reload.
      if (id === "tmdb") setMetadataReady(!!next.find((k) => k.id === "tmdb")?.configured);
      setDrafts((d) => drop(d, id));
      onChanged();
      // Test straight after saving, so a key that saved but doesn't work says so now.
      const target = next.find((k) => k.id === testTarget(id));
      if (target?.testable && target.configured) await test(target.id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  };

  // Clear goes straight to the DELETE, not through the draft, so a value typed in the
  // field is never saved by pressing Clear.
  const clear = async (k: APIKeyStatus) => {
    const ok = await confirm({
      title: `Clear the saved ${k.label}?`,
      body: k.env_set
        ? `The value from your install${k.env_hint ? ` (${k.env_hint})` : ""} will be used instead.`
        : KEY_CLEAR_EFFECT[k.id] ?? k.purpose,
      confirmLabel: "Clear",
      tone: "danger",
    });
    if (!ok) return;
    setBusy("clear:" + k.id);
    setClearErr((c) => drop(c, k.id));
    try {
      const next = await api.clearAPIKey(k.id);
      setKeys(next);
      if (k.id === "tmdb") setMetadataReady(!!next.find((x) => x.id === "tmdb")?.configured);
      setDrafts((d) => drop(d, k.id));
      setTests((t) => drop(t, k.id));
      onChanged();
    } catch (e) {
      setClearErr((c) => ({ ...c, [k.id]: `Couldn't clear it: ${(e as Error).message}` }));
    } finally {
      setBusy(null);
    }
  };

  return {
    keys, error, busy, drafts, tests, clearErr,
    setDraft: (id, v) => setDrafts((d) => ({ ...d, [id]: v })),
    save, test, clear,
  };
}

// KeyEditor is the rows a card edits (keyRowsFor), opened inside the card.
export function KeyEditor({ ids, ctl }: { ids: string[]; ctl: KeyControl }) {
  if (ctl.keys === null) {
    return ctl.error
      ? <p className="m-0 text-[11.5px]" style={{ color: "var(--reject)" }}>{ctl.error}</p>
      : <p className="m-0 text-[11.5px] text-ink-dim">Loading…</p>;
  }
  const rows = ids.map((id) => ctl.keys?.find((k) => k.id === id)).filter((k): k is APIKeyStatus => !!k);
  return (
    <div className="mt-1 flex flex-col gap-2.5">
      {ctl.error && <div className="rounded-lg p-2.5 text-[11.5px]" style={{ border: "1px solid var(--reject)", color: "var(--reject)" }}>{ctl.error}</div>}
      {rows.map((k) => <KeyRow key={k.id} k={k} ctl={ctl} />)}
    </div>
  );
}

function KeyRow({ k, ctl }: { k: APIKeyStatus; ctl: KeyControl }) {
  const draft = ctl.drafts[k.id] ?? "";
  // A typed value is tested in place of the saved one where the provider allows it, so a
  // wrong key shows before it replaces a working one.
  const typed = k.tests_candidate ? draft.trim() : "";
  const result = ctl.tests[k.id];
  return (
    <div className="flex flex-col gap-1.5 rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-[12px] font-semibold">{k.label}</span>
        {k.configured ? (
          <span className="rounded-full px-2 py-0.5 text-[10px] font-semibold" style={{ background: "var(--good-soft, rgba(80,200,120,.15))", color: "var(--good)" }}>
            Set {k.hint ? `(${k.hint})` : ""}{k.source === "env" ? " · from install" : ""}
          </span>
        ) : (
          <span className="rounded-full px-2 py-0.5 text-[10px] font-semibold" style={{ background: "var(--panel)", color: "var(--ink-faint)" }}>Not set</span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type={k.secret ? "password" : "text"}
          aria-label={k.label}
          value={draft}
          onChange={(e) => ctl.setDraft(k.id, e.target.value)}
          placeholder={k.configured ? "Enter a new value to replace it" : k.secret ? "Paste your key here" : "Type it here"}
          autoComplete="off"
          className="min-w-0 flex-1 basis-[180px] rounded-lg px-3 py-1.5 text-[12px]"
          style={{ background: "var(--panel)", border: "1px solid var(--line)", color: "var(--ink)" }}
        />
        <button
          onClick={() => void ctl.save(k.id)}
          disabled={ctl.busy !== null || !draft.trim()}
          className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold disabled:opacity-50"
          style={{ border: "1px solid var(--accent-line, var(--line))", color: "var(--accent)" }}
        >
          {ctl.busy === k.id ? "Saving…" : "Save"}
        </button>
        {k.testable && (k.configured || typed) && (
          <button
            onClick={() => void ctl.test(k.id, typed || undefined)}
            disabled={ctl.busy !== null}
            className="flex-none rounded-lg px-2.5 py-1.5 text-[11.5px] font-semibold"
            style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
            title={typed ? "Make a real request with the value you typed, without saving it" : "Make a real request with the saved value and show what came back"}
          >
            {ctl.busy === "test:" + k.id ? "Testing…" : typed ? "Test typed" : "Test"}
          </button>
        )}
        {k.configured && k.source === "settings" && (
          <button
            onClick={() => void ctl.clear(k)}
            disabled={ctl.busy !== null}
            className="flex-none rounded-lg px-2.5 py-1.5 text-[11.5px] font-semibold"
            style={{ border: "1px solid var(--line)", color: "var(--ink-faint)" }}
            title="Clear the saved value"
          >
            {ctl.busy === "clear:" + k.id ? "Clearing…" : "Clear"}
          </button>
        )}
      </div>
      {ctl.clearErr[k.id] && <p className="m-0 text-[11px]" style={{ color: "var(--reject)" }}>{ctl.clearErr[k.id]}</p>}
      {k.last_error && (
        <p className="m-0 text-[11px]" style={{ color: "var(--reject)" }} title={k.last_error_at ? new Date(k.last_error_at).toLocaleString() : undefined}>
          Last failed {ago(k.last_error_at)}: {k.last_error}
        </p>
      )}
      {result && (
        <p className="m-0 break-words text-[11px]" style={{ color: result.ok ? "var(--good)" : "var(--reject)" }}>
          {result.ok ? "✓ " : "✗ "}{result.detail}
          {result.candidate && <span className="text-ink-faint"> (typed value, not saved)</span>}
        </p>
      )}
      <p className="m-0 text-[10px] text-ink-faint">
        {k.steps} {k.help_url && <a href={k.help_url} target="_blank" rel="noreferrer" style={{ color: "var(--accent)" }}>Get one →</a>}
      </p>
    </div>
  );
}

// DiscoveryRegion tunes what the TMDB key returns, so it sits with the metadata keys.
export function DiscoveryRegion() {
  const { syncRegion } = useLoadedSettings();
  const [region, setRegion] = useState("");
  const [saved, setSaved] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    api.settings().then((s) => { setRegion(s.tmdb_region ?? ""); setSaved(s.tmdb_region ?? ""); }).catch(() => {});
  }, []);

  const save = async () => {
    setBusy(true); setMsg(null);
    try {
      const next = await api.updateSettings({ tmdb_region: region.trim().toUpperCase() });
      setRegion(next.tmdb_region);
      setSaved(next.tmdb_region);
      syncRegion(next.tmdb_region);
      setMsg({ ok: true, text: region.trim() ? `Discover now favours ${region.trim().toUpperCase()} listings` : "Back to global listings" });
    } catch (e) {
      setMsg({ ok: false, text: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div id="discovery-region" className="flex scroll-mt-20 flex-col gap-1.5 rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-[13px] font-semibold">Discovery region</span>
        <span className="rounded-full px-2 py-0.5 text-[10px] font-semibold" style={saved ? { background: "var(--good-soft, rgba(80,200,120,.15))", color: "var(--good)" } : { background: "var(--panel-2)", color: "var(--ink-faint)" }}>
          {saved || "Global"}
        </span>
      </div>
      <p className="m-0 text-[11px] text-ink-faint">
        Localizes Discover's popular, upcoming and genre lists (release dates, theatrical calendar). Two-letter
        country code — e.g. AU, US, GB. Leave empty for TMDB's global lists.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="text"
          aria-label="Discovery region"
          value={region}
          maxLength={2}
          onChange={(e) => setRegion(e.target.value.toUpperCase().replace(/[^A-Z]/g, ""))}
          placeholder="AU"
          className="w-[80px] rounded-lg px-3 py-1.5 text-center font-mono text-[12px] uppercase"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
        />
        <button
          onClick={save}
          disabled={busy || region === (saved ?? "")}
          className="flex-none rounded-lg px-3 py-1.5 text-[11.5px] font-semibold disabled:opacity-50"
          style={{ border: "1px solid var(--accent-line, var(--line))", color: "var(--accent)" }}
        >
          {busy ? "Saving…" : "Save"}
        </button>
        {msg && <span className="text-[10.5px]" style={{ color: msg.ok ? "var(--good)" : "var(--reject)" }}>{msg.text}</span>}
      </div>
    </div>
  );
}
