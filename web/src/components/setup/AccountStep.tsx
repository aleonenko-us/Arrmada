import { lazy, Suspense, useEffect, useState, type FormEvent } from "react";
import { api, type LoginAnswer, type Status } from "../../lib/api";
import { setupApi } from "../../lib/setupApi";
import { Button } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";
import { TabPanel, Tabs } from "../../ui/Tabs";
import { JellyfinMark, PlexMark } from "../brand/marks";

const PlexLoginButton = lazy(() => import("../PlexLoginButton"));
const JellyfinLoginSheet = lazy(() => import("../JellyfinSignInSheet"));

// The wizard's last step on a fresh install: the account, made once the providers are set
// up. It's the media server's owner signing in (only offered for a server connected in this
// wizard whose sign-in the server says is usable, which is what /status's plex_login and
// jellyfin_login are), or an email and password. Emby has no sign-in, so it's never offered.
// Either way the first admin is made here and signed in; the setup session ends with it.

export const MIN_PASSWORD = 8;

// passwordHint is the line under the password: what the server requires (8 characters),
// and how much more would help. It never blocks anything past that minimum.
export function passwordHint(pw: string): { tone: "idle" | "bad" | "ok" | "good"; text: string } {
  if (!pw) return { tone: "idle", text: `At least ${MIN_PASSWORD} characters. A few random words make a strong one.` };
  if (pw.length < MIN_PASSWORD) return { tone: "bad", text: `${MIN_PASSWORD - pw.length} more ${MIN_PASSWORD - pw.length === 1 ? "character" : "characters"} to go.` };
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length;
  if (pw.length >= 16 || (pw.length >= 12 && kinds >= 3)) return { tone: "good", text: "Strong." };
  return { tone: "ok", text: "Good enough. Longer is stronger." };
}

const HINT_COLOR = { idle: "var(--ink-faint)", bad: "var(--reject-text)", ok: "var(--avoid-text)", good: "var(--good-text)" } as const;

type Method = "media" | "password";

export default function AccountStep({ plexServer, jellyfinServer, onBack, onSignedIn }: {
  /** The connected servers' names, for the sentence above their buttons. */
  plexServer?: string;
  jellyfinServer?: string;
  onBack: () => void;
  /** The first admin is made and signed in (or has the two-factor step still to do). */
  onSignedIn: (answer: LoginAnswer) => void;
}) {
  const [status, setStatus] = useState<Status | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [method, setMethod] = useState<Method>("media");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [plexBusy, setPlexBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jellyfinOpen, setJellyfinOpen] = useState(false);

  // Read when the step opens: the family switches saved on the Media server step decide
  // whether a server's sign-in is usable.
  useEffect(() => {
    api.status().then(setStatus).catch((e: Error) => setStatusError(e.message));
  }, []);

  if (!status) {
    return <p className="m-0 text-md text-ink-dim">{statusError ?? "Loading…"}</p>;
  }

  const plex = status.plex_login;
  const jellyfin = !!status.jellyfin_login;
  const media = plex || jellyfin;
  const active: Method = media ? method : "password";
  const hint = passwordHint(password);
  const mismatch = confirm !== "" && confirm !== password;
  const tooShort = password.length < MIN_PASSWORD;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (tooShort || password !== confirm || !email.trim()) return;
    setBusy(true);
    setError(null);
    try {
      onSignedIn(await setupApi.createAccount(email.trim(), password));
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };

  const names = [plex && (plexServer ? `Plex (${plexServer})` : "Plex"), jellyfin && (jellyfinServer ? `Jellyfin (${jellyfinServer})` : "Jellyfin")].filter(Boolean).join(" and ");

  return (
    <>
      <h1 className="m-0 text-2xl font-bold">Create your account</h1>
      <p className="mb-4 mt-1 text-md text-ink-dim">
        {media
          ? "Last step. This is the admin account you'll run Arrmada with. Sign in as the owner of your media server, or use an email and password."
          : "Last step. This is the admin account you'll run Arrmada with."}
      </p>

      {error && <div role="alert" className="mb-3 rounded-lg p-2.5 text-sm" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{error}</div>}

      {media && (
        <Tabs<Method>
          idPrefix="setup-account"
          label="How to create your account"
          value={active}
          onChange={(m) => { setError(null); setMethod(m); }}
          tabs={[{ key: "media", label: "Media server" }, { key: "password", label: "Email and password" }]}
        />
      )}

      {active === "media" && media && (
        <TabPanel idPrefix="setup-account" value="media" className="flex flex-col gap-3">
          <p className="m-0 text-sm text-ink-dim">
            Sign in with {names}: whoever owns the server you just connected becomes the admin
            {jellyfin && !plex ? " (a Jellyfin administrator)" : ""}, with nothing to remember. You can add a password later in your account settings.
          </p>
          {plex && (
            <Suspense fallback={<button type="button" disabled className={SERVER_BUTTON} style={{ background: "var(--plex)", color: "var(--plex-ink)" }}><PlexMark />Sign in with Plex</button>}>
              <PlexLoginButton disabled={busy} onBusy={setPlexBusy} onError={setError} onSignedIn={onSignedIn} />
            </Suspense>
          )}
          {jellyfin && (
            <button type="button" disabled={busy || plexBusy} onClick={() => { setError(null); setJellyfinOpen(true); }} className={SERVER_BUTTON} style={{ background: "var(--jellyfin)", color: "var(--jellyfin-ink)" }}>
              <JellyfinMark />Sign in with Jellyfin
            </button>
          )}
        </TabPanel>
      )}

      {active === "password" && (
        <TabPanel idPrefix="setup-account" value="password">
          <form onSubmit={submit} className="flex flex-col gap-3">
            <Field label="Email">
              <Input type="email" required autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
            </Field>
            <Field label="Password" hint={<span aria-live="polite" style={{ color: HINT_COLOR[hint.tone] }}>{hint.text}</span>}>
              <Input type="password" required minLength={MIN_PASSWORD} autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={`${MIN_PASSWORD}+ characters`} />
            </Field>
            <Field label="Confirm password" error={mismatch ? "The two passwords don't match." : undefined}>
              <Input type="password" required autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
            </Field>
            <Button type="submit" variant="primary" busy={busy} busyLabel="Creating…" disabled={tooShort || mismatch || confirm === "" || !email.trim() || plexBusy}>
              Create account and open Arrmada
            </Button>
          </form>
        </TabPanel>
      )}

      <div className="mt-5 flex items-center justify-between gap-2">
        <Button variant="ghost" onClick={onBack} disabled={busy || plexBusy}>Back</Button>
        <span className="text-xs text-ink-faint">Setup ends here. Nothing is made until you do this.</span>
      </div>

      {jellyfinOpen && (
        <Suspense fallback={null}>
          <JellyfinLoginSheet quickConnect={!!status.jellyfin_quickconnect} onClose={() => setJellyfinOpen(false)} onSignedIn={(r) => { setJellyfinOpen(false); onSignedIn(r); }} />
        </Suspense>
      )}
    </>
  );
}

// The media server buttons: the server's mark, then its name, on its brand fill (the login
// page's own look).
const SERVER_BUTTON = "flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-md font-semibold";
