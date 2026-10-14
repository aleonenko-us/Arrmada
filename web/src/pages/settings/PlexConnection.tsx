import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { inputStyle } from "../../components/settings/ui";
import { type PlexConfig, type PlexTestResult } from "../../lib/api";
import { insightsApi } from "../../lib/insightsApi";
import { LINKS } from "../../lib/links";
import { plexApi, type PlexConnectResult } from "../../lib/plexApi";
import { usePlexPinSignIn, type PlexFlow } from "../../lib/plexSignIn";
import { useToast } from "../../ui";

// PlexConnect points Arrmada at the Plex Media Server: Sign in with Plex (finds the
// server itself), or a URL and token typed in. It opens inside the Plex card on Settings →
// Connections; the first-run wizard can reuse it as is. It reads and writes the connection
// through the API on its own, so nothing else holds its state. The token never comes back
// to the browser: a blank token field keeps the saved one.
//
// plex.tv sends a redirect-mode sign-in back to /settings/connections?plexpin=<id>, and
// the Connections page opens this editor so the PIN is picked up again here.
// onError, when given, takes a failed Plex sign-in instead of the toast (the setup wizard
// explains it in place).
export function PlexConnect({ onChanged, onError }: { onChanged?: () => void; onError?: (msg: string) => void }) {
  const toast = useToast();
  const [cfg, setCfg] = useState<PlexConfig | null>(null);
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<PlexTestResult | null>(null);
  const [, setParams] = useSearchParams();

  const load = useCallback(() => insightsApi.insightsConfig().then(setCfg).catch(() => toast("Could not load Plex settings", { tone: "error" })), [toast]);
  useEffect(() => { void load(); }, [load]);
  const [choices, setChoices] = useState<NonNullable<PlexConnectResult["choices"]>>([]);
  useEffect(() => {
    if (!cfg) return;
    setUrl(cfg.url);
  }, [cfg]);

  // Sign in with Plex: a popup on a computer, the whole page on a phone (plex.tv sends it
  // back here with ?plexpin=). Once approved the server stores the token and finds the
  // owner's server: one that answers is saved; several that answer are offered here.
  const flow = useMemo<PlexFlow<PlexConnectResult>>(() => ({
    kind: "connect",
    start: plexApi.connectStart,
    poll: async (id) => { const r = await plexApi.connectPoll(id); return r.authorized ? r : null; },
  }), []);
  const plex = usePlexPinSignIn({
    flow,
    onDone: (r) => {
      setTest(null);
      setChoices(r.choices ?? []);
      void load();
      onChanged?.();
      if (r.server_name) toast(`Signed in with Plex — connected to ${r.server_name}`, { tone: "good" });
      else if (r.choices?.length) toast("Signed in with Plex — pick your server below", { tone: "good" });
      else toast("Signed in with Plex, but none of your servers answered — enter its URL below.", { tone: "error" });
    },
    onError: (m) => (onError ? onError(m) : toast(m, { tone: "error" })),
    resumeParam: "plexpin",
    strip: () => setParams((p) => { p.delete("plexpin"); return p; }, { replace: true }),
  });
  const signingIn = plex.phase !== "idle";

  // Only the connection: monitoring and its interval are Settings → Media servers' and save on
  // their own. A first connection switches monitoring on by itself (the server does it).
  const body = (u = url) => ({ url: u.trim(), token: token.trim() || undefined });
  const save = async (u?: string) => {
    setBusy("save");
    try { setCfg(await insightsApi.updateInsightsConfig(body(u))); setToken(""); setChoices([]); onChanged?.(); toast("Plex connection saved", { tone: "good" }); }
    catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(null); }
  };
  const runTest = async () => {
    setBusy("test"); setTest(null);
    try { setTest(await insightsApi.testInsights({ url: url.trim() || undefined, token: token.trim() || undefined })); }
    catch (e) { setTest({ ok: false, error: (e as Error).message }); } finally { setBusy(null); }
  };

  return (
    <div className="mt-2 flex flex-col gap-4 border-t pt-3" style={{ borderColor: "var(--line)" }}>
      {choices.length > 1 && (
        <div role="group" aria-label="Choose your Plex server" className="flex flex-col gap-2 rounded-lg p-3" style={{ border: "1px solid var(--accent-line)", background: "var(--accent-soft)" }}>
          <div className="text-base font-semibold">More than one of your servers answered. Which one should Arrmada use?</div>
          {choices.map((c) => (
            <button key={c.machine_id} onClick={() => { setUrl(c.url); void save(c.url); }} disabled={busy !== null} className="flex flex-wrap items-baseline justify-between gap-2 rounded-lg px-3 py-2 text-left text-base" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
              <span className="font-semibold">{c.name}</span>
              <span className="break-all font-mono text-xs text-ink-faint">{c.url}</span>
            </button>
          ))}
        </div>
      )}
      <div className="flex flex-col gap-2">
        <button onClick={() => { setTest(null); plex.begin(); }} disabled={signingIn} className="flex w-full items-center justify-center gap-2 rounded-lg py-2.5 text-md font-semibold disabled:opacity-60" style={{ background: "#e5a00d", color: "#1f1200" }}>
          {plex.phase === "finishing" ? "Finishing Plex sign-in…" : signingIn ? "Waiting for Plex…" : (cfg?.token_set ? "Re-sign in with Plex" : "Sign in with Plex")}
        </button>
        {(plex.phase === "waiting" || plex.phase === "slow") && (
          <div className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 text-sm">
            {plex.phase === "slow" && <button onClick={plex.continueHere} className="font-semibold" style={{ color: "var(--accent-text)" }}>Plex window didn't open? Continue in this tab</button>}
            <button onClick={plex.cancel} className="text-ink-faint underline">Cancel</button>
          </div>
        )}
      </div>
      <div className="flex items-center gap-2 text-xs text-ink-faint">
        <span className="h-px flex-1" style={{ background: "var(--line)" }} /> or enter manually <span className="h-px flex-1" style={{ background: "var(--line)" }} />
      </div>

      <label className="block">
        <span className="mb-1 block text-sm font-semibold">Server URL</span>
        <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://192.168.1.10:32400" className="w-full rounded-lg px-3 py-2 text-md" style={inputStyle} />
      </label>
      <label className="block">
        <span className="mb-1 block text-sm font-semibold">X-Plex-Token</span>
        <input value={token} onChange={(e) => setToken(e.target.value)} type="password" autoComplete="off" placeholder={cfg?.token_set ? "•••••••••• (saved — leave blank to keep)" : "paste your token"} className="w-full rounded-lg px-3 py-2 text-md" style={inputStyle} />
        <span className="mt-1 block text-xs text-ink-faint">In Plex web: play an item → ⋯ → Get Info → View XML — the URL ends with <code>X-Plex-Token=…</code></span>
      </label>

      <div className="flex flex-wrap items-center gap-2">
        <button onClick={runTest} disabled={busy !== null || !url.trim()} className="rounded-lg px-3.5 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}>{busy === "test" ? "Testing…" : "Test connection"}</button>
        <button onClick={() => void save()} disabled={busy !== null || !url.trim()} className="rounded-lg px-3.5 py-2 text-base font-semibold disabled:opacity-50" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>{busy === "save" ? "Saving…" : "Save"}</button>
      </div>

      {test && (
        <div className="rounded-lg p-3 text-sm" style={{ border: `1px solid ${test.ok ? "var(--good)" : "var(--reject)"}`, background: test.ok ? "var(--good-soft)" : "var(--reject-soft)", color: test.ok ? "var(--good-text)" : "var(--reject-text)" }}>
          {test.ok ? (
            <div>
              <div className="font-semibold">✓ Connected{test.server_name ? ` to ${test.server_name}` : ""}{test.version ? ` · Plex ${test.version}` : ""}</div>
              {test.libraries && test.libraries.length > 0 && (
                <div className="mt-1 text-ink-dim">Libraries: {test.libraries.map((l) => l.title).join(", ")}</div>
              )}
            </div>
          ) : (
            <div className="break-words font-semibold">✕ {test.error || "Connection failed"}</div>
          )}
        </div>
      )}
      <p className="m-0 text-xs text-ink-faint">
        Watch monitoring, library updates, who may sign in with Plex and the accounts linked to it: <Link to={LINKS.plexServer} className="font-semibold" style={{ color: "var(--accent-text)" }}>Settings → Media servers →</Link>
      </p>
    </div>
  );
}
