import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, type APIKeyStatus, type LibraryPaths, type SetupState } from "../lib/api";
import { FolderChips, FolderPicker } from "./Library";
import { restartAndWait } from "../lib/restart";
import { FleetMark } from "../components/FleetMark";
import { ChecklistRows, useSetupChecklist } from "../components/GettingStarted";
import { ProwlarrPanel } from "../components/connections/ProwlarrPanel";
import { useMe } from "../lib/me";
import { setupApi } from "../lib/setupApi";
import { Button, useConfirm, useToast } from "../ui";
import { PlexConnect } from "./settings/PlexConnection";

// SetupWizard is the first thing an admin sees on a fresh install: the metadata key, then
// each library folder (pre-filled from what's on the mount), then Plex (Settings →
// Connections' own sign-in, plus the family switches), then indexers (a Prowlarr sync, or
// one tracker tested before it's saved — Connections' own dialog), then the Getting
// started checklist with a link to each step that's left. Folders apply live, so the restart only
// appears if a startup-only setting is waiting. Only the first step can be skipped
// outright; Settings → System → Run setup again opens it again with what's set filled in.

type Step = "keys" | "folders" | "plex" | "indexers" | "finish";

const STEPS: { id: Step; label: string }[] = [
  { id: "keys", label: "Metadata" },
  { id: "folders", label: "Folders" },
  { id: "plex", label: "Plex" },
  { id: "indexers", label: "Indexers" },
  { id: "finish", label: "Finish" },
];

// The Indexers step reuses Settings → Connections' add-indexer dialog (presets, Test
// before saving); it loads when opened.
const AddIndexerModal = lazy(() => import("../components/connections/AddIndexerModal"));

// The family sign-in choices on the Plex step: off unless the owner turns them on here.
interface Family { enabled: boolean; types: AutoTypes }
type AutoTypes = { movie: boolean; series: boolean; book: boolean };
const TYPE_NAMES: [keyof AutoTypes, string][] = [["movie", "Movies"], ["series", "Series"], ["book", "Books"]];
const typesFrom = (csv: string): AutoTypes => {
  const on = csv.split(",").map((t) => t.trim());
  return { movie: on.includes("movie"), series: on.includes("series"), book: on.includes("book") };
};
const typesCSV = (t: AutoTypes) => TYPE_NAMES.filter(([k]) => t[k]).map(([k]) => k).join(",");

const FOLDERS: { key: keyof LibraryPaths; label: string; hint: string; optional?: boolean }[] = [
  { key: "movies", label: "Movies", hint: "Where movie folders live" },
  { key: "tv", label: "TV shows", hint: "Where show folders live" },
  { key: "downloads", label: "Downloads", hint: "Where qBittorrent saves downloads" },
  { key: "ebooks", label: "Ebooks", hint: "Optional — only if you use Books", optional: true },
  { key: "audiobooks", label: "Audiobooks", hint: "Optional — can be the same as Ebooks", optional: true },
  { key: "music", label: "Music", hint: "Optional — only if you use Music", optional: true },
];

const OPTIONAL_KEYS = ["hardcover", "omdb", "tvdb"];

// The answer to a pre-save key check, for the value it was asked about.
interface KeyCheck { value: string; ok: boolean; detail: string; busy?: boolean }

/**
 * onDone: setup is finished (or skipped). to: a page the admin picked from the last
 * step's checklist, to open next.
 * rerun: opened from Settings → System → Run setup again, not on a fresh install.
 */
