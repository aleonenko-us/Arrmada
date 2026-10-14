import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { PlexMergeDialog } from "../../components/PlexMergeDialog";
import { AutoNumber, SavedTick, wholeNumber } from "../../components/settings/auto";
import { Section, inputStyle } from "../../components/settings/ui";
import { api, type Person, type PlexBlock, type UserImpact, type UserQuota, type UserRole } from "../../lib/api";
import { peopleApi } from "../../lib/peopleApi";
import { accountApi } from "../../lib/accountApi";
import { LINKS } from "../../lib/links";
import { useMe } from "../../lib/me";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { ago } from "../../lib/taskTime";
import { useAutoSave, type AutoSave } from "../../lib/useAutoSave";
import { useLoadedSettings } from "../../lib/useSettings";
import { Button, ConfirmDialog, useConfirm, useToast } from "../../ui";
import { managerExceptions, requesterPages } from "./roles";

// Settings → People & access (admin only): everyone once — their role, what they
// auto-approve, their Plex link, when they were last around, where they're signed in,
// whether they may use the audiobook server — with Disable and Delete. It replaces the
// old Users list and the audiobook access list. Every control saves itself. Listening is
// shown as when, never what.
export function PeopleSettings() {
  const { user, booksEnabled } = useMe();
  return (
    <div className="flex flex-col gap-6">
      <PeopleList meId={user?.id} />
      <p className="m-0 text-sm text-ink-faint">
        Who may sign in with Plex, and what new Plex sign-ins auto-approve, is in <Link to={LINKS.plexSignIn} style={{ color: "var(--accent-text)" }}>Settings → Plex</Link>.
      </p>
      <Section id="request-limits" title="Request limits" subtitle="Optional: how much one person can ask for in a stretch of days. Following someone else's request is free, a withdrawn or declined request gives its share back, and admins and managers are never limited. 0 means no limit.">
        <RequestLimits books={booksEnabled} />
        <p className="m-0 text-xs text-ink-faint">Typical household: 10 movies, 5 seasons and 10 books a week. A season request counts each season; asking for a whole show counts all of its seasons. Set a different limit for one person under More on their row.</p>
      </Section>
    </div>
  );
}

type QuotaKey = "request_quota_movies" | "request_quota_seasons" | "request_quota_books" | "request_quota_days";
const TYPICAL: Record<QuotaKey, number> = { request_quota_movies: 10, request_quota_seasons: 5, request_quota_books: 10, request_quota_days: 7 };

// useQuota is one number in the request limits card, saved when its field is left.
function useQuota(key: QuotaKey, label: string, min: number, max: number) {
  const { s, commit } = useLoadedSettings();
  return useAutoSave(String(s[key] ?? (key === "request_quota_days" ? 7 : 0)), (v) => commit({ [key]: Number(v) }), { validate: wholeNumber(min, max, label) });
}

// RequestLimits is the global request limits; each number saves itself, and Typical
// household saves all four at once.
function RequestLimits({ books }: { books: boolean }) {
  const { commit } = useLoadedSettings();
  const movies = useQuota("request_quota_movies", "Movies", 0, 1000);
  const seasons = useQuota("request_quota_seasons", "Seasons", 0, 1000);
  const bookQ = useQuota("request_quota_books", "Books", 0, 1000);
  const days = useQuota("request_quota_days", "Every … days", 1, 365);
  const typical = useAutoSave<string>("", async () => { await commit(TYPICAL); });
  const field = "w-[96px] rounded-lg px-3 py-2 text-base";
  return (
    <div className="flex flex-wrap items-start gap-3">
      <AutoNumber label="Movies" save={movies} min={0} max={1000} className={field} />
      <AutoNumber label="Seasons" save={seasons} min={0} max={1000} className={field} />
      {books && <AutoNumber label="Books" save={bookQ} min={0} max={1000} className={field} />}
      <AutoNumber label="Every … days" save={days} min={1} max={365} className={field} />
      <div className="flex flex-col gap-1 pt-[18px]">
        <button
          type="button"
          onClick={() => void typical.commit("typical")}
          className="min-h-[36px] rounded-lg px-3 text-sm font-semibold"
          style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
        >
          Typical household
        </button>
        <SavedTick save={typical} />
      </div>
    </div>
  );
}

