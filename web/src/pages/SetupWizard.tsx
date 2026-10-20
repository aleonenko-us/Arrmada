import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { api, ApiError, type APIKeyStatus, type LibraryPaths, type LoginAnswer, type SetupState } from "../lib/api";
import { EmbyConnect } from "./settings/EmbyConnection";
import AccountStep from "../components/setup/AccountStep";
import { FolderChips, FolderPicker } from "./Library";
import { restartAndWait } from "../lib/restart";
import { FleetMark } from "../components/FleetMark";
import { ChecklistRows, useSetupChecklist } from "../components/GettingStarted";
import { ProwlarrPanel } from "../components/connections/ProwlarrPanel";
import { useMe } from "../lib/me";
import { setupApi } from "../lib/setupApi";
import { Button, StatusChip, useConfirm, useToast } from "../ui";
import { ServerChoiceTile } from "../components/brand/ServerBadge";
import { ClientForm } from "../components/connections/ClientEditor";
import { SiteIcon } from "../components/connections/SiteIcon";
import { downloadClientApi, type DownloadClientTest } from "../lib/downloadClientApi";
import type { DownloadClient } from "../lib/api";
import { Icon, type IconName } from "../components/icons";
import { PlexConnect } from "./settings/PlexConnection";
import { JellyfinConnect } from "./settings/JellyfinConnection";

// SetupWizard is the first thing an admin sees on a fresh install: the TMDB key and the
// optional ones (Hardcover recommended), each checked before it's saved, then each library
// folder (pre-filled from what's on the mount), then the media server — Plex, Jellyfin,
// both or later: each one's connection editor from Settings → Connections, plus its own
// family sign-in switches — then indexers (a Prowlarr sync, or one tracker tested before
// it's saved — Connections' own dialog), then the Getting started checklist with a link to
// each step that's left. Folders apply live, so the restart only appears if a startup-only
// setting is waiting. Skipping a step only moves on to the next; ending setup early is its
// own small "Finish setup later" link, which says what's left.
// Settings → System → Run setup again opens it again with what's set shown.

type Step = "keys" | "folders" | "media" | "downloads" | "indexers" | "finish" | "account";

// A fresh install (firstRun) has no account yet, so the order is providers first and the
// account last: Sign in with Plex needs a connected server to check against.
const FIRST_RUN_STEPS: { id: Step; label: string }[] = [
  { id: "media", label: "Media server" },
  { id: "keys", label: "Keys" },
  { id: "folders", label: "Folders" },
  { id: "downloads", label: "Downloads" },
  { id: "indexers", label: "Indexers" },
  { id: "account", label: "Account" },
];

const STEPS: { id: Step; label: string }[] = [
  { id: "keys", label: "Keys" },
  { id: "folders", label: "Folders" },
  { id: "media", label: "Media server" },
  { id: "downloads", label: "Downloads" },
  { id: "indexers", label: "Indexers" },
  { id: "finish", label: "Finish" },
];

// The Media server step's choice. later: neither for now (nothing is saved).
type ServerChoice = "plex" | "jellyfin" | "emby" | "both" | "later";
const SERVER_CHOICES: { id: ServerChoice; label: string; hint: string }[] = [
  { id: "plex", label: "Plex", hint: "Plex Media Server" },
  { id: "jellyfin", label: "Jellyfin", hint: "Free and open source" },
  { id: "emby", label: "Emby", hint: "Emby Server" },
  { id: "both", label: "Both", hint: "Plex and Jellyfin side by side" },
  { id: "later", label: "Later", hint: "Connect one in Settings" },
];
// ChoiceIcon is the picture at the head of a media server choice: the server's own tile (Plex,
// Jellyfin, Emby), two overlapping tiles for Both, and a clock for Later.
const CHOICE_TILE = 36;
function ChoiceIcon({ id }: { id: ServerChoice }) {
  if (id === "both") {
    return (
      <span className="flex items-center gap-1.5" aria-hidden="true">
        <ServerChoiceTile server="plex" size={CHOICE_TILE} />
        <ServerChoiceTile server="jellyfin" size={CHOICE_TILE} />
      </span>
    );
  }
  if (id === "later") {
    return (
      <span aria-hidden="true" className="grid flex-none place-items-center rounded-xl text-ink-faint" style={{ width: CHOICE_TILE, height: CHOICE_TILE, border: "1px dashed var(--line)", background: "var(--panel)" }}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>
      </span>
    );
  }
  return <ServerChoiceTile server={id} size={CHOICE_TILE} />;
}
const usesPlex = (c: ServerChoice | null) => c === "plex" || c === "both";
const usesJellyfin = (c: ServerChoice | null) => c === "jellyfin" || c === "both";
const usesEmby = (c: ServerChoice | null) => c === "emby";

