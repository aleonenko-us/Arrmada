import { lazy, Suspense, useEffect, useState } from "react";
import { FleetMark } from "../components/FleetMark";
import { api, resetSignedOut, type AuthUser } from "../lib/api";
import { nextAfterSignIn } from "../lib/session";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";

const PlexLoginButton = lazy(() => import("../components/PlexLoginButton"));

// After any successful sign-in: back to the page they were on (or ?next=), else their home —
// the Dashboard for staff, Discover for everyone else.
// A full load, so MeProvider and /status start fresh for the new session.
function continueAfterSignIn(user?: AuthUser | null) {
  resetSignedOut();
  window.location.replace(nextAfterSignIn(user?.role));
}

// Login is shown when auth is enabled and no session is active. It doubles as the
// first-run setup screen (create the first admin) when the instance has no users yet.
// signedOut: the session ended while the app was open (expired, revoked, password changed).
export function Login({ signedOut = false }: { signedOut?: boolean }) {
  const [mode, setMode] = useState<"loading" | "login" | "setup">("loading");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [plexEnabled, setPlexEnabled] = useState(false);
  const [plexBusy, setPlexBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.status().then((s) => { setMode(s.needs_setup ? "setup" : "login"); setPlexEnabled(s.plex_login); }).catch(() => setMode("login"));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      // A new install's first admin lands on "/", where the setup wizard opens.
      const { user } = mode === "setup" ? await api.setupAdmin(email.trim(), password) : await api.login(email.trim(), password);
      continueAfterSignIn(user);
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };

  const setup = mode === "setup";

  return (
    <div className="grid h-full place-items-center px-5" style={{ background: "var(--bg)" }}>
      <div className="w-full max-w-[380px]">
        <div className="mb-6 flex flex-col items-center gap-2.5 text-center">
          <span className="grid h-12 w-12 place-items-center rounded-2xl" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
            <FleetMark className="h-6 w-6" />
          </span>
          <h1 className="m-0 text-2xl font-extrabold tracking-[0.14em]">ARRMADA</h1>
          <p className="m-0 text-base text-ink-dim">{mode === "loading" ? "…" : setup ? "Create your admin account to get started." : "Sign in to discover and request."}</p>
        </div>

        {mode !== "loading" && (
          <form onSubmit={submit} className="flex flex-col gap-3 rounded-2xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)", boxShadow: "var(--shadow)" }}>
            {signedOut && !setup && !error && (
              <div role="status" className="rounded-lg px-3 py-2.5 text-sm text-ink-dim" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
                You were signed out. Sign in again to carry on.
              </div>
            )}
            <Field label="Email">
              {/* The page is only this form, so the cursor starts where the first thing is typed. */}
              {/* eslint-disable-next-line jsx-a11y/no-autofocus */}
              <Input type="email" required autoFocus autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" className={BOX} />
            </Field>
            <Field label="Password">
              <Input type="password" required minLength={setup ? 8 : undefined} autoComplete={setup ? "new-password" : "current-password"} value={password} onChange={(e) => setPassword(e.target.value)} placeholder={setup ? "8+ characters" : "••••••••"} className={BOX} />
            </Field>
            {error && <div className="text-sm" style={{ color: "var(--reject)" }}>{error}</div>}
            <button type="submit" disabled={busy || plexBusy} className="mt-1 rounded-lg px-4 py-2.5 text-md font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
              {busy ? "…" : setup ? "Create admin & continue" : "Sign in"}
            </button>
            {setup && (
              <p className="m-0 mt-1 text-xs leading-snug text-ink-faint">
                Next, a short setup asks for your free <b>TMDB</b> key and where your media lives. You can skip it and do it later in Settings.
              </p>
            )}

            {!setup && plexEnabled && (
              <>
                <div className="my-0.5 flex items-center gap-2 text-xs uppercase tracking-wide text-ink-faint">
                  <span className="h-px flex-1" style={{ background: "var(--line)" }} /> or <span className="h-px flex-1" style={{ background: "var(--line)" }} />
                </div>
                {/* Sign in with Plex: the server checks the Plex account can reach this
                    server and signs it into its requester account. */}
                <Suspense fallback={<button type="button" disabled className="rounded-lg px-4 py-2.5 text-md font-semibold" style={{ background: "#e5a00d", color: "#1f1300" }}>Sign in with Plex</button>}>
                  <PlexLoginButton disabled={busy} onBusy={setPlexBusy} onError={setError} onSignedIn={continueAfterSignIn} />
                </Suspense>
              </>
            )}
          </form>
        )}
      </div>
    </div>
  );
}

const BOX = "w-full px-3 py-2.5 text-md";