// Auto-approve is per media type: { movie, series, book }.
export type AutoTypes = { movie: boolean; series: boolean; book: boolean };
const NO_TYPES: AutoTypes = { movie: false, series: false, book: false };
const TYPE_LABEL: [keyof AutoTypes, string][] = [["movie", "Movies"], ["series", "Series"], ["book", "Books"]];
export const typesFromCSV = (csv: string): AutoTypes => {
  const on = csv.split(",").map((t) => t.trim());
  return { movie: on.includes("movie"), series: on.includes("series"), book: on.includes("book") };
};
export const typesToCSV = (t: AutoTypes) => TYPE_LABEL.filter(([k]) => t[k]).map(([k]) => k).join(",");
const typesOf = (p: Person): AutoTypes => ({ movie: p.auto_approve_movie, series: p.auto_approve_series, book: p.auto_approve_book });
const typeFlags = (t: AutoTypes) => ({ auto_approve_movie: t.movie, auto_approve_series: t.series, auto_approve_book: t.book });
const sameTypes = (a: AutoTypes, b: AutoTypes) => a.movie === b.movie && a.series === b.series && a.book === b.book;

// AutoApproveChecks is "Auto-approve: ☐ Movies ☐ Series ☐ Books" (Books only while the
// module is on; a hidden type keeps its value).
export function AutoApproveChecks({ value, onChange, books, label }: { value: AutoTypes; onChange: (v: AutoTypes) => void; books: boolean; label: string }) {
  return (
    <div role="group" aria-label={label} className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-sm text-ink-dim">
      {TYPE_LABEL.filter(([k]) => k !== "book" || books).map(([k, name]) => (
        <label key={k} className="flex min-h-[28px] items-center gap-1.5">
          <input type="checkbox" checked={value[k]} onChange={(e) => onChange({ ...value, [k]: e.target.checked })} />
          {name}
        </label>
      ))}
    </div>
  );
}

// A user's own limit as typed: "" follows the global limit (-1), otherwise a number
// (0 = unlimited).
const limitText = (n: number) => (n < 0 ? "" : String(n));
const limitValue = (s: string) => (s.trim() === "" ? -1 : Math.min(1000, Math.round(Number(s))));
const limitProblem = (what: string) => (v: string) => (v.trim() === "" || /^\d{1,4}$/.test(v.trim()) && Number(v) <= 1000 ? null : `${what}: blank for the usual limit, or 0–1000.`);

const ROLE_TONE: Record<string, string> = { admin: "var(--reject-text)", manager: "var(--accent-text)", requester: "var(--good-text)", readonly: "var(--ink-faint)" };
export const ROLES: { value: UserRole; label: string }[] = [
  { value: "requester", label: "Requester" },
  { value: "readonly", label: "Read-only" },
  { value: "manager", label: "Manager" },
  { value: "admin", label: "Admin" },
];

type UserPatch = Parameters<typeof peopleApi.updateUser>[1];