export function SetupWizard({ onDone, rerun = false }: { onDone: (to?: string) => void; rerun?: boolean }) {
  const [state, setState] = useState<SetupState | null>(null);
  // Back from plex.tv after a Plex sign-in by redirect (a phone, or popups blocked): the
  // address carries ?plexpin=, and the Plex step picks the sign-in up again.
  const [step, setStep] = useState<Step>(() => (hasPlexPin() ? "plex" : "keys"));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [keys, setKeys] = useState<Record<string, string>>({});
  const [folders, setFolders] = useState<LibraryPaths | null>(null);
  const [picking, setPicking] = useState<keyof LibraryPaths | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [blocking, setBlocking] = useState<Partial<Record<keyof LibraryPaths, boolean>>>({});
  const onBlocking = useCallback((k: keyof LibraryPaths, b: boolean) => setBlocking((cur) => (cur[k] === b ? cur : { ...cur, [k]: b })), []);
  const folderBlocked = Object.values(blocking).some(Boolean);
  // The last step's list, read only once it's on screen (and kept fresh while it is).
  const checklist = useSetupChecklist(step === "finish");

  const [family, setFamily] = useState<Family | null>(null);
  const [plexError, setPlexError] = useState<string | null>(null);
  const { booksEnabled } = useMe();

  const load = () => api.setupState().then((s) => {
    setState(s);
    // Pre-fill each folder: what's already chosen, else what the mount looks like it holds.
    setFolders((cur) => cur ?? Object.fromEntries(FOLDERS.map((f) => [
      f.key, s.libraries_chosen ? s.library[f.key] : (s.suggestions[f.key] ?? s.library[f.key] ?? ""),
    ])) as unknown as LibraryPaths);
    setFamily((cur) => cur ?? { enabled: !!s.plex_family?.enabled, types: typesFrom(s.plex_family?.auto_approve_types ?? "") });
  }).catch((e) => setError((e as Error).message));
  useEffect(() => { load(); }, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setError(null);
    try { await fn(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };

  const go = (s: Step) => { setError(null); setStep(s); };
  const finish = (to?: string) => run(async () => { await api.completeSetup(); onDone(to); });

  // The TMDB key is checked before it's saved: on paste or when the field loses focus, the
  // typed value goes to TMDB once (never stored) and the answer shows under the field.
  const confirm = useConfirm();
  const [tmdbCheck, setTmdbCheck] = useState<KeyCheck | null>(null);
  // The latest check, kept as a promise so a blur and a click on Next for the same value
  // (the click blurs the field first) share one request.
  const lastCheck = useRef<{ value: string; result: Promise<KeyCheck> } | null>(null);
  const checkTmdb = (value: string): Promise<KeyCheck | null> => {
    const v = value.trim();
    if (!v) { lastCheck.current = null; setTmdbCheck(null); return Promise.resolve(null); }
    if (lastCheck.current?.value === v) return lastCheck.current.result;
    setTmdbCheck({ value: v, busy: true, ok: false, detail: "" });
    const result = api.testAPIKey("tmdb", v).then(
      (r): KeyCheck => ({ value: v, ok: r.ok, detail: r.detail }),
      (e: Error): KeyCheck => {
        // Couldn't ask (Arrmada unreachable, say): that says nothing about the key, so
        // the next blur or Next asks again rather than reusing this answer.
        if (lastCheck.current?.value === v) lastCheck.current = null;
        return { value: v, ok: false, detail: e.message };
      },
    );
    const mine = { value: v, result };
    lastCheck.current = mine;
    // null: this check failed to ask and cleared itself; still show why.
    result.then((res) => { if (lastCheck.current === mine || lastCheck.current === null) setTmdbCheck(res); });
    return result;
  };

  const saveKeys = () => run(async () => {
    // A TMDB key that failed its check is only saved if the admin says so.
    const tmdbValue = keys.tmdb?.trim();
    if (tmdbValue) {
      const r = await checkTmdb(tmdbValue);
      if (r && !r.ok && !(await confirm({
        title: "Save this TMDB key anyway?",
        body: <>TMDB didn't accept it: {r.detail}. Movies and TV won't find anything until it works.</>,
        confirmLabel: "Save anyway",
      }))) return;
    }
    for (const [id, v] of Object.entries(keys)) {
      if (v.trim()) await api.setAPIKey(id, v.trim());
    }
    await load();
    go("folders");
  });

  const saveFolders = () => run(async () => {
    if (folders) await api.setLibraryPaths(folders);
    await load();
    go("plex");
  });

  // The family switches are saved as chosen here, both of them, so the wizard never leaves
  // a stored default deciding who gets in or what downloads unasked.
  const savePlex = () => run(async () => {
    if (family) {
      await api.updateSettings({ plex_login_enabled: family.enabled, plex_login_auto_approve_types: typesCSV(family.types) });
    }
    go("indexers");
  });

  // The Indexers step asks whether a Prowlarr answers (which can take a few seconds) only
  // when it opens; null while it's asking. showProwlarr: the sync panel is open.
  const toast = useToast();
  const [prowlarr, setProwlarr] = useState<boolean | null>(null);
  const [showProwlarr, setShowProwlarr] = useState(false);
  const [addingIndexer, setAddingIndexer] = useState(false);
  const refreshIndexers = useCallback(() => setupApi.stateWithProwlarr().then((s) => {
    setState(s);
    setProwlarr(!!s.prowlarr_available);
    if (s.prowlarr_available) setShowProwlarr(true);
  }).catch(() => setProwlarr(false)), []);
  useEffect(() => {
    if (step === "indexers") void refreshIndexers();
  }, [step, refreshIndexers]);

  // Restart and wait for the NEW process (not the old one's last answers), then finish.
  const restart = () => run(async () => {
    setRestarting(true);
    // Finishes setup even if it times out: the reload then shows whatever state it's in.
    await restartAndWait(() => api.restartApp());
    await api.completeSetup();
    window.location.reload();
  });

  if (!state || !folders) {
    return <Frame><p className="text-md text-ink-dim">{error ?? "Loading…"}</p></Frame>;
  }

  const keyStatus = (id: string): APIKeyStatus | undefined => state.keys.find((k) => k.id === id);
  const tmdb = keyStatus("tmdb");
  const managedOnly = state.mounts.length > 0 && !state.mounts.includes("/storage");
  const stepIndex = STEPS.findIndex((s) => s.id === step);
  const restartFirst = state.restart_needed && state.can_restart;

  return (
    <Frame>
      <ol aria-label="Setup steps" className="m-0 mb-5 flex list-none flex-wrap items-center gap-x-1.5 gap-y-1 p-0 text-xs font-semibold uppercase tracking-wide text-ink-faint">
        {STEPS.map((s, i) => (
          <li key={s.id} aria-current={s.id === step ? "step" : undefined} style={{ color: s.id === step ? "var(--accent-text)" : undefined }}>
            {i > 0 && <span aria-hidden className="mr-1.5 text-ink-faint">›</span>}
            {i + 1}. {s.label}
          </li>
        ))}
      </ol>

      {error && <div role="alert" className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}

      {step === "keys" && (
        <>
          <h1 className="m-0 text-2xl font-bold">{rerun ? "Run setup again" : "Welcome to Arrmada"}</h1>
          <p className="mb-4 mt-1 text-md text-ink-dim">
            {rerun ? "What's already set is filled in; change anything, or carry on to the next step." : "A few quick steps and you're ready. Everything here can be changed later in Settings."}
          </p>

          <KeyField
            status={tmdb}
            label="TMDB API key"
            note="Needed for Movies and TV — titles, artwork and Discover. It's free."
            value={keys.tmdb ?? ""}
            onChange={(v, pasted) => { setKeys({ ...keys, tmdb: v }); if (pasted) checkTmdb(v); }}
            onBlur={(v) => { if (v.trim()) checkTmdb(v); }}
          />
          {tmdbCheck && tmdbCheck.value === (keys.tmdb ?? "").trim() && (
            <p className="mt-1 text-sm" style={{ color: tmdbCheck.busy ? "var(--ink-faint)" : tmdbCheck.ok ? "var(--good-text)" : "var(--reject-text)" }}>
              {tmdbCheck.busy ? "Checking with TMDB…" : `${tmdbCheck.ok ? "✓" : "✗"} ${tmdbCheck.detail}`}
            </p>
          )}
          <details className="mt-3">
            <summary className="cursor-pointer text-sm font-semibold text-ink-dim">More keys (all optional)</summary>
            <div className="mt-2 flex flex-col gap-3">
              {OPTIONAL_KEYS.map((id) => {
                const s = keyStatus(id);
                return s ? <KeyField key={id} status={s} label={s.label} note={s.purpose} value={keys[id] ?? ""} onChange={(v) => setKeys({ ...keys, [id]: v })} /> : null;
              })}
            </div>
          </details>
          <Nav
            busy={busy}
            skip={{
              label: rerun ? "Back to Settings → System" : "Skip for now — you can run it again from Settings → System",
              onClick: () => (rerun ? onDone() : finish()),
            }}
            onNext={saveKeys}
            nextLabel={Object.values(keys).some((v) => v.trim()) ? "Save and continue" : "Continue"}
          />
        </>
      )}

      {step === "folders" && (
        <>
          <h1 className="m-0 text-2xl font-bold">Where's your media?</h1>
          <p className="mb-1 mt-1 text-md text-ink-dim">
            Pick a folder for each library. {state.libraries_chosen ? "These are your current folders." : "We've filled in anything that looked right — check it."}
          </p>
          <p className="mb-4 text-sm text-ink-faint">
            Keep Downloads on the same drive or share as your libraries, so finished downloads are hardlinked instantly instead of copied.
          </p>
          {managedOnly && (
            <div className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--avoid)", color: "var(--avoid-text)" }}>
              Arrmada is using its own managed storage — no media folder was given at install. To use your existing library, re-run the installer with your media folder.
            </div>
          )}
          <div className="flex flex-col gap-2.5">
            {FOLDERS.map((f) => (
              <div key={f.key}>
                <div className="mb-1 flex items-baseline justify-between gap-2">
                  <label htmlFor={`setup-folder-${f.key}`} className="text-base font-semibold">{f.label}</label>
                  <span className="text-xs text-ink-faint">{f.hint}</span>
                </div>
                <div className="flex gap-2">
                  <input
                    id={`setup-folder-${f.key}`}
                    value={folders[f.key]}
                    onChange={(e) => setFolders({ ...folders, [f.key]: e.target.value })}
                    placeholder={f.optional ? "Leave as is if you don't use it" : "/storage/…"}
                    className="min-w-0 flex-1 rounded-lg px-3 py-2 font-mono text-sm"
                    style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
                  />
                  <Button onClick={() => setPicking(f.key)} className="flex-none" aria-label={`Browse for the ${f.label} folder`}>Browse</Button>
                </div>
                <FolderChips
                  kind={f.key}
                  path={folders[f.key]}
                  current={state.library[f.key] ?? ""}
                  downloads={folders.downloads}
                  onBlocking={onBlocking}
                  onCreated={(saved) => setFolders((cur) => (cur ? { ...cur, [f.key]: saved[f.key] } : cur))}
                />
              </div>
            ))}
          </div>
          <Nav busy={busy} blocked={folderBlocked} onBack={() => go("keys")} onNext={saveFolders} nextLabel="Save and continue" />
          {picking && (
            <FolderPicker
              initial={folders[picking] || state.mounts[0]}
              onClose={() => setPicking(null)}
              onSelect={(p) => { setFolders({ ...folders, [picking]: p }); setPicking(null); }}
            />
          )}
        </>
      )}

      {step === "plex" && family && (
        <>
          <h1 className="m-0 text-2xl font-bold">Connect Plex</h1>
          <p className="mb-3 mt-1 text-md text-ink-dim">
            Family sign-in, watch stats and pausing conversions while someone's watching all need your Plex server. Sign in with Plex and Arrmada finds your own server — never one someone else shared with you.
          </p>
          {state.plex_connected && (
            <p role="status" className="m-0 mb-1 text-base font-semibold" style={{ color: "var(--good-text)" }}>
              ✓ Connected{state.plex_server ? ` to ${state.plex_server}` : ""}
            </p>
          )}
          {plexError && (
            <div role="alert" className="mb-1 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--avoid)", color: "var(--avoid-text)" }}>
              Couldn't reach plex.tv from the server — you can connect later in Settings → Connections, or enter the server's address below.
              <span className="mt-1 block break-words text-ink-faint">{plexError}</span>
            </div>
          )}
          <PlexConnect onChanged={() => { setPlexError(null); void load(); }} onError={setPlexError} />

          <fieldset className="m-0 mt-5 flex flex-col gap-2.5 rounded-xl p-3.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
            <legend className="sr-only">Family sign-in</legend>
            <div className="flex items-start gap-2.5 text-base">
              <input id="setup-plex-family" type="checkbox" className="mt-0.5" aria-describedby="setup-plex-family-hint" checked={family.enabled} onChange={(e) => setFamily({ ...family, enabled: e.target.checked })} />
              <div>
                <label htmlFor="setup-plex-family" className="block font-semibold">Let family sign in with Plex</label>
                <span id="setup-plex-family-hint" className="block text-sm text-ink-faint">People with access to your Plex server get a requester account the first time they sign in — no accounts to hand out.</span>
              </div>
            </div>
            {family.enabled && (
              <div role="group" aria-label="Auto-approve their requests" className="ml-6 flex flex-col gap-1.5">
                <span className="text-base font-semibold">Auto-approve their requests</span>
                <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-sm text-ink-dim">
                  {TYPE_NAMES.filter(([k]) => k !== "book" || booksEnabled).map(([k, name]) => (
                    <label key={k} className="flex min-h-[28px] items-center gap-1.5">
                      <input type="checkbox" checked={family.types[k]} onChange={(e) => setFamily({ ...family, types: { ...family.types, [k]: e.target.checked } })} />
                      {name}
                    </label>
                  ))}
                </div>
                <span className="text-xs text-ink-faint">Ticked types download straight away; the rest wait for you to approve. A series request can pull every season of a long show.</span>
              </div>
            )}
          </fieldset>
          <Nav
            busy={busy}
            onBack={() => go("folders")}
            skip={{ label: "Skip this step", onClick: () => go("indexers") }}
            onNext={savePlex}
            nextLabel="Save and continue"
          />
        </>
      )}

      {step === "indexers" && (
        <>
          <h1 className="m-0 text-2xl font-bold">Where releases come from</h1>
          <p className="mb-3 mt-1 text-md text-ink-dim">
            Arrmada searches indexers — your trackers — for releases. Sync them all from Prowlarr in one go, or add one tracker now and test it before it's saved. You can add more any time in Settings → Connections.
          </p>
          {(state.indexers_enabled ?? 0) > 0 && (
            <p role="status" className="m-0 mb-3 text-base font-semibold" style={{ color: "var(--good-text)" }}>
              ✓ {state.indexers_enabled} {state.indexers_enabled === 1 ? "indexer" : "indexers"} enabled
            </p>
          )}

          <div className="flex flex-col gap-3">
            {prowlarr === null ? (
              <p className="m-0 text-sm text-ink-faint">Looking for Prowlarr…</p>
            ) : showProwlarr ? (
              <div>
                <div className="text-base font-semibold">{prowlarr ? "Prowlarr is running — sync its indexers" : "Sync from Prowlarr"}</div>
                <ProwlarrPanel onSynced={() => void refreshIndexers()} />
              </div>
            ) : (
              <p className="m-0 text-sm text-ink-dim">
                No Prowlarr answered at its saved address.{" "}
                <button type="button" onClick={() => setShowProwlarr(true)} className="font-semibold" style={{ color: "var(--accent-text)" }}>Sync from a Prowlarr elsewhere</button>
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <Button onClick={() => setAddingIndexer(true)}>{(state.indexers_enabled ?? 0) > 0 ? "Add another tracker…" : "Add a tracker…"}</Button>
              <span className="text-sm text-ink-faint">Pick it from the list, fill in its key, and Test it first.</span>
            </div>
          </div>
          {addingIndexer && (
            <Suspense fallback={null}>
              <AddIndexerModal
                onClose={() => setAddingIndexer(false)}
                onAdded={(created) => { setAddingIndexer(false); toast(`Added ${created.name}`, { tone: "good" }); void refreshIndexers(); }}
                onProwlarr={() => { setAddingIndexer(false); setShowProwlarr(true); }}
              />
            </Suspense>
          )}
          <Nav
            busy={busy}
            onBack={() => go("plex")}
            skip={(state.indexers_enabled ?? 0) > 0 ? undefined : { label: "Skip this step", onClick: () => go("finish") }}
            onNext={() => go("finish")}
            nextLabel="Continue"
          />
        </>
      )}

      {step === "finish" && (
        <>
          <h1 className="m-0 text-2xl font-bold">{state.restart_needed ? "One restart to go" : "Here's where you are"}</h1>
          {state.restart_needed ? (
            <p className="mt-1 text-md text-ink-dim">
              Arrmada needs to restart to start using the new folders for downloads and imports.
              {state.can_restart ? " It takes a few seconds." : " Restart the Arrmada container the way you started it (for example docker compose restart arrmada-app)."}
            </p>
          ) : (
            <p className="mt-1 text-md text-ink-dim">
              Each step ticks itself off once it's done; pick one to go there now. The Dashboard keeps this list until everything's ticked.
            </p>
          )}
          <div className="mt-4 rounded-xl p-2.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
            {checklist.data ? (
              // A restart has to come first, so the steps aren't links until it's done.
              <ChecklistRows items={checklist.data.items} onOpen={restartFirst ? undefined : (to) => finish(to)} />
            ) : (
              <p className="m-0 px-2 py-1.5 text-sm text-ink-faint">{checklist.error ? "Couldn't read the checklist — the Dashboard shows it too." : "Checking…"}</p>
            )}
          </div>
          <div className="mt-5 flex flex-wrap items-center justify-between gap-2">
            <Button variant="ghost" onClick={() => go(STEPS[stepIndex - 1].id)} disabled={busy}>Back</Button>
            {restartFirst ? (
              <Button variant="primary" onClick={restart} busy={busy} busyLabel={restarting ? "Restarting…" : "Saving…"}>Restart and finish</Button>
            ) : (
              <Button variant="primary" onClick={() => finish()} busy={busy} busyLabel="Saving…">{rerun ? "Done" : "Open Arrmada"}</Button>
            )}
          </div>
        </>
      )}
    </Frame>
  );
}

function hasPlexPin(): boolean {
  try {
    return new URLSearchParams(window.location.search).has("plexpin");
  } catch {
    return false;
  }
}

// SetupAgain is Settings → System → Run setup again: the wizard with what's set filled
// in. Finishing goes back to Settings → System, or to the step picked on the last page.
export function SetupAgain() {
  const navigate = useNavigate();
  return <SetupWizard rerun onDone={(to) => navigate(to ?? "/settings/system")} />;
}

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-full items-start justify-center overflow-y-auto px-4 py-10 font-sans">
      <div className="w-full max-w-[600px]">
        <div className="mb-6 flex items-center gap-2.5">
          <span className="grid h-[30px] w-[30px] place-items-center rounded-lg" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
            <FleetMark className="h-[17px] w-[17px]" />
          </span>
          <span className="text-lg font-extrabold tracking-[0.12em]">ARRMADA</span>
        </div>
        <div className="rounded-2xl p-6" style={{ background: "var(--panel)", border: "1px solid var(--line)", boxShadow: "var(--shadow)" }}>{children}</div>
      </div>
    </div>
  );
}