// The Indexers step reuses Settings → Connections' add-indexer dialog (presets, Test
// before saving); it loads when opened.
const AddIndexerModal = lazy(() => import("../components/connections/AddIndexerModal"));

// A server's family sign-in choices on the Media server step: off unless the owner turns
// them on here.
interface Family { enabled: boolean; types: AutoTypes }
type AutoTypes = { movie: boolean; series: boolean; book: boolean };
const TYPE_NAMES: [keyof AutoTypes, string][] = [["movie", "Movies"], ["series", "Series"], ["book", "Books"]];
const typesFrom = (csv: string): AutoTypes => {
  const on = csv.split(",").map((t) => t.trim());
  return { movie: on.includes("movie"), series: on.includes("series"), book: on.includes("book") };
};
const typesCSV = (t: AutoTypes) => TYPE_NAMES.filter(([k]) => t[k]).map(([k]) => k).join(",");

const FOLDERS: { key: keyof LibraryPaths; label: string; hint: string; icon: IconName; optional?: boolean }[] = [
  { key: "movies", label: "Movies", hint: "Where movie folders live", icon: "movies" },
  { key: "tv", label: "TV shows", hint: "Where show folders live", icon: "series" },
  { key: "downloads", label: "Downloads", hint: "Where qBittorrent saves downloads — keep it on the same drive as your libraries so finished downloads move instantly", icon: "downloads" },
  { key: "ebooks", label: "Ebooks", hint: "Only if you use Books", icon: "books", optional: true },
  { key: "audiobooks", label: "Audiobooks", hint: "Can be the same as Ebooks", icon: "audiobooks", optional: true },
  { key: "music", label: "Music", hint: "Only if you use Music", icon: "music", optional: true },
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
export function SetupWizard({ onDone, rerun = false, firstRun = false, onSignedIn }: {
  onDone: (to?: string) => void;
  rerun?: boolean;
  /** A fresh install with no account: providers first, the account last (setup session). */
  firstRun?: boolean;
  /** firstRun: the account step made the admin (or reached its two-factor step). */
  onSignedIn?: (answer: LoginAnswer) => void;
}) {
  const [state, setState] = useState<SetupState | null>(null);
  const order = firstRun ? FIRST_RUN_STEPS : STEPS;
  const nxt = (s: Step): Step => order[order.findIndex((x) => x.id === s) + 1]?.id ?? s;
  const prv = (s: Step): Step | undefined => order[order.findIndex((x) => x.id === s) - 1]?.id;
  // Back from plex.tv after a Plex sign-in by redirect (a phone, or popups blocked): the
  // address carries ?plexpin=, and the Media server step picks the sign-in up again.
  // A refresh comes back to the step it left (this tab only); what was saved is reloaded
  // from the server, so only half-typed fields are lost.
  const [step, setStep] = useState<Step>(() => {
    if (hasPlexPin()) return "media";
    const kept = readStep(firstRun);
    if (kept && order.some((x) => x.id === kept)) return kept;
    return firstRun ? "media" : "keys";
  });
  useEffect(() => { keepStep(firstRun, step); }, [firstRun, step]);
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
  const [jfFamily, setJFFamily] = useState<Family | null>(null);
  const [plexError, setPlexError] = useState<string | null>(null);
  // Which server(s) the Media server step sets up: what's connected already, Plex when
  // plex.tv has just sent a sign-in back, else nothing until the admin picks.
  const [choice, setChoice] = useState<ServerChoice | null>(() => (hasPlexPin() ? "plex" : null));
  const { booksEnabled } = useMe();

  const load = () => api.setupState().then((s) => {
    setState(s);
    // Pre-fill each folder: what's already chosen, else what the mount looks like it holds.
    setFolders((cur) => cur ?? Object.fromEntries(FOLDERS.map((f) => [
      f.key, s.libraries_chosen ? s.library[f.key] : (s.suggestions[f.key] ?? s.library[f.key] ?? ""),
    ])) as unknown as LibraryPaths);
    setFamily((cur) => cur ?? { enabled: !!s.plex_family?.enabled, types: typesFrom(s.plex_family?.auto_approve_types ?? "") });
    setJFFamily((cur) => cur ?? { enabled: !!s.jellyfin_family?.enabled, types: typesFrom(s.jellyfin_family?.auto_approve_types ?? "") });
    setChoice((cur) => cur ?? (s.plex_connected && s.jellyfin_connected ? "both" : s.jellyfin_connected ? "jellyfin" : s.plex_connected ? "plex" : null));
  }).catch((e) => setError((e as Error).message));
  useEffect(() => {
    // First run: open the setup session (a cookie) before anything asks for state.
    if (!firstRun) { load(); return; }
    setupApi.startSession().then(load).catch((e: Error) => {
      if (e instanceof ApiError && e.status === 409) { window.location.reload(); return; } // an admin exists now: sign in
      setError(e.message);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true); setError(null);
    try {
      try { await fn(); } catch (e) {
        // A setup session that ended (two hours) is started again, once.
        if (!(firstRun && e instanceof ApiError && e.status === 401)) throw e;
        await setupApi.startSession();
        await fn();
      }
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };

  const go = (s: Step) => { setError(null); setStep(s); };
  const finish = (to?: string) => run(async () => { await api.completeSetup(); keepStep(firstRun, null); onDone(to); });

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
      !state.plex_connected && !state.jellyfin_connected && "a media server",
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
    go(nxt("keys"));
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
    go(nxt("folders"));
  });

  // Each chosen server's family switches are saved as chosen here, both of them, so the
  // wizard never leaves a stored default deciding who gets in or what downloads unasked.
  // A server that wasn't chosen (and Later) keeps whatever it had.
  const saveMedia = () => run(async () => {
    if (usesPlex(choice) && family) {
      await api.updateSettings({ plex_login_enabled: family.enabled, plex_login_auto_approve_types: typesCSV(family.types) });
    }
    if (usesJellyfin(choice) && jfFamily) {
      await api.updateSettings({ jellyfin_login_enabled: jfFamily.enabled, jellyfin_login_auto_approve_types: typesCSV(jfFamily.types) });
    }
    go(nxt("media"));
  });

  // The Indexers step asks whether a Prowlarr answers (which can take a few seconds) only
  // when it opens; null while it's asking. showProwlarr: the sync panel is open.
  const [prowlarr, setProwlarr] = useState<boolean | null>(null);
  // The Downloads step's clients: null until read.
  const [clients, setClients] = useState<DownloadClient[] | null>(null);
  const refreshClients = useCallback(() => downloadClientApi.downloadClients().then(setClients).catch(() => setClients([])), []);
  useEffect(() => { if (step === "downloads") void refreshClients(); }, [step, refreshClients]);
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
  // No /storage: media is under /media, which is either Arrmada's managed volume or a host
  // folder the owner named in .env (ARRMADA_MEDIA_HOST). Only the first gets the warning.
  const noStorage = state.mounts.length > 0 && !state.mounts.includes("/storage");
  const managedOnly = noStorage && !state.media_host_folder;
  const mediaFolder = noStorage && !!state.media_host_folder;
  // One library folder: its icon, name and hint, the path with Browse beside it, and the
  // checks under it (missing, not writable, …).
  const folderRow = (f: (typeof FOLDERS)[number]) => (
    <div key={f.key} className="flex gap-3 px-3.5 py-3">
      <span aria-hidden className="mt-0.5 grid h-9 w-9 shrink-0 place-items-center rounded-lg" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>
        <Icon name={f.icon} size={18} />
      </span>
      <div className="min-w-0 flex-1">
        <label htmlFor={`setup-folder-${f.key}`} className="block text-base font-semibold">{f.label}</label>
        <span className="mb-2 block text-xs text-ink-faint">{f.hint}</span>
        <div className="flex items-stretch overflow-hidden rounded-lg focus-within:ring-2" style={{ border: "1px solid var(--line)", background: "var(--panel-2)", ["--tw-ring-color" as string]: "var(--accent-line)" }}>
          <input
            id={`setup-folder-${f.key}`}
            value={folders[f.key]}
            onChange={(e) => setFolders({ ...folders, [f.key]: e.target.value })}
            placeholder={f.optional ? "Leave blank if you don't use it" : "/storage/…"}
            className="min-w-0 flex-1 bg-transparent px-3 py-2 font-mono text-sm outline-none"
            style={{ color: "var(--ink)" }}
          />
          <button
            type="button"
            onClick={() => setPicking(f.key)}
            aria-label={`Browse for the ${f.label} folder`}
            className="flex flex-none items-center gap-1.5 px-3 text-sm font-semibold"
            style={{ borderLeft: "1px solid var(--line)", color: "var(--accent-text)" }}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" aria-hidden><path d="M1.8 4.2c0-.6.5-1.1 1.1-1.1h3l1.4 1.6h5.8c.6 0 1.1.5 1.1 1.1v6.1c0 .6-.5 1.1-1.1 1.1H2.9c-.6 0-1.1-.5-1.1-1.1z" /></svg>
            Browse
          </button>
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
    </div>
  );
  const stepIndex = order.findIndex((s) => s.id === step);
  const restartFirst = state.restart_needed && state.can_restart;

  // Ending early, on every step but the last: small and under the card, so it's chosen,
  // not hit by mistake. Run again just goes back (its first step already has that button).
  const endEarly = step !== "finish" && step !== "account" && !(rerun && step === "keys") && (
    <button type="button" onClick={() => (rerun ? onDone() : firstRun ? go("account") : void finishLater())} disabled={busy} className="text-xs text-ink-faint underline hover:text-[var(--ink)]">
      {rerun ? "Back to Settings → System" : firstRun ? "Skip to account" : "Finish setup later"}
    </button>
  );

  return (
    <Frame after={endEarly || undefined}>
      <ol aria-label="Setup steps" className="m-0 mb-5 flex list-none flex-wrap items-center gap-x-1.5 gap-y-1 p-0 text-xs font-semibold uppercase tracking-wide text-ink-faint">
        {order.map((s, i) => (
          <li key={s.id} aria-current={s.id === step ? "step" : undefined} style={{ color: s.id === step ? "var(--accent-text)" : undefined }}>
            {i > 0 && <span aria-hidden className="mr-1.5 text-ink-faint">›</span>}
            {i + 1}. {s.label}
          </li>
        ))}
      </ol>

      {error && <Callout tone="error" role="alert" className="mb-4">{error}</Callout>}

      {step === "keys" && (
        <>
          <h1 className="m-0 text-2xl font-bold">{rerun ? "Run setup again" : firstRun ? "Add your API keys" : "Welcome to Arrmada"}</h1>
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
                          autoComplete={f.id === "opensubtitles_username" ? "off" : "new-password"}
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
              : !tmdb?.configured ? { label: "Continue without a key", onClick: () => go(nxt("keys")) } : undefined}
            onNext={saveKeys}
            nextLabel={Object.values(keys).some((v) => v.trim()) ? "Save and continue" : "Continue"}
          />
        </>
      )}

      {step === "folders" && (
        <>
          <h1 className="m-0 text-2xl font-bold">Where's your media?</h1>
          <p className="mb-4 mt-1 text-md text-ink-dim">
            Pick a folder for each library. {state.libraries_chosen ? "These are your current folders." : "We've filled in anything that looked right — check it."}
          </p>
          {managedOnly && (
            <Callout tone="warn" className="mb-4">
              <strong className="font-semibold">No media folder was given at install</strong>, so Arrmada is using its own storage for now. To use your existing library, add your media folder to the install and restart Arrmada, then pick each library here.
              <details className="mt-1.5">
                <summary className="cursor-pointer text-xs font-semibold">How?</summary>
                <span className="mt-1 block text-xs">Run the install line again on the server with <code className="font-mono">--media</code> and your folder, for example <code className="font-mono">… | sh -s -- --media /mnt/user/data</code>.</span>
              </details>
            </Callout>
          )}
          {mediaFolder && (
            <p className="mb-3 text-sm text-ink-dim">
              Your media folder is mounted at <code className="font-mono">/media</code>: pick each library inside it.
            </p>
          )}
          <FolderGroup title="Video and downloads">
            {FOLDERS.filter((f) => !f.optional).map(folderRow)}
          </FolderGroup>
          <details className="group mt-3" open={state.libraries_chosen && FOLDERS.some((f) => f.optional && !!state.library[f.key])}>
            <summary className="flex cursor-pointer list-none items-center gap-2 rounded-lg px-1 py-1.5 text-sm font-semibold text-ink-dim">
              <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="transition-transform group-open:rotate-90" aria-hidden><path d="M6 3.5l4.5 4.5L6 12.5" /></svg>
              Books and music <StatusChip tone="faint" size="xs">Optional</StatusChip>
            </summary>
            <FolderGroup>{FOLDERS.filter((f) => f.optional).map(folderRow)}</FolderGroup>
          </details>
          <Nav busy={busy} blocked={folderBlocked} onBack={() => go(prv("folders")!)} onNext={saveFolders} nextLabel="Save and continue" />
          {picking && (
            <FolderPicker
              initial={folders[picking] || state.mounts[0]}
              onClose={() => setPicking(null)}
              onSelect={(p) => { setFolders({ ...folders, [picking]: p }); setPicking(null); }}
            />
          )}
        </>
      )}

      {step === "media" && family && jfFamily && (
        <>
          <h1 className="m-0 text-2xl font-bold">{firstRun ? "Welcome to Arrmada" : "Connect your media server"}</h1>
          {firstRun && <p className="mb-1 mt-1 text-md text-ink-dim">A few quick steps, and your account is made last. Everything here can be changed later in Settings.</p>}
          <p className="mb-3 mt-1 text-md text-ink-dim">
            Where your family watches: Plex, Jellyfin, Emby or both. Watch buttons, library updates, family sign-in, watch stats and pausing conversions while someone's watching all use it.
          </p>

          <fieldset className="m-0 min-w-0 border-0 p-0">
            <legend className="mb-1.5 p-0 text-base font-semibold">Which media server do you use?</legend>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-6">
              {SERVER_CHOICES.map((c, i) => {
                const on = choice === c.id;
                return (
                  <label
                    key={c.id}
                    className={`flex min-h-[112px] cursor-pointer flex-col items-start gap-2 rounded-xl p-4 focus-within:outline focus-within:outline-2 ${i < 3 ? "sm:col-span-2" : "sm:col-span-3"} ${i === SERVER_CHOICES.length - 1 && SERVER_CHOICES.length % 2 === 1 ? "col-span-2" : ""}`}
                    style={{
                      border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`,
                      background: on ? "var(--accent-soft)" : "var(--panel-2)",
                      outlineColor: "var(--accent)",
                    }}
                  >
                    <input type="radio" name="setup-media-server" value={c.id} checked={on} onChange={() => setChoice(c.id)} className="sr-only" />
                    <ChoiceIcon id={c.id} />
                    <span className="text-base font-semibold" style={{ color: on ? "var(--accent-text)" : undefined }}>{c.label}</span>
                    <span className="text-xs text-ink-dim">{c.hint}</span>
                  </label>
                );
              })}
            </div>
          </fieldset>

          {choice === "later" && (
            <p role="status" className="m-0 mt-4 text-sm text-ink-dim">
              Nothing to do here for now. Connect Plex or Jellyfin any time from its card in Settings → Connections; requests still download, and wait for it before they say "ready to watch".
            </p>
          )}

          {usesPlex(choice) && (
            <section aria-labelledby="setup-plex-title" className="mt-5 flex flex-col gap-1">
              <h2 id="setup-plex-title" className="m-0 flex items-center gap-2 text-md font-bold"><ServerChoiceTile server="plex" size={22} /> Plex</h2>
              <p className="m-0 text-sm text-ink-dim">Sign in with Plex and Arrmada finds your own server — never one someone else shared with you.</p>
              {state.plex_connected && (
                <p role="status" className="m-0 text-base font-semibold" style={{ color: "var(--good-text)" }}>
                  ✓ Connected{state.plex_server ? ` to ${state.plex_server}` : ""}
                </p>
              )}
              {plexError && (
                <div role="alert" className="rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--avoid)", color: "var(--avoid-text)" }}>
                  Couldn't reach plex.tv from the server — you can connect later in Settings → Connections, or enter the server's address below.
                  <span className="mt-1 block break-words text-ink-faint">{plexError}</span>
                </div>
              )}
              <PlexConnect onChanged={() => { setPlexError(null); void load(); }} onError={setPlexError} />
              <FamilyFields
                id="setup-plex-family"
                server="Plex"
                hint="People with access to your Plex server get a requester account the first time they sign in — no accounts to hand out."
                family={family}
                onChange={setFamily}
                books={booksEnabled}
              />
            </section>
          )}

          {usesJellyfin(choice) && (
            <section aria-labelledby="setup-jellyfin-title" className="mt-6 flex flex-col gap-1">
              <h2 id="setup-jellyfin-title" className="m-0 flex items-center gap-2 text-md font-bold"><ServerChoiceTile server="jellyfin" size={22} /> Jellyfin</h2>
              <p className="m-0 text-sm text-ink-dim">Its address and an API key made in Jellyfin (Dashboard → API Keys). Both are tested before they're saved, and the key is never shown again.</p>
              {state.jellyfin_connected && (
                <p role="status" className="m-0 text-base font-semibold" style={{ color: "var(--good-text)" }}>
                  ✓ Connected{state.jellyfin_server ? ` to ${state.jellyfin_server}` : ""}
                </p>
              )}
              <JellyfinConnect testFirst inWizard onChanged={() => void load()} />
              <FamilyFields
                id="setup-jellyfin-family"
                server="Jellyfin"
                hint="People with a user on your Jellyfin server get a requester account the first time they sign in with their Jellyfin username and password."
                family={jfFamily}
                onChange={setJFFamily}
                books={booksEnabled}
              />
            </section>
          )}

          {usesEmby(choice) && (
            <section aria-labelledby="setup-emby-title" className="mt-6 flex flex-col gap-1">
              <h2 id="setup-emby-title" className="m-0 flex items-center gap-2 text-md font-bold"><ServerChoiceTile server="emby" size={22} /> Emby</h2>
              <p className="m-0 text-sm text-ink-dim">Its address and an API key made in Emby. Both are tested before they're saved. Emby has no sign-in for Arrmada, so the account is made with an email and password.</p>
              {state.emby_connected && <p role="status" className="m-0 text-base font-semibold" style={{ color: "var(--good-text)" }}>✓ Connected</p>}
              <EmbyConnect testFirst onChanged={() => void load()} />
            </section>
          )}

          <Nav
            busy={busy}
            onBack={prv("media") ? () => go(prv("media")!) : undefined}
            skip={choice === null ? { label: "Skip this step", onClick: () => go(nxt("media")) } : undefined}
            onNext={choice === null || choice === "later" ? () => go(nxt("media")) : saveMedia}
            nextLabel={choice === null || choice === "later" ? "Continue" : "Save and continue"}
          />
        </>
      )}

      {step === "account" && onSignedIn && (
        <AccountStep
          plexServer={state.plex_connected ? state.plex_server : undefined}
          jellyfinServer={state.jellyfin_connected ? state.jellyfin_server : undefined}
          onBack={() => go(prv("account") ?? "media")}
          onSignedIn={onSignedIn}
        />
      )}

      {step === "downloads" && (
        <>
          <h1 className="m-0 text-2xl font-bold">Where downloads happen</h1>
          <p className="mb-4 mt-1 text-md text-ink-dim">
            Arrmada hands what it finds to a download client: qBittorrent for torrents, SABnzbd for Usenet. Connect the ones you use; you need at least one.
          </p>
          <div className="flex flex-col gap-3">
            {DOWNLOADERS.map((d) => (
              <DownloaderCard
                key={d.kind}
                kind={d.kind}
                name={d.name}
                what={d.what}
                clients={(clients ?? []).filter((c) => c.kind === d.kind)}
                loading={clients === null}
                onSaved={() => { void refreshClients(); void load(); }}
              />
            ))}
          </div>
          <Nav
            busy={busy}
            onBack={() => go(prv("downloads")!)}
            skip={(clients ?? []).length > 0 ? undefined : { label: "Skip this step", onClick: () => go(nxt("downloads")) }}
            onNext={() => go(nxt("downloads"))}
            nextLabel="Continue"
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
            <p className="m-0 text-sm text-ink-dim">
              {state.usenet_client
                ? "Usenet indexers (NZBgeek, DrunkenSlug, NZBFinder and more) are in the list too."
                : "Usenet indexers are in the list too; connect SABnzbd on the Downloads step to use them."}
            </p>
          </div>
          {addingIndexer && (
            <Suspense fallback={null}>
              <AddIndexerModal
                usenet={!!state.usenet_client}
                onClose={() => setAddingIndexer(false)}
                onAdded={(created) => { setAddingIndexer(false); toast(`Added ${created.name}`, { tone: "good" }); void refreshIndexers(); }}
                onProwlarr={() => { setAddingIndexer(false); setShowProwlarr(true); }}
              />
            </Suspense>
          )}
          <Nav
            busy={busy}
            onBack={() => go(prv("indexers")!)}
            skip={(state.indexers_enabled ?? 0) > 0 ? undefined : { label: "Skip this step", onClick: () => go(nxt("indexers")) }}
            onNext={() => go(nxt("indexers"))}
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
              <ChecklistRows items={checklist.data.items} onOpen={restartFirst ? undefined : (to) => finish(to)} onChanged={checklist.refetch} />
            ) : (
              <p className="m-0 px-2 py-1.5 text-sm text-ink-faint">{checklist.error ? "Couldn't read the checklist — the Dashboard shows it too." : "Checking…"}</p>
            )}
            <p className="m-0 mt-1 px-2 py-1.5 text-sm text-ink-dim">
              Moving from Radarr or Sonarr?{" "}
              {restartFirst ? "After the restart, Settings → Import & migrate brings your library, indexers and download clients over." : (
                <a href="/settings/import#arr-import" onClick={(e) => { e.preventDefault(); finish("/settings/import#arr-import"); }} style={{ color: "var(--accent-text)" }}>Import them now</a>
              )}
            </p>
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

// The step a refresh returns to, per tab: sessionStorage, which may be blocked (then a
// refresh starts over, as before).
const stepKey = (firstRun?: boolean) => `arrmada.setup.step${firstRun ? ".first" : ""}`;
function readStep(firstRun?: boolean): Step | null {
  try { return sessionStorage.getItem(stepKey(firstRun)) as Step | null; } catch { return null; }
}
function keepStep(firstRun: boolean | undefined, step: Step | null) {
  try {
    if (step) sessionStorage.setItem(stepKey(firstRun), step);
    else sessionStorage.removeItem(stepKey(firstRun));
  } catch { /* private mode: nothing kept */ }
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
  // A saved key shows as saved, with Change; the field only opens when asked for (or when
  // something is already typed in it).
  const [changing, setChanging] = useState(false);
  const showSaved = !!status?.configured && !changing && !value;
  const noteEl = (
    <span id={`${inputId}-note`} className="mt-1 block text-xs text-ink-dim">
      {note}{id === "tmdb" && status?.help_url && <> · <a href={status.help_url} target="_blank" rel="noreferrer" className="underline" style={{ color: "var(--accent-text)" }}>get one</a></>}
    </span>
  );
  return (
    <div>
      <div className="mb-1 flex flex-wrap items-center gap-2">
        {showSaved ? <span id={inputId} className="text-base font-semibold">{label}</span> : <label htmlFor={inputId} className="text-base font-semibold">{label}</label>}
        {badge}
      </div>
      {showSaved && status ? (
        <div className="flex items-center gap-3 rounded-lg px-3 py-2" style={{ background: "var(--good-soft)", border: "1px solid var(--line)" }} aria-describedby={`${inputId}-note`}>
          <span aria-hidden className="grid h-6 w-6 shrink-0 place-items-center rounded-full" style={{ background: "var(--good)", color: "var(--accent-ink)" }}>
            <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round"><path d="M3.5 8.5l3 3 6-7" /></svg>
          </span>
          <span className="min-w-0 flex-1 text-sm">
            <span className="font-semibold">Key saved</span>
            {status.hint && <span className="ml-1.5 font-mono text-xs text-ink-dim">••••{status.hint.replace(/^…/, "")}</span>}
            {status.source === "env" && <span className="ml-1.5 text-xs text-ink-faint">· from install</span>}
          </span>
          <button type="button" onClick={() => setChanging(true)} className="shrink-0 rounded-md px-2.5 py-1 text-sm font-semibold" style={{ border: "1px solid var(--line)", background: "var(--panel)", color: "var(--accent-text)" }} aria-label={`Change the ${label}`}>
            Change
          </button>
        </div>
      ) : (
        <>
          <input
            id={inputId}
            type={status?.secret === false ? "text" : "password"}
            value={value}
            aria-describedby={`${inputId}-note`}
            autoFocus={changing}
            onPaste={() => { pasting.current = true; }}
            onChange={(e) => { const p = pasting.current; pasting.current = false; onChange(e.target.value, p); }}
            onBlur={onBlur ? (e) => onBlur(e.target.value) : undefined}
            placeholder={status?.configured ? "Paste the new key" : "Paste the key"}
            autoComplete="new-password"
            className="block w-full rounded-lg px-3 py-2 font-mono text-sm"
            style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
          />
          {status?.configured && (
            <button type="button" onClick={() => { setChanging(false); onChange("", false); }} className="mt-1 text-xs font-semibold underline" style={{ color: "var(--accent-text)" }}>
              Keep the saved key
            </button>
          )}
        </>
      )}
      {noteEl}
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

// FamilyFields is one server's "Let family sign in with <server>" checkbox, with which
// request types a new sign-in may download unasked once it's ticked.
function FamilyFields({ id, server, hint, family, onChange, books }: {
  id: string; server: string; hint: string; family: Family; onChange: (f: Family) => void; books: boolean;
}) {
  return (
    <fieldset className="m-0 mt-3 flex flex-col gap-2.5 rounded-xl p-3.5" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      <legend className="sr-only">{server} family sign-in</legend>
      <div className="flex items-start gap-2.5 text-base">
        <input id={id} type="checkbox" className="mt-0.5" aria-describedby={`${id}-hint`} checked={family.enabled} onChange={(e) => onChange({ ...family, enabled: e.target.checked })} />
        <div>
          <label htmlFor={id} className="block font-semibold">Let family sign in with {server}</label>
          <span id={`${id}-hint`} className="block text-sm text-ink-faint">{hint}</span>
        </div>
      </div>
      {family.enabled && (
        <div role="group" aria-label={`Auto-approve their requests (${server})`} className="ml-6 flex flex-col gap-1.5">
          <span className="text-base font-semibold">Auto-approve their requests</span>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-sm text-ink-dim">
            {TYPE_NAMES.filter(([k]) => k !== "book" || books).map(([k, name]) => (
              <label key={k} className="flex min-h-[28px] items-center gap-1.5">
                <input type="checkbox" checked={family.types[k]} onChange={(e) => onChange({ ...family, types: { ...family.types, [k]: e.target.checked } })} />
                {name}
              </label>
            ))}
          </div>
          <span className="text-xs text-ink-faint">Ticked types download straight away; the rest wait for you to approve. A series request can pull every season of a long show.</span>
        </div>
      )}
    </fieldset>
  );
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

// FolderGroup is a card of folder rows, split by hairlines.
function FolderGroup({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col">
      {title && <h2 className="m-0 mb-1.5 px-1 text-xs font-bold uppercase tracking-wide text-ink-faint">{title}</h2>}
      <div className="flex flex-col overflow-hidden rounded-xl [&>*+*]:border-t [&>*+*]:border-[color:var(--line)]" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
        {children}
      </div>
    </section>
  );
}

// Callout is the wizard's note box: a tinted panel with an icon, for a problem (error) or
// something to know before going on (warn).
function Callout({ tone, role, className = "", children }: { tone: "error" | "warn"; role?: string; className?: string; children: ReactNode }) {
  const c = tone === "error"
    ? { bg: "var(--reject-soft)", line: "var(--reject)", ink: "var(--reject-text)" }
    : { bg: "var(--avoid-soft)", line: "var(--avoid)", ink: "var(--avoid-text)" };
  return (
    <div role={role} className={`flex gap-2.5 rounded-xl p-3 text-sm ${className}`} style={{ background: c.bg, border: `1px solid ${c.line}` }}>
      <span aria-hidden className="grid h-6 w-6 shrink-0 place-items-center rounded-full" style={{ background: c.line, color: "var(--accent-ink)" }}>
        <svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round"><path d="M8 3.5v5.5M8 12.2v.3" /></svg>
      </span>
      <div className="min-w-0 flex-1 self-center leading-snug" style={{ color: "var(--ink)" }}>{children}</div>
    </div>
  );
}

const DOWNLOADERS: { kind: "qbittorrent" | "sabnzbd"; name: string; what: string }[] = [
  { kind: "qbittorrent", name: "qBittorrent", what: "Torrents: public and private trackers" },
  { kind: "sabnzbd", name: "SABnzbd", what: "Usenet: needs a Usenet provider and indexers like NZBgeek" },
];

// DownloaderCard is one download client on the Downloads step: what's connected (with a
// Test), or Add, which opens the same form Settings → Connections uses.
function DownloaderCard({ kind, name, what, clients, loading, onSaved }: {
  kind: "qbittorrent" | "sabnzbd"; name: string; what: string; clients: DownloadClient[]; loading: boolean; onSaved: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const [tests, setTests] = useState<Record<number, DownloadClientTest | "busy">>({});
  const test = async (id: number) => {
    setTests((t) => ({ ...t, [id]: "busy" }));
    const r = await downloadClientApi.testDownloadClient(id).catch((e: Error) => ({ ok: false, error: e.message }));
    setTests((t) => ({ ...t, [id]: r }));
  };
  return (
    <section className="rounded-xl p-3.5" style={{ border: "1px solid var(--line)", background: "var(--panel)" }} aria-label={name}>
      <div className="flex items-center gap-3">
        <SiteIcon slug={kind} monogram={kind === "sabnzbd" ? "SB" : "QB"} size={40} />
        <div className="min-w-0 flex-1">
          <div className="text-md font-semibold">{name}</div>
          <div className="text-xs text-ink-faint">{what}</div>
        </div>
        {!adding && !loading && (
          <Button size="sm" variant={clients.length ? "secondary" : "primary"} onClick={() => setAdding(true)}>
            {clients.length ? "Add another" : `Add ${name}`}
          </Button>
        )}
      </div>
      {clients.map((c) => {
        const t = tests[c.id];
        return (
          <div key={c.id} className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg px-3 py-2 text-sm" style={{ background: "var(--panel-2)" }}>
            <span className="font-semibold" style={{ color: "var(--good-text)" }}>✓ {c.name}</span>
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-faint">{c.url}</span>
            <button type="button" onClick={() => void test(c.id)} disabled={t === "busy"} className="text-sm font-semibold" style={{ color: "var(--accent-text)" }}>
              {t === "busy" ? "Testing…" : "Test"}
            </button>
            {t && t !== "busy" && (
              <span className="basis-full text-xs" style={{ color: t.ok ? "var(--good-text)" : "var(--reject-text)" }}>
                {t.ok ? "Connected." : t.error || "Couldn't connect."}
              </span>
            )}
          </div>
        );
      })}
      {adding && (
        <div className="mt-3">
          <ClientForm initialKind={kind} onSaved={() => { setAdding(false); onSaved(); }} />
          <button type="button" onClick={() => setAdding(false)} className="mt-2 text-sm text-ink-faint underline">Cancel</button>
        </div>
      )}
    </section>
  );
}