// PeopleList is everyone, one row each, plus the add-a-person form and the blocked Plex
// accounts. A person's saves run one after another: the server writes role and
// auto-approve together, so two at once could put one of them back.
function PeopleList({ meId }: { meId?: number }) {
  const toast = useToast();
  const [people, setPeople] = useState<Person[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [blocks, setBlocks] = useState<PlexBlock[]>([]);
  const [blockErr, setBlockErr] = useState<string | null>(null);
  const stored = useRef(new Map<number, Person>());
  const chains = useRef(new Map<number, Promise<unknown>>());

  const load = useCallback(() => {
    peopleApi.people().then((ps) => { stored.current = new Map(ps.map((p) => [p.id, p])); setPeople(ps); setErr(null); }).catch((e: Error) => setErr(e.message));
    peopleApi.plexBlocks().then(setBlocks).catch(() => {});
  }, []);
  useEffect(() => { load(); }, [load]);

  const merge = useCallback((id: number, patch: Partial<Person>) => {
    const cur = stored.current.get(id);
    if (cur) stored.current.set(id, { ...cur, ...patch });
    setPeople((ps) => ps && ps.map((p) => (p.id === id ? { ...p, ...patch } : p)));
  }, []);

  // queue runs fn after this person's earlier saves, with what the server last confirmed.
  const queue = useCallback(<T,>(id: number, fn: (cur: Person) => Promise<T>): Promise<T> => {
    const run = (chains.current.get(id) ?? Promise.resolve()).then(() => {
      const cur = stored.current.get(id);
      if (!cur) throw new Error("That person is no longer here.");
      return fn(cur);
    });
    chains.current.set(id, run.catch(() => {}));
    return run;
  }, []);

  const update = useCallback((id: number, build: (cur: Person) => UserPatch) => queue(id, async (cur) => {
    const body = build(cur);
    const r = await peopleApi.updateUser(id, body);
    // The answer is the account as it now stands; anything it leaves out stays as it was.
    merge(id, {
      role: r.role ?? cur.role, disabled: r.disabled ?? cur.disabled,
      auto_approve_movie: r.auto_approve_movie ?? cur.auto_approve_movie,
      auto_approve_series: r.auto_approve_series ?? cur.auto_approve_series,
      auto_approve_book: r.auto_approve_book ?? cur.auto_approve_book,
      ...(body.quota ? { quota: body.quota } : {}),
    });
  }), [queue, merge]);

  const setAudio = useCallback((id: number, allowed: boolean) => queue(id, async () => {
    await api.setAudioUser(id, allowed);
    merge(id, { audio_allowed: allowed });
  }), [queue, merge]);

  const unblock = async (b: PlexBlock) => {
    setBlockErr(null);
    try { setBlocks(await peopleApi.unblockPlex(b.plex_id)); load(); }
    catch (e) { setBlockErr((e as Error).message); }
  };

  const ctx: RowContext = { update, setAudio, reload: load, toast, people: people ?? [] };

  return (
    <Section id="users" title="People & access" subtitle="Everyone who can use Arrmada, and what each of them may do. Changes save as you make them. Auto-approve lets someone's requests download without waiting for you.">
      <RoleLegend />
      {err && <div className="text-sm" style={{ color: "var(--reject-text)" }}>{err}</div>}
      <div className="flex flex-col gap-2">
        {people === null ? (
          !err && <p className="text-sm text-ink-dim">Loading…</p>
        ) : people.length === 0 ? (
          <p className="text-sm text-ink-dim">No one yet.</p>
        ) : (
          people.map((p) => <PersonRow key={p.id} p={p} isMe={p.id === meId} ctx={ctx} />)
        )}
      </div>

      <AddPerson onAdded={load} />

      {blocks.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <div className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Blocked Plex accounts</div>
          {blocks.map((b) => (
            <div key={b.plex_id} className="flex flex-wrap items-center gap-3 rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }}>
              <span className="min-w-0 flex-1 truncate text-base">{b.name || `Plex account ${b.plex_id}`}</span>
              <span className="flex-none text-xs text-ink-faint">can't sign in with Plex</span>
              <button onClick={() => unblock(b)} className="flex-none rounded-lg px-2.5 py-1 text-sm font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>Unblock</button>
            </div>
          ))}
          {blockErr && <div className="text-sm" style={{ color: "var(--reject-text)" }}>{blockErr}</div>}
        </div>
      )}
    </Section>
  );
}

