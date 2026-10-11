import { useCallback, useEffect, useState } from "react";
import { Link, Navigate, useSearchParams } from "react-router-dom";
import { AutoNumber, AutoToggle, SavedTick, wholeNumber } from "../../components/settings/auto";
import { Note, Section } from "../../components/settings/ui";
import { api, type Person, type PlexConfig, type PlexStatus } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { isAdmin, useMe } from "../../lib/me";
import { ago } from "../../lib/taskTime";
import { useAutoSave } from "../../lib/useAutoSave";
import { useSetting } from "../../lib/useSettings";
import { Button, useConfirm, useToast } from "../../ui";
import { PlexLibraryUpdates } from "./PlexLibraryUpdates";
import { requesterPages } from "./roles";
import { AutoApproveChecks, ROLES, plexUnlinkConfirm, typesFromCSV, typesToCSV } from "./People";

// Settings → Plex: everything about how Arrmada uses the Plex server, on one page — which
// server it is, watch monitoring, the scans Arrmada sends after it changes a file, and
// (admins) who may sign in with Plex, which accounts are linked to Plex and the Tautulli
// import. The connection itself (Sign in with Plex, address and token, Test) is the Plex
// card on Settings → Connections, next to every other integration; the Plex server card
// here links to it, and a link there opens its editor.
export function PlexSettings() {
  const { user } = useMe();
  const admin = isAdmin(user);
  // plex.tv used to send a redirect-mode "Sign in with Plex" back here. One that was under
  // way across an update still finishes: the connection editor picks the PIN up.
  const [params] = useSearchParams();
  const pin = params.get("plexpin");
  if (pin) return <Navigate to={`/settings/connections?plexpin=${encodeURIComponent(pin)}`} replace />;
  return <PlexPage admin={admin} />;
}

function PlexPage({ admin }: { admin: boolean }) {
  // One read of the Plex settings for the server and monitoring cards; a monitoring save
  // answers with the whole config, so the server card's status line follows it.
  const [cfg, setCfg] = useState<PlexConfig | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { api.insightsConfig().then(setCfg).catch((e: Error) => setErr(e.message)); }, []);
  return (
    <div className="flex flex-col gap-6">
      <PlexServer cfg={cfg} err={err} />
      {cfg && <PlexMonitoring cfg={cfg} setCfg={setCfg} />}
      {/* Path mapping and the last scan; its own load and Save. */}
      <PlexLibraryUpdates />
      {admin && <PlexSignIn />}
      {admin && <PlexAccounts />}
      {admin && <PlexTautulli />}
    </div>
  );
}

const STATUS_LINE: Record<PlexStatus, { text: string; color: string }> = {
  unconfigured: { text: "Not connected", color: "var(--avoid)" },
  off: { text: "Connected · watch monitoring is off", color: "var(--avoid)" },
  recording: { text: "Connected · recording what's watched", color: "var(--good)" },
  unreachable: { text: "Plex isn't answering", color: "var(--reject)" },
};

// PlexServer says which server Arrmada is connected to and how it's doing, and sends you
// to the Plex card on Connections to connect or change it.
function PlexServer({ cfg, err }: { cfg: PlexConfig | null; err: string | null }) {
  const connected = !!cfg && cfg.status !== "unconfigured";
  const line = cfg ? STATUS_LINE[cfg.status] ?? STATUS_LINE.unconfigured : null;
  return (
    <Section id="plex-server" title="Plex server" subtitle="Insights records what's watched from it, library updates go to it, and Plex sign-in checks people against it. The connection itself — Sign in with Plex, the address and token, Test — is the Plex card in Settings → Connections.">
      {!cfg ? (
        err ? <p role="alert" className="m-0 text-[12px]" style={{ color: "var(--reject)" }}>Couldn't load the Plex connection: {err}</p> : <p className="m-0 text-[12px] text-ink-dim">Loading…</p>
      ) : (
        <div className="flex flex-col gap-2">
          {connected && (
            <div className="text-[12.5px]">
              Connected to <b className="font-semibold">{cfg.server_name || "your Plex server"}</b>{" "}
              <span className="break-all font-mono text-[11px] text-ink-faint">{cfg.url}</span>
            </div>
          )}
          {line && (
            <div className="break-words text-[11.5px]" style={{ color: line.color }}>
              {line.text}
              {cfg.status === "unreachable" && cfg.last_error ? `: ${cfg.last_error}` : ""}
              {cfg.status === "recording" && cfg.last_poll_at ? ` · last answer ${ago(cfg.last_poll_at)}` : ""}
            </div>
          )}
          <div>
            {connected ? (
              <Link to={LINKS.plexConnection} className="text-[12px] font-semibold" style={{ color: "var(--accent)" }}>Change the connection →</Link>
            ) : (
              <Link to={LINKS.plexConnection} className="inline-block rounded-lg px-3.5 py-2 text-[12.5px] font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>Connect your Plex server →</Link>
            )}
          </div>
        </div>
      )}
    </Section>
  );
}

