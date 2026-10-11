import { useEffect, useState } from "react";
import { Link, Navigate, useSearchParams } from "react-router-dom";
import { AutoToggle, SavedTick } from "../../components/settings/auto";
import { Note, Section } from "../../components/settings/ui";
import { api, type PlexConfig, type PlexStatus } from "../../lib/api";
import { LINKS } from "../../lib/links";
import { isAdmin, useMe } from "../../lib/me";
import { ago } from "../../lib/taskTime";
import { useSetting } from "../../lib/useSettings";
import { PlexLibraryUpdates } from "./PlexLibraryUpdates";
import { requesterPages } from "./roles";
import { AutoApproveChecks, typesFromCSV, typesToCSV } from "./People";

// Settings → Plex: how Arrmada uses the Plex server — which server it is, the scans Arrmada
// sends after it changes a file, and (admins) who may sign in with Plex. The connection
// itself (Sign in with Plex, address and token, Test) is the Plex card on Settings →
// Connections, next to every other integration; this page links there.
export function PlexSettings() {
  const { user } = useMe();
  const admin = isAdmin(user);
  // plex.tv used to send a redirect-mode "Sign in with Plex" back here. One that was under
  // way across an update still finishes: the connection editor picks the PIN up.
  const [params] = useSearchParams();
  const pin = params.get("plexpin");
  if (pin) return <Navigate to={`/settings/connections?plexpin=${encodeURIComponent(pin)}`} replace />;
  return (
    <div className="flex flex-col gap-6">
      <PlexServer />
      {/* Path mapping and the last scan; its own load and Save. */}
      <PlexLibraryUpdates />
      {admin && <PlexSignIn />}
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
function PlexServer() {
  const [cfg, setCfg] = useState<PlexConfig | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => { api.insightsConfig().then(setCfg).catch((e: Error) => setErr(e.message)); }, []);
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