interface RowContext {
  update: (id: number, build: (cur: Person) => UserPatch) => Promise<void>;
  setAudio: (id: number, allowed: boolean) => Promise<void>;
  reload: () => void;
  toast: ReturnType<typeof useToast>;
  people: Person[];
}

const chip = "rounded-full px-2 py-0.5 font-mono text-2xs font-bold uppercase";

// PersonRow is one person. The row wraps into a stack on a phone.
function PersonRow({ p, isMe, ctx }: { p: Person; isMe: boolean; ctx: RowContext }) {
  const { booksEnabled } = useMe();
  const confirm = useConfirm();
  const [more, setMore] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  const role = useAutoSave<UserRole>(p.role, (v) => ctx.update(p.id, () => ({ role: v })), { keepOnError: false });
  const auto = useAutoSave<AutoTypes>(typesOf(p), (v) => ctx.update(p.id, () => typeFlags(v)), { equal: sameTypes, keepOnError: false });
  const audio = useAutoSave(p.audio_allowed, (v) => ctx.setAudio(p.id, v));

  const setSignIn = async (enable: boolean) => {
    if (!enable) {
      const ok = await confirm({
        title: `Turn off ${p.username}'s sign-in?`,
        body: "They're signed out of the web app and their audiobook apps, and can't sign in again until you turn it back on. Nothing of theirs is deleted.",
        confirmLabel: "Turn off sign-in",
        tone: "danger",
      });
      if (!ok) return;
    }
    setBusy("signin");
    try {
      await ctx.update(p.id, () => ({ disabled: !enable }));
      ctx.toast(enable ? `${p.username} can sign in again.` : `${p.username} is signed out and can't sign in.`, { tone: "good" });
      ctx.reload(); // turning sign-in off ends their sessions and apps
    } catch (e) { ctx.toast((e as Error).message, { tone: "error" }); }
    finally { setBusy(null); }
  };

  const signOutEverywhere = async () => {
    const ok = await confirm({
      title: `Sign ${p.username} out everywhere?`,
      body: "Every browser and phone signed in as them will have to sign in again. Their password, requests and audiobook apps stay as they are.",
      confirmLabel: "Sign out everywhere",
      tone: "danger",
    });
    if (!ok) return;
    setBusy("signout");
    try {
      const r = await accountApi.signOutEverywhere(p.id);
      ctx.toast(r.signed_out > 0 ? `Signed out of ${r.signed_out} ${r.signed_out === 1 ? "browser" : "browsers"}.` : "They weren’t signed in anywhere.", { tone: "good" });
      ctx.reload();
    } catch (e) { ctx.toast((e as Error).message, { tone: "error" }); }
    finally { setBusy(null); }
  };

  const faded = p.disabled ? { color: "var(--ink-faint)" } : undefined;
  return (
    <div className="flex flex-col gap-2 rounded-lg px-3 py-2.5" style={{ background: "var(--panel-2)" }} data-testid="person">
      {/* Who: name, Plex, role. */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <span className="grid h-7 w-7 flex-none place-items-center rounded-full text-xs font-bold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>{p.username[0]?.toUpperCase()}</span>
        <span className="min-w-[8rem] flex-1 truncate text-base font-semibold" style={faded}>{p.username}{isMe && <span className="font-normal text-ink-faint"> (you)</span>}</span>
        {p.disabled && <span className={chip} style={{ background: "var(--reject-soft)", color: "var(--reject-text)" }} title="Can't sign in. Nothing of theirs was deleted.">Disabled</span>}
        {p.plex_linked && (
          <span className="max-w-[160px] truncate rounded-full px-2 py-0.5 text-xs" style={{ background: "var(--panel)", color: p.plex_blocked ? "var(--reject-text)" : "var(--ink-dim)", border: "1px solid var(--line)" }} title={p.plex_blocked ? "Linked Plex account — blocked from signing in" : "Linked Plex account"}>
            Plex{p.plex_username ? ` · ${p.plex_username}` : ""}{p.plex_blocked ? " · blocked" : ""}
          </span>
        )}
        {p.plex_linked && p.auto_approve_series && p.role === "requester" && (
          <span className={chip} style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }} title="Their series requests are approved at once, every season asked for.">Approves whole shows</span>
        )}
        <span className="flex items-center gap-1.5">
          <select
            aria-label={`${p.username}'s role`}
            value={role.value}
            onChange={(e) => void role.commit(e.target.value as UserRole)}
            className="rounded-lg px-2 py-1 text-sm font-semibold"
            style={{ ...inputStyle, color: ROLE_TONE[role.value] ?? "var(--ink)" }}
          >
            {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
          </select>
        </span>
      </div>
      {role.status !== "idle" && <SavedTick save={role} />}

      {/* When and where: last seen, browsers, audiobooks. */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-sm text-ink-dim">
        <span title={p.last_seen ? new Date(p.last_seen).toLocaleString() : undefined}>Last seen {ago(p.last_seen)}</span>
        <span className="flex items-center gap-2">
          {p.sessions === 0 ? "Not signed in" : `Signed in on ${p.sessions} ${p.sessions === 1 ? "browser" : "browsers"}`}
          {!isMe && p.sessions > 0 && <Button size="sm" variant="secondary" busy={busy === "signout"} busyLabel="Signing out…" onClick={signOutEverywhere}>Sign out everywhere</Button>}
        </span>
        <AudiobookAccess p={p} save={audio} />
      </div>

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="text-sm text-ink-dim">Auto-approve:</span>
        <AutoApproveChecks value={auto.value} onChange={(v) => void auto.commit(v)} books={booksEnabled} label={`Auto-approve ${p.username}'s requests for`} />
        <SavedTick save={auto} />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {!isMe && (p.disabled
          ? <Button size="sm" busy={busy === "signin"} busyLabel="Turning on…" onClick={() => setSignIn(true)}>Turn sign-in back on</Button>
          : <Button size="sm" busy={busy === "signin"} busyLabel="Turning off…" onClick={() => setSignIn(false)}>Disable</Button>)}
        <Button size="sm" variant="ghost" aria-expanded={more} onClick={() => setMore((m) => !m)}>{more ? "Less" : "More…"}</Button>
        {!isMe && <Button size="sm" variant="ghost" style={{ color: "var(--reject-text)" }} onClick={() => setRemoving(true)}>Delete</Button>}
      </div>

      {more && <PersonMore p={p} isMe={isMe} ctx={ctx} />}
      {removing && <DeleteUserDialog p={p} onClose={() => setRemoving(false)} onDeleted={() => { setRemoving(false); ctx.reload(); }} />}
    </div>
  );
}

// AudiobookAccess is whether they may connect a listening app, and whether they've set
// the audiobook password apps sign in with.
function AudiobookAccess({ p, save }: { p: Person; save: AutoSave<boolean> }) {
  if (!p.audio_eligible) {
    return <span className="text-ink-faint">Audiobook apps: {p.disabled ? "sign-in is off" : "read-only accounts can't connect"}</span>;
  }
  return (
    <span className="flex flex-wrap items-center gap-2">
      <label className="flex items-center gap-1.5">
        <input type="checkbox" checked={save.value} onChange={(e) => void save.commit(e.target.checked)} aria-label={`${p.username} may use audiobook apps`} />
        Audiobook apps
      </label>
      <span style={{ color: p.audio_password_set ? "var(--good-text)" : "var(--ink-faint)" }}>{p.audio_password_set ? "password set" : "no password yet"}</span>
      {p.audio_devices > 0 && <span className="text-ink-faint">· {p.audio_devices} {p.audio_devices === 1 ? "app" : "apps"}</span>}
      <SavedTick save={save} />
    </span>
  );
}

// useOwnLimit is one of a person's own request limits, saved when its field is left. The
// server takes all three together, so the other two go along as they're stored.
function useOwnLimit(p: Person, k: keyof UserQuota, label: string, ctx: RowContext) {
  return useAutoSave(limitText(p.quota[k]), (v) => ctx.update(p.id, (cur) => ({ quota: { ...cur.quota, [k]: limitValue(v) } })), { validate: limitProblem(label) });
}

// plexUnlinkConfirm is the question before unlinking someone's Plex account, here and on
// Settings → Plex's linked accounts: a Plex-only account loses its only way in.
export function plexUnlinkConfirm(p: Person) {
  return {
    title: `Unlink ${p.username}'s Plex account${p.plex_username ? ` (${p.plex_username})` : ""}?`,
    body: p.plex_only
      ? "Plex is their only way in: nobody knows a password for this account. After unlinking, their next Sign in with Plex makes a new, empty account. Set a new password in Settings → People & access first if they should keep using this one."
      : "Sign in with Plex won't open this account any more, and Discover stops using their Plex watch history. They can link it again from their account menu.",
    confirmLabel: "Unlink",
    tone: "danger" as const,
  };
}

// PersonMore is what's changed less often: their own request limits, a new password, and
// their Plex link (unlink, block, or merge a duplicate account into another).
function PersonMore({ p, isMe, ctx }: { p: Person; isMe: boolean; ctx: RowContext }) {
  const { booksEnabled } = useMe();
  const movies = useOwnLimit(p, "movies", "Movies", ctx);
  const seasons = useOwnLimit(p, "seasons", "Seasons", ctx);
  const books = useOwnLimit(p, "books", "Books", ctx);

  const [password, setPassword] = useState("");
  const [pwBusy, setPwBusy] = useState(false);
  const [pwMsg, setPwMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const setPw = async () => {
    setPwBusy(true); setPwMsg(null);
    try { await ctx.update(p.id, () => ({ password })); setPassword(""); setPwMsg({ ok: true, text: isMe ? "Password changed. Your other devices were signed out." : "Password changed." }); }
    catch (e) { setPwMsg({ ok: false, text: (e as Error).message }); }
    finally { setPwBusy(false); }
  };

  const confirm = useConfirm();
  const plexAction = async (kind: "unlink" | "block") => {
    const ok = await confirm(kind === "unlink"
      ? plexUnlinkConfirm(p)
      : {
          title: `Block ${p.username}'s Plex account?`,
          body: "They won't be able to sign in here with Plex. Devices they're already signed in on stay signed in — Disable them to sign them out too.",
          confirmLabel: "Block", tone: "danger",
        });
    if (!ok) return;
    try {
      if (kind === "unlink") await peopleApi.updateUser(p.id, { plex_unlink: true });
      else await peopleApi.blockUserPlex(p.id);
      ctx.reload();
    } catch (e) { ctx.toast((e as Error).message, { tone: "error" }); }
  };
  // Accounts this one could be merged into: any other account with no Plex link of its own.
  const mergeTargets = ctx.people.filter((u) => u.id !== p.id && !u.plex_linked);
  const [mergeInto, setMergeInto] = useState<number | null>(null);
  const field = "w-[88px] rounded-lg px-2.5 py-1.5 text-base";

  return (
    <div className="flex flex-col gap-3 rounded-lg p-3" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <div>
        <div className="mb-1 font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Their request limits</div>
        <div className="flex flex-wrap items-start gap-2">
          <AutoNumber label="Movies" save={movies} min={0} max={1000} placeholder="Default" className={field} />
          <AutoNumber label="Seasons" save={seasons} min={0} max={1000} placeholder="Default" className={field} />
          {booksEnabled && <AutoNumber label="Books" save={books} min={0} max={1000} placeholder="Default" className={field} />}
        </div>
        <p className="m-0 mt-1 text-xs text-ink-faint">Blank uses the limits under Request limits; 0 means no limit for them.</p>
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">New password</span>
        <div className="flex flex-wrap items-center gap-2">
          <input type="password" minLength={8} aria-label={`New password for ${p.username}`} value={password} onChange={(e) => setPassword(e.target.value)} placeholder="8+ characters" autoComplete="new-password" className="min-w-0 flex-1 rounded-lg px-3 py-1.5 text-base" style={inputStyle} />
          <Button size="sm" variant="primary" disabled={password.length < 8} busy={pwBusy} busyLabel="Setting…" onClick={setPw}>Set password</Button>
        </div>
        {pwMsg && <span className="text-sm" style={{ color: pwMsg.ok ? "var(--good-text)" : "var(--reject-text)" }}>{pwMsg.text}</span>}
      </div>

      {p.plex_linked && !isMe && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="min-w-[12rem] flex-1 text-sm text-ink-dim">
            {p.plex_blocked
              ? "Their Plex account is blocked from signing in here. Unblock it under Blocked Plex accounts."
              : `Linked to Plex${p.plex_username ? ` as ${p.plex_username}` : ""}.`}
          </span>
          {!p.plex_blocked && <Button size="sm" style={{ color: "var(--reject-text)" }} onClick={() => plexAction("block")}>Block their Plex account</Button>}
          <Button size="sm" onClick={() => plexAction("unlink")}>Unlink Plex</Button>
        </div>
      )}
      {/* A duplicate Plex requester (the owner or a family member signed in with Plex before
          their own account was linked) can be folded into that account. */}
      {p.plex_linked && !isMe && (p.role === "requester" || p.role === "readonly") && mergeTargets.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-sm text-ink-dim">
          <span className="min-w-[12rem] flex-1">Same person as another account? Merge this one into it: requests, inbox, phones, audiobook progress and the Plex link move there.</span>
          <select aria-label="Merge into" value={mergeInto ?? ""} onChange={(e) => setMergeInto(e.target.value ? Number(e.target.value) : null)} className="rounded-lg px-2 py-1.5 text-sm" style={inputStyle}>
            <option value="">Merge into…</option>
            {mergeTargets.map((t) => <option key={t.id} value={t.id}>{t.username} ({t.role})</option>)}
          </select>
        </div>
      )}
      {mergeInto !== null && (
        <PlexMergeDialog targetId={mergeInto} fromId={p.id} onClose={() => setMergeInto(null)} onMerged={() => { setMergeInto(null); ctx.reload(); }} />
      )}
    </div>
  );
}

