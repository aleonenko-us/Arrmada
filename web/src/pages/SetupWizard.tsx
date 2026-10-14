import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { api, type APIKeyStatus, type LibraryPaths, type SetupState } from "../lib/api";
import { FolderChips, FolderPicker } from "./Library";
import { restartAndWait } from "../lib/restart";
import { FleetMark } from "../components/FleetMark";
import { ChecklistRows, useSetupChecklist } from "../components/GettingStarted";
import { ProwlarrPanel } from "../components/connections/ProwlarrPanel";
import { useMe } from "../lib/me";
import { setupApi } from "../lib/setupApi";
import { Button, StatusChip, useConfirm, useToast } from "../ui";
import { PlexConnect } from "./settings/PlexConnection";

// SetupWizard is the first thing an admin sees on a fresh install: the TMDB key and the
// optional ones (Hardcover recommended), each checked before it's saved, then each library
// folder (pre-filled from what's on the mount), then Plex (Settings →
// Connections' own sign-in, plus the family switches), then indexers (a Prowlarr sync, or
// one tracker tested before it's saved — Connections' own dialog), then the Getting
// started checklist with a link to each step that's left. Folders apply live, so the restart only
// appears if a startup-only setting is waiting. Skipping a step only moves on to the next;
// ending setup early is its own small "Finish setup later" link, which says what's left.
// Settings → System → Run setup again opens it again with what's set shown.

type Step = "keys" | "folders" | "plex" | "indexers" | "finish";