// PlexMonitoring is Insights' recorder: on or off, and how often it asks Plex what's
// playing. Each saves itself and sends only its own field, so it can't put back an
// address or token changed meanwhile on Connections.
function PlexMonitoring({ cfg, setCfg }: { cfg: PlexConfig; setCfg: (c: PlexConfig) => void }) {
  const connected = cfg.status !== "unconfigured";
  // Until someone chooses, the server switches monitoring on when Plex is first connected,
  // so an unconnected install shows it on; a connected one that never chose isn't recording.
  const on = useAutoSave(cfg.enabled_set ? cfg.enabled : !connected, (v) => api.updateInsightsConfig({ enabled: v }).then(setCfg));
  const poll = useAutoSave(String(cfg.poll_seconds || 5), (v) => api.updateInsightsConfig({ poll_seconds: Number(v) }).then(setCfg), {
    validate: wholeNumber(2, 60, "The interval"),
  });
  return (
    <Section id="plex-monitoring" title="Watch monitoring" subtitle="Insights' Activity, History, People and Graphs come from this: Arrmada asks Plex what's playing every few seconds and records each play. It switches on by itself when Plex is first connected.">
      <AutoToggle
        label="Record what's watched"
        hint={connected ? "Off keeps everything already recorded and stops adding to it." : "Starts once your Plex server is connected."}
        save={on}
      />
      <AutoNumber label="Ask Plex every (seconds)" save={poll} min={2} max={60} className="w-[90px] rounded-lg px-3 py-2 text-[12.5px]">
        <span className="text-[11px] text-ink-faint">2 to 60. Shorter catches short plays and pauses more exactly; 5 is plenty for most homes.</span>
      </AutoNumber>
    </Section>
  );
}

// PlexAccounts (admin): which Arrmada accounts are linked to which Plex account, with
// Unlink. Blocking a Plex account and merging a duplicate Plex sign-in stay under More on
// the person's row in People & access, where the rest of their access is.
function PlexAccounts() {
  const { user } = useMe();
  const confirm = useConfirm();
  const toast = useToast();
  const [people, setPeople] = useState<Person[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const load = useCallback(() => api.people().then((ps) => { setPeople(ps); setErr(null); }).catch((e: Error) => setErr(e.message)), []);
  useEffect(() => { void load(); }, [load]);

  const unlink = async (p: Person) => {
    if (!(await confirm(plexUnlinkConfirm(p)))) return;
    setBusy(p.id);
    try {
      await api.updateUser(p.id, { plex_unlink: true });
      toast(`${p.username} is no longer linked to Plex.`, { tone: "good" });
      await load();
    } catch (e) { toast((e as Error).message, { tone: "error" }); }
    finally { setBusy(null); }
  };

  const linked = (people ?? []).filter((p) => p.plex_linked);
  return (
    <Section id="plex-accounts" title="Linked Plex accounts" subtitle="Who signs in with which Plex account. A Plex sign-in opens the Arrmada account linked to it; Discover also uses that person's Plex watch history.">
      {people === null ? (
        err ? <p role="alert" className="m-0 text-[12px]" style={{ color: "var(--reject)" }}>Couldn't load the accounts: {err}</p> : <p className="m-0 text-[12px] text-ink-dim">Loading…</p>
      ) : linked.length === 0 ? (
        <p className="m-0 text-[12px] text-ink-dim">No account is linked to Plex yet. Someone's first Sign in with Plex links one, and anyone can link their own Plex account themselves.</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
          {linked.map((p) => (
            <li key={p.id} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }} data-testid="plex-account">
              <span className="min-w-[8rem] flex-1 truncate text-[12.5px] font-semibold">
                {p.username}{p.id === user?.id && <span className="font-normal text-ink-faint"> (you)</span>}
              </span>
              <span className="min-w-0 truncate text-[11.5px] text-ink-dim">Plex · {p.plex_username || "linked"}</span>
              <span className="text-[11px] text-ink-faint">{ROLES.find((r) => r.value === p.role)?.label ?? p.role}</span>
              {p.plex_blocked && <span className="rounded-full px-2 py-0.5 text-[10.5px]" style={{ background: "var(--reject-soft)", color: "var(--reject)" }}>blocked</span>}
              {p.id !== user?.id && (
                <Button size="sm" busy={busy === p.id} busyLabel="Unlinking…" disabled={busy !== null} onClick={() => void unlink(p)} aria-label={`Unlink ${p.username}'s Plex account`}>Unlink</Button>
              )}
            </li>
          ))}
        </ul>
      )}
      <p className="m-0 text-[11px] text-ink-faint">
        To block a Plex account from signing in, or merge a duplicate Plex sign-in into the right account, use More on their row in <Link to={LINKS.users} style={{ color: "var(--accent)" }}>Settings → People &amp; access</Link>. Your own Plex link is in the sidebar.
      </p>
    </Section>
  );
}