// AddPerson is the add-a-person form: an email and password (Plex sign-ins make their
// own accounts), a role and what they auto-approve.
function AddPerson({ onAdded }: { onAdded: () => void }) {
  const { booksEnabled } = useMe();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("requester");
  const [autoApprove, setAutoApprove] = useState<AutoTypes>(NO_TYPES);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  useUnsavedGuard(email.trim() !== "" || password !== "");
  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr(null);
    try {
      await peopleApi.createUser({ email: email.trim(), password, role, ...typeFlags(autoApprove) });
      setEmail(""); setPassword(""); setRole("requester"); setAutoApprove(NO_TYPES);
      onAdded();
    } catch (e) { setErr((e as Error).message); }
    finally { setBusy(false); }
  };
  return (
    <form onSubmit={add} className="mt-2 flex flex-col gap-2.5 rounded-lg p-3" style={{ border: "1px dashed var(--line)" }}>
      <div className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Add a person</div>
      <div className="flex flex-wrap gap-2">
        <input type="email" required aria-label="Email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="email@example.com" className="min-w-[180px] flex-1 rounded-lg px-3 py-2 text-base" style={inputStyle} />
        <input type="password" required minLength={8} aria-label="Password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="password (8+ chars)" autoComplete="new-password" className="min-w-[160px] flex-1 rounded-lg px-3 py-2 text-base" style={inputStyle} />
        <select aria-label="Role" value={role} onChange={(e) => setRole(e.target.value)} className="rounded-lg px-2.5 py-2 text-base" style={inputStyle}>
          {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
        </select>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-dim">
          <span>Auto-approve:</span>
          <AutoApproveChecks value={autoApprove} onChange={setAutoApprove} books={booksEnabled} label="Auto-approve this user's requests" />
        </div>
        <Button type="submit" variant="primary" busy={busy} busyLabel="Adding…">Add person</Button>
      </div>
      {err && <div className="text-sm" style={{ color: "var(--reject-text)" }}>{err}</div>}
    </form>
  );
}