function KeyField({ status, label, note, value, onChange, onBlur }: {
  status?: APIKeyStatus; label: string; note: string; value: string;
  /** pasted: the change came from a paste, which is when a key is worth checking at once. */
  onChange: (v: string, pasted: boolean) => void;
  onBlur?: (v: string) => void;
}) {
  const pasting = useRef(false);
  return (
    <label className="block">
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="text-base font-semibold">{label}</span>
        {status?.configured && <span className="text-xs" style={{ color: "var(--good-text)" }}>✓ saved{status.hint ? ` (…${status.hint})` : ""}</span>}
      </div>
      <input
        type={status?.secret === false ? "text" : "password"}
        value={value}
        onPaste={() => { pasting.current = true; }}
        onChange={(e) => { const p = pasting.current; pasting.current = false; onChange(e.target.value, p); }}
        onBlur={onBlur ? (e) => onBlur(e.target.value) : undefined}
        placeholder={status?.configured ? "Leave blank to keep the saved key" : "Paste the key"}
        autoComplete="off"
        className="block w-full rounded-lg px-3 py-2 font-mono text-sm"
        style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
      />
      <span className="mt-1 block text-xs text-ink-faint">
        {note}{status?.help_url && <> · <a href={status.help_url} target="_blank" rel="noreferrer" className="underline" style={{ color: "var(--accent-text)" }}>get one</a></>}
      </span>
    </label>
  );
}

// Nav is a step's buttons. skip: the first step's "Skip for now" (the whole wizard), or a
// later optional step's "Skip this step". Only those two kinds exist.
function Nav({ busy, blocked, onBack, skip, onNext, nextLabel }: {
  busy: boolean;
  blocked?: boolean;
  onBack?: () => void;
  skip?: { label: string; onClick: () => void };
  onNext: () => void;
  nextLabel: string;
}) {
  return (
    <div className="mt-6 flex flex-wrap items-center justify-between gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {onBack && <Button variant="ghost" onClick={onBack} disabled={busy}>Back</Button>}
        {skip && (
          <button type="button" onClick={skip.onClick} disabled={busy} className="text-left text-sm text-ink-faint hover:text-[var(--ink)]">{skip.label}</button>
        )}
      </div>
      <Button variant="primary" onClick={onNext} disabled={blocked} busy={busy} busyLabel="Saving…" title={blocked ? "Fix the folders marked in red first" : undefined}>{nextLabel}</Button>
    </div>
  );
}