// PlexTautulli (admin) points at the Tautulli import, which lives with the other
// migrations in Settings → Import & migrate.
function PlexTautulli() {
  return (
    <Section id="plex-tautulli" title="Import from Tautulli" subtitle="Moving from Tautulli? Bring its watch history into Insights so the stats and graphs aren't empty on day one. The import, its past runs and Undo are in Settings → Import & migrate.">
      <div>
        <Link to={LINKS.tautulliImport} className="text-[12px] font-semibold" style={{ color: "var(--accent)" }}>Open the Tautulli import →</Link>
      </div>
    </Section>
  );
}

// PlexSignIn (admin): whether family members may sign in with Plex, and what a new Plex
// sign-in may request without asking. Each control saves itself.
function PlexSignIn() {
  const { booksEnabled } = useMe();
  const allow = useSetting("plex_login_enabled");
  const staff = useSetting("plex_signin_staff");
  const autoTypes = useSetting("plex_login_auto_approve_types", { keepOnError: false });
  return (
    <>
      <Section id="plex-sign-in" title="Plex sign-in" subtitle={<>Let your Plex Home members and shared users sign in with Plex — no accounts to hand out. They get a Requester account ({requesterPages(booksEnabled)}), and only people with access to your Plex server get in. Needs the Plex server above to be connected.</>}>
        <AutoToggle label="Allow Sign in with Plex" hint="Adds a 'Sign in with Plex' button to the login page." save={allow} />
        <div className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-2 text-[12.5px] font-semibold">Auto-approve new Plex sign-ins' requests <SavedTick save={autoTypes} /></div>
          <AutoApproveChecks
            value={typesFromCSV(autoTypes.value ?? "movie")}
            onChange={(v) => void autoTypes.commit(typesToCSV(v))}
            books={booksEnabled}
            label="New Plex sign-ins auto-approve"
          />
          <p className="m-0 text-[11px] text-ink-faint">Ticked types download straight away for someone who signs in with Plex for the first time; the rest wait for you. A series request can pull every season of a long show, so it starts with movies only. Existing accounts keep their own settings — change them in Settings → People & access.</p>
        </div>
        <AutoToggle
          label="Let staff sign in with Plex"
          hint="Sign in with Plex may open an admin or manager account linked to that Plex account. Your own first Plex sign-in then links to your admin account, if it's the only one. Off: staff sign in with their password."
          save={staff}
        />
        {staff.value && (
          <Note tone="warn">Anyone who controls a linked Plex account gets that account — for an admin, everything in Arrmada. Keep the Plex account behind two-factor sign-in at plex.tv.</Note>
        )}
      </Section>
    </>
  );
}