// RoleLegend says what each role can do, from today's gates (see roles.ts): the requester
// pages are their top bar's, and Read-only can't request (POST /requests needs Requester).
function RoleLegend() {
  const { booksEnabled } = useMe();
  const pages = requesterPages(booksEnabled);
  const rows: [string, string][] = [
    ["Requester", `${pages}, and can request.`],
    ["Read-only", `the same pages, but can't request.`],
    ["Manager", managerExceptions()],
    ["Admin", "everything."],
  ];
  return (
    <div className="-mt-2 flex flex-col gap-0.5 text-xs text-ink-faint">
      {rows.map(([role, what]) => <div key={role}><b className="text-ink-dim">{role}</b> — {what}</div>)}
    </div>
  );
}

const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;

// DeleteUserDialog says what a delete would erase before it happens — as counts only, never
// which books (admins see how much, not what) — and asks for the username when it would
// erase someone's audiobook places. The server copies the database first either way.
function DeleteUserDialog({ p, onClose, onDeleted }: { p: Person; onClose: () => void; onDeleted: () => void }) {
  const [impact, setImpact] = useState<UserImpact | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // Plex-linked users come straight back on their next Plex sign-in unless this is ticked.
  const [blockPlex, setBlockPlex] = useState(false);

  useEffect(() => {
    peopleApi.userImpact(p.id).then(setImpact).catch((e: Error) => setErr(`Couldn't check what this would remove: ${e.message}`));
  }, [p.id]);

  const listening = !!impact && (impact.places > 0 || impact.listening_hours > 0);
  const hours = impact ? (impact.listening_hours >= 10 ? Math.round(impact.listening_hours) : Math.round(impact.listening_hours * 10) / 10) : 0;
  const canBlock = p.plex_linked && !p.plex_blocked;

  const confirm = async () => {
    setBusy(true); setErr(null);
    try { await peopleApi.deleteUser(p.id, listening ? p.username : undefined, canBlock && blockPlex); onDeleted(); }
    catch (e) { setErr((e as Error).message); setBusy(false); }
  };

  return (
    <ConfirmDialog
      title={<>Delete {p.username}?</>}
      body={impact ? (
        <>
          <p className="m-0">
            This permanently removes their place in {plural(impact.places, "audiobook")}, {hours} hour{hours === 1 ? "" : "s"} of listening history, {plural(impact.bookmarks, "bookmark")} and {plural(impact.devices, "signed-in device")}.
            {" "}Their {plural(impact.requests, "request")} stay.
          </p>
          <p className="m-0 mt-2">A copy of the database from just before is kept under Backups.</p>
          {canBlock && !blockPlex && <p className="m-0 mt-2">They can sign in again with Plex unless you block their Plex account below, or turn off their sign-in instead of deleting them.</p>}
        </>
      ) : !err ? "Checking what this would remove…" : null}
      extra={canBlock ? (
        <label className="flex items-center gap-2 text-sm text-ink-dim">
          <input type="checkbox" checked={blockPlex} disabled={busy} onChange={(e) => setBlockPlex(e.target.checked)} />
          Also block their Plex account from signing in here
        </label>
      ) : undefined}
      typedPhrase={listening ? p.username : undefined}
      confirmLabel="Delete user"
      busyLabel="Deleting…"
      busy={busy}
      error={err}
      confirmDisabled={!impact}
      onConfirm={confirm}
      onCancel={onClose}
    />
  );
}
