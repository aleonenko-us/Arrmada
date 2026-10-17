import { useEffect, useRef, useState, type FormEvent } from "react";
import { ApiError, type AuthUser } from "../../lib/api";
import { twoFactorApi, type TwoFactorSetup } from "../../lib/twoFactorApi";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";
import { EnrolStep, RecoveryCodes } from "./parts";

// The sign-in page's second step, after a right password on an account with two-factor
// sign-in on ("code"), or for an admin the server requires it of who hasn't set it up
// ("setup"). The ticket from the password step is all it holds: it is not a session, it
// lasts five minutes, and when it's gone the person starts again from the password. Its
// own chunk, downloaded only when a sign-in needs it.

export default function TwoFactorLogin({ step, ticket, username, onSignedIn, onRestart }: {
  step: "code" | "setup";
  ticket: string;
  username: string;
  onSignedIn: (user: AuthUser) => void;
  /** The ticket can't be used any more: back to the password, with why. */
  onRestart: (why: string) => void;
}) {
  // The server marks a ticket that can't be used any more (expired, spent, or the
  // password changed meanwhile): that goes back to the password; anything else (a wrong
  // code, too many tries) is said here.
  const fail = (err: unknown, setError: (m: string) => void) => {
    if (ticketGone(err)) onRestart((err as Error).message);
    else setError((err as Error).message);
  };
  return step === "code"
    ? <CodeStep ticket={ticket} onSignedIn={onSignedIn} onRestart={onRestart} fail={fail} />
    : <SetupStep ticket={ticket} username={username} onSignedIn={onSignedIn} onRestart={onRestart} fail={fail} />;
}

function ticketGone(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401 && err.body?.reason === "ticket";
}

type StepProps = {
  ticket: string;
  onSignedIn: (user: AuthUser) => void;
  onRestart: (why: string) => void;
  fail: (err: unknown, setError: (m: string) => void) => void;
};

function CodeStep({ ticket, onSignedIn, onRestart, fail }: StepProps) {
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState("");
  const [trust, setTrust] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      const { user } = await twoFactorApi.loginCode(ticket, code.trim(), trust);
      onSignedIn(user);
    } catch (err) {
      fail(err, setError);
      setBusy(false);
    }
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3" aria-label="Two-factor sign-in">
      <div>
        <h2 className="m-0 text-md font-bold">Two-factor sign-in</h2>
        <p className="m-0 mt-0.5 text-sm text-ink-dim">
          {recovery ? "Enter one of the recovery codes you saved. Each works once." : "Enter the six-digit code from your authenticator app."}
        </p>
      </div>
      <Field label={recovery ? "Recovery code" : "Code"} error={error ?? undefined}>
        {/* The step replaces the password form, so the cursor goes where the code is typed. */}
        {/* eslint-disable-next-line jsx-a11y/no-autofocus */}
        <Input key={recovery ? "recovery" : "code"} autoFocus required inputMode={recovery ? "text" : "numeric"} autoComplete="one-time-code" maxLength={recovery ? 16 : 7}
          value={code} onChange={(e) => setCode(e.target.value)} placeholder={recovery ? "abcde-fghjk" : "123456"} className="w-full px-3 py-2.5 font-mono text-md tracking-widest" />
      </Field>
      <label className="flex items-center gap-2 text-sm text-ink-dim">
        <input type="checkbox" checked={trust} onChange={(e) => setTrust(e.target.checked)} />
        Trust this device for 30 days
      </label>
      <button type="submit" disabled={busy} className="rounded-lg px-4 py-2.5 text-md font-semibold" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>
        {busy ? "…" : "Verify"}
      </button>
      <div className="flex flex-wrap justify-between gap-2 text-sm">
        <button type="button" className="font-semibold" style={{ color: "var(--accent-text)" }} onClick={() => { setRecovery((r) => !r); setCode(""); setError(null); }}>
          {recovery ? "Use a code from the app" : "Use a recovery code"}
        </button>
        <button type="button" className="text-ink-dim" onClick={() => onRestart("")}>Back</button>
      </div>
      {recovery && <p className="m-0 text-xs text-ink-faint">No codes either? An admin can turn two-factor off for you in People &amp; access.</p>}
    </form>
  );
}

// SetupStep: an admin the server requires two-factor of sets it up before any session
// exists, then sees the recovery codes once, then carries on into the app.
function SetupStep({ ticket, username, onSignedIn, onRestart, fail }: StepProps & { username: string }) {
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ user: AuthUser; codes: string[] } | null>(null);
  // One secret per ticket: asking twice would replace the one on screen, so a re-run of
  // the effect (a new fail from the parent) asks nothing.
  const asked = useRef("");
  useEffect(() => {
    if (asked.current === ticket) return;
    asked.current = ticket;
    twoFactorApi.loginSetup(ticket).then(setSetup).catch((err) => fail(err, setError));
  }, [ticket, fail]);

  if (done) {
    return (
      <div className="flex flex-col gap-3">
        <h2 className="m-0 text-md font-bold">Two-factor sign-in is on</h2>
        <RecoveryCodes codes={done.codes} username={username} onDone={() => onSignedIn(done.user)} doneLabel="Saved — continue" />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div>
        <h2 className="m-0 text-md font-bold">Set up two-factor sign-in</h2>
        <p className="m-0 mt-0.5 text-sm text-ink-dim">Admins on this server must use a code from an authenticator app after their password. Set it up to carry on.</p>
      </div>
      {error && <div role="alert" className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
      {!setup && !error && <div className="text-sm text-ink-faint">Loading…</div>}
      {setup && (
        <EnrolStep
          setup={setup}
          confirm={async (code) => {
            const r = await twoFactorApi.loginSetupConfirm(ticket, code).catch((err) => {
              if (ticketGone(err)) onRestart((err as Error).message);
              throw err;
            });
            setDone({ user: r.user, codes: r.recovery_codes });
            return r.recovery_codes;
          }}
          onConfirmed={() => {}}
          onCancel={() => onRestart("")}
        />
      )}
    </div>
  );
}