const STEPS: { id: Step; label: string }[] = [
  { id: "keys", label: "Keys" },
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

// The optional keys, Hardcover first: what each adds, what happens without it, and where
// to get one. OpenSubtitles is one item with its login (the key alone only searches).
interface OptionalKey { id: string; name: string; what: string; url: string; recommended?: boolean }
const OPTIONAL_KEYS: OptionalKey[] = [
  {
    id: "hardcover", name: "Hardcover", recommended: true, url: "https://hardcover.app/account/api",
    what: "A better book catalogue, one entry per book. Without it, Books use Open Library.",
  },
  {
    id: "tvdb", name: "TheTVDB", url: "https://thetvdb.com/api-information",
    what: "Anime episode numbering. Without it, anime uses TMDB's numbering.",
  },
  {
    id: "omdb", name: "OMDb", url: "https://www.omdbapi.com/apikey.aspx",
    what: "IMDb, Rotten Tomatoes and Metacritic scores. Without it, those scores don't show.",
  },
];
const OPENSUBTITLES = {
  id: "opensubtitles", name: "OpenSubtitles", url: "https://www.opensubtitles.com/en/consumers",
  what: "Downloads subtitles your files lack. The key alone only searches; downloading also needs your login. Without it, subtitles come from your files, or local AI if you turn it on.",
};
const OS_FIELDS = [
  { id: "opensubtitles_api", label: "API key", placeholder: "Paste the key" },
  { id: "opensubtitles_username", label: "Username", placeholder: "Your username" },
  { id: "opensubtitles_password", label: "Password", placeholder: "Your password" },
] as const;

// The answer to a pre-save key check, for the value it was asked about. unasked: the check
// itself couldn't be made (Arrmada unreachable, no test for that key here), which says
// nothing about the key, so it never stands in the way of saving.
interface KeyCheck { value: string; ok: boolean; detail: string; busy?: boolean; unasked?: boolean }
type Answer = { ok: boolean; detail: string };

// useKeyChecks runs the pre-save checks, one per key: on paste, on leaving the field and
// on Next. The typed value goes to the provider once (never stored) and the answer shows
// under the field. The latest check per key is kept as a promise, so a blur and a click
// on Next for the same value (the click blurs the field first) share one request.
function useKeyChecks() {
  const [checks, setChecks] = useState<Record<string, KeyCheck>>({});
  const last = useRef<Record<string, { value: string; result: Promise<KeyCheck> } | undefined>>({});
  const check = useCallback((id: string, value: string, ask: () => Promise<Answer>): Promise<KeyCheck | null> => {
    if (!value) {
      last.current[id] = undefined;
      setChecks((c) => { const n = { ...c }; delete n[id]; return n; });
      return Promise.resolve(null);
    }
    const prev = last.current[id];
    if (prev?.value === value) return prev.result;
    setChecks((c) => ({ ...c, [id]: { value, busy: true, ok: false, detail: "" } }));
    const result = ask().then(
      (r): KeyCheck => ({ value, ok: r.ok, detail: r.detail }),
      (e: Error): KeyCheck => {
        // Couldn't ask: the next blur or Next asks again rather than reusing this answer.
        if (last.current[id]?.value === value) last.current[id] = undefined;
        return { value, ok: false, detail: e.message, unasked: true };
      },
    );
    const mine = { value, result };
    last.current[id] = mine;
    // undefined: this check failed to ask and cleared itself; still show why.
    void result.then((res) => {
      const cur = last.current[id];
      if (cur === mine || cur === undefined) setChecks((c) => ({ ...c, [id]: res }));
    });
    return result;
  }, []);
  return { checks, check };
}

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

  const confirm = useConfirm();
  const toast = useToast();

  // Ending setup early is a deliberate choice, made from a small link on every step (not
  // the main skip, which only moves on a step), and the confirm names what's still not set
  // up. Setup used to end from the first step's Skip, so an admin without a TMDB key at
  // hand was never asked where their media is.
  const finishLater = async () => {
    if (!state) return;
    const left = [
      !state.tmdb_configured && "the TMDB key",
      !state.libraries_chosen && "library folders",
      !state.plex_connected && "Plex",
      !state.indexers_enabled && "indexers",
    ].filter((s): s is string => !!s);
    if (left.length > 0) {
      const list = left.length === 1 ? left[0] : `${left.slice(0, -1).join(", ")} and ${left[left.length - 1]}`;
      const ok = await confirm({
        title: "Finish setup later?",
        body: `${list[0].toUpperCase()}${list.slice(1)} ${left.length === 1 ? "isn't" : "aren't"} set up yet. You can run setup again from Settings → System, and the Dashboard keeps a list of what's left.`,
        confirmLabel: "Finish later",
        cancelLabel: "Keep going",
      });
      if (!ok) return;
    }
    await finish();
  };

  // Every key typed on the first step is checked before it's saved (useKeyChecks).
  const { checks, check } = useKeyChecks();
  const typed = (id: string) => (keys[id] ?? "").trim();
  const checkKey = (id: string, value: string) => check(id, value.trim(), () => api.testAPIKey(id, value.trim()));
  // OpenSubtitles is checked as one: the key with the login typed beside it (the saved
  // parts fill in the rest). Only worth asking once there's a key, typed or saved.
  const osValue = () => {
    const [k, u, p] = OS_FIELDS.map((f) => typed(f.id));
    const keyKnown = !!k || !!state?.keys.find((s) => s.id === "opensubtitles_api")?.configured;
    return (k || u || p) && keyKnown ? [k, u, p].join("\u0000") : "";
  };
  const checkOS = () => {
    const [k, u, p] = OS_FIELDS.map((f) => typed(f.id));
    return check(OPENSUBTITLES.id, osValue(), () => setupApi.testOpenSubtitles(k, u, p));
  };

  const saveKeys = () => run(async () => {
    const asked = await Promise.all([
      ...["tmdb", ...OPTIONAL_KEYS.map((k) => k.id)].map(async (id) => ({ id, r: await checkKey(id, typed(id)) })),
      checkOS().then((r) => ({ id: OPENSUBTITLES.id, r })),
    ]);
    const name = (id: string) => (id === "tmdb" ? "TMDB" : id === OPENSUBTITLES.id ? OPENSUBTITLES.name : OPTIONAL_KEYS.find((k) => k.id === id)?.name ?? id);
    // A key its provider turned down is only saved if the admin says so. One that couldn't
    // be checked at all is saved as typed, and the toast says it wasn't checked.
    const failed = asked.filter((a) => a.r && !a.r.ok && !a.r.unasked);
    if (failed.length > 0 && !(await confirm({
      title: failed.length === 1 ? `Save this ${name(failed[0].id)} key anyway?` : "Save these keys anyway?",
      body: (
        <>
          {failed.map((f) => <span key={f.id} className="block">{name(f.id)} didn't accept it: {f.r?.detail}.</span>)}
          {failed.some((f) => f.id === "tmdb")
            ? <span className="mt-1 block">Movies and TV won't find anything until the TMDB key works.</span>
            : <span className="mt-1 block">Optional keys can stay blank: clear the field to carry on without it.</span>}
        </>
      ),
      confirmLabel: "Save anyway",
      cancelLabel: "Keep editing",
    }))) return;
    for (const [id, v] of Object.entries(keys)) {
      if (v.trim()) await api.setAPIKey(id, v.trim());
    }
    const unchecked = asked.filter((a) => a.r?.unasked).map((a) => name(a.id));
    if (unchecked.length > 0) toast(`Saved. ${unchecked.join(", ")} couldn't be checked first; Test it in Settings → Connections.`);
    setKeys({});
    await load();
    go("folders");
  });

  const saveFolders = () => run(async () => {
    // Passing this step with no library folder at all is allowed, but never by accident.
    if (folders && !folders.movies.trim() && !folders.tv.trim() && !folders.downloads.trim() && !(await confirm({
      title: "Continue without library folders?",
      body: "Arrmada can't find, download or import anything until Movies, TV and Downloads each have a folder. You can pick them later in Settings → Library.",
      confirmLabel: "Continue without them",
      cancelLabel: "Pick folders",
    }))) return;
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

  // Ending early, on every step but the last: small and under the card, so it's chosen,
  // not hit by mistake. Run again just goes back (its first step already has that button).
  const endEarly = step !== "finish" && !(rerun && step === "keys") && (
    <button type="button" onClick={() => (rerun ? onDone() : void finishLater())} disabled={busy} className="text-xs text-ink-faint underline hover:text-[var(--ink)]">
      {rerun ? "Back to Settings → System" : "Finish setup later"}
    </button>
  );

  return (
    <Frame after={endEarly || undefined}>
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
            {rerun ? "What's already set is shown; change anything, or carry on to the next step." : "A few quick steps and you're ready. Everything here can be changed later in Settings."}
          </p>

          <KeyField
            id="tmdb"
            status={tmdb}
            label="TMDB API key"
            // Not set: say what that means, without stopping anyone (Continue without a key).
            note={tmdb?.configured || typed("tmdb")
              ? "Needed for Movies and TV — titles, artwork and Discover. It's free."
              : <span style={{ color: "var(--avoid-text)" }}>No key yet? Movies, TV and Discover find nothing until one is added, now or in Settings → Connections. It's free.</span>}
            value={keys.tmdb ?? ""}
            onChange={(v, pasted) => { setKeys({ ...keys, tmdb: v }); if (pasted) void checkKey("tmdb", v); }}
            onBlur={(v) => { if (v.trim()) void checkKey("tmdb", v); }}
          />
          <CheckLine provider="TMDB" check={checks.tmdb} value={typed("tmdb")} />

          <section aria-labelledby="setup-optional-keys" className="mt-5 border-t pt-4" style={{ borderColor: "var(--line)" }}>
            <h2 id="setup-optional-keys" className="m-0 text-md font-bold">Optional keys — Arrmada works without them</h2>
            <p className="mb-3 mt-0.5 text-sm text-ink-dim">Add them now or later in Settings → Connections; Hardcover is the one worth adding.</p>
            <div className="flex flex-col gap-3">
              {OPTIONAL_KEYS.map((k) => (
                <div key={k.id}>
                  <KeyField
                    id={k.id}
                    status={keyStatus(k.id)}
                    label={k.name}
                    badge={k.recommended ? <StatusChip tone="accent" size="xs">Recommended</StatusChip> : <StatusChip tone="faint" size="xs">Optional</StatusChip>}
                    note={<>{k.what} <GetKey url={k.url} name={k.name} /></>}
                    value={keys[k.id] ?? ""}
                    onChange={(v, pasted) => { setKeys({ ...keys, [k.id]: v }); if (pasted) void checkKey(k.id, v); }}
                    onBlur={(v) => { if (v.trim()) void checkKey(k.id, v); }}
                  />
                  <CheckLine provider={k.name} check={checks[k.id]} value={typed(k.id)} />
                </div>
              ))}

              <fieldset
                className="m-0 min-w-0 border-0 p-0"
                aria-describedby="setup-key-opensubtitles-note"
                // Checked as one when focus leaves the group, so the login typed after the
                // key is part of the check.
                onBlur={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node | null)) void checkOS(); }}
              >
                <legend className="mb-1 flex w-full flex-wrap items-center gap-2 p-0">
                  <span className="text-base font-semibold">{OPENSUBTITLES.name}</span>
                  <StatusChip tone="faint" size="xs">Optional</StatusChip>
                </legend>
                <p id="setup-key-opensubtitles-note" className="m-0 mb-2 text-xs text-ink-dim">
                  {OPENSUBTITLES.what} <GetKey url={OPENSUBTITLES.url} name={OPENSUBTITLES.name} />
                </p>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                  {OS_FIELDS.map((f) => {
                    const s = keyStatus(f.id);
                    return (
                      <div key={f.id} className="min-w-0">
                        <div className="mb-1 flex flex-wrap items-baseline justify-between gap-x-2">
                          <label htmlFor={`setup-key-${f.id}`} className="text-sm font-semibold text-ink-dim">{f.label}</label>
                          {s?.configured && <Saved status={s} />}
                        </div>
                        <input
                          id={`setup-key-${f.id}`}
                          type={s?.secret === false ? "text" : "password"}
                          value={keys[f.id] ?? ""}
                          onChange={(e) => setKeys({ ...keys, [f.id]: e.target.value })}
                          placeholder={s?.configured ? "Leave blank to keep it" : f.placeholder}
                          // new-password: browsers never fill the admin's own Arrmada login in here.
                          autoComplete={f.id === "opensubtitles_password" ? "new-password" : "off"}
                          className="block w-full rounded-lg px-3 py-2 font-mono text-sm"
                          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
                        />
                      </div>
                    );
                  })}
                </div>
                <CheckLine provider={OPENSUBTITLES.name} check={checks[OPENSUBTITLES.id]} value={osValue()} />
              </fieldset>
            </div>
          </section>
          <Nav
            busy={busy}
            // The skip here only moves on a step, saving nothing: setup carries on to the
            // folders. Ending setup is the Finish setup later link.
            skip={rerun
              ? { label: "Back to Settings → System", onClick: () => onDone() }
              : !tmdb?.configured ? { label: "Continue without a key", onClick: () => go("folders") } : undefined}
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
              Arrmada is using its own managed storage — no media folder was given at install. To use your existing library, run the install line again on the server with <code className="font-mono">--media</code> and your media folder, for example <code className="font-mono">… | sh -s -- --media /mnt/user/data</code>. It mounts the folder and restarts Arrmada; then pick each library here.
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

// Frame is the wizard's page: the logo, the card, and (after) anything set apart below it.
function Frame({ children, after }: { children: React.ReactNode; after?: ReactNode }) {
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
        {after && <div className="mt-3 flex justify-center">{after}</div>}
      </div>
    </div>
  );
}

// KeyField is one key: its name (and a badge), whether one is saved — only the masked
// hint, never the value — the field, and a line on what it's for. Nothing is ever filled in.
function KeyField({ id, status, label, badge, note, value, onChange, onBlur }: {
  id: string; status?: APIKeyStatus; label: string; badge?: ReactNode; note: ReactNode; value: string;
  /** pasted: the change came from a paste, which is when a key is worth checking at once. */
  onChange: (v: string, pasted: boolean) => void;
  onBlur?: (v: string) => void;
}) {
  const pasting = useRef(false);
  const inputId = `setup-key-${id}`;
  return (
    <div>
      <div className="mb-1 flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
        <span className="flex items-center gap-2">
          <label htmlFor={inputId} className="text-base font-semibold">{label}</label>
          {badge}
        </span>
        {status?.configured && <Saved status={status} />}
      </div>
      <input
        id={inputId}
        type={status?.secret === false ? "text" : "password"}
        value={value}
        aria-describedby={`${inputId}-note`}
        onPaste={() => { pasting.current = true; }}
        onChange={(e) => { const p = pasting.current; pasting.current = false; onChange(e.target.value, p); }}
        onBlur={onBlur ? (e) => onBlur(e.target.value) : undefined}
        placeholder={status?.configured ? "Leave blank to keep the saved key" : "Paste the key"}
        autoComplete="off"
        className="block w-full rounded-lg px-3 py-2 font-mono text-sm"
        style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
      />
      <span id={`${inputId}-note`} className="mt-1 block text-xs text-ink-dim">
        {note}{id === "tmdb" && status?.help_url && <> · <a href={status.help_url} target="_blank" rel="noreferrer" className="underline" style={{ color: "var(--accent-text)" }}>get one</a></>}
      </span>
    </div>
  );
}

// Saved says a key is set without showing it: the server's hint is the last few
// characters of a secret ("…a1b2"), or a username (which isn't one).
function Saved({ status }: { status: APIKeyStatus }) {
  return (
    <span className="text-xs" style={{ color: "var(--good-text)" }}>
      ✓ saved{status.hint ? ` (${status.hint})` : ""}{status.source === "env" ? " · from install" : ""}
    </span>
  );
}

// GetKey links to where a free key is handed out.
function GetKey({ url, name }: { url: string; name: string }) {
  return (
    <a href={url} target="_blank" rel="noreferrer" className="whitespace-nowrap font-semibold underline" style={{ color: "var(--accent-text)" }} aria-label={`Get a free ${name} key (opens in a new tab)`}>
      Get a free key
    </a>
  );
}

// CheckLine is a key's pre-save check under its field, while it's for what's typed now.
function CheckLine({ provider, check, value }: { provider: string; check?: KeyCheck; value: string }) {
  if (!check || !value || check.value !== value) return null;
  const [color, text] = check.busy
    ? ["var(--ink-dim)", `Checking with ${provider}…`]
    : check.ok
      ? ["var(--good-text)", `✓ ${check.detail}`]
      : check.unasked
        ? ["var(--avoid-text)", `Couldn't check it (${check.detail}). It can still be saved; Test it later in Settings → Connections.`]
        : ["var(--reject-text)", `✗ ${check.detail}`];
  return <p role="status" className="m-0 mt-1 break-words text-sm" style={{ color }}>{text}</p>;
}

// Nav is a step's buttons. skip: a quiet way past the step without saving it ("Continue
// without a key", "Skip this step"), or Run setup again's way back to Settings.
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
