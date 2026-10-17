import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button, useConfirm, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";
import { accountApi } from "../../lib/accountApi";
import { useMe } from "../../lib/me";
import { twoFactorApi, type TwoFactorSetup, type TwoFactorState } from "../../lib/twoFactorApi";
import { EnrolStep, RecoveryCodes, WHAT_IT_COVERS } from "./parts";

// Two-factor sign-in on the Me page (every role): set it up with an authenticator app, see
// how many recovery codes are left, make new ones, or turn it off. Its own chunk, loaded
// with the Account card.
//
// Setting up asks for the password first; turning it off or making new recovery codes
// asks for the password and a current code. Signed-in devices stay signed in when it's
// turned on, so the card offers to sign the others out.

type View =
  | { kind: "idle" }
  | { kind: "start" } // asking for the password to begin
  | { kind: "enrol"; setup: TwoFactorSetup }
  | { kind: "codes"; codes: string[]; fresh: boolean } // fresh: just turned on
  | { kind: "manage"; action: "disable" | "codes" };

export function TwoFactorSettings() {
  const { user } = useMe();
  const toast = useToast();
  const confirm = useConfirm();
  const [state, setState] = useState<TwoFactorState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [view, setView] = useState<View>({ kind: "idle" });

  const load = useCallback(() => {
    twoFactorApi.state().then((s) => { setState(s); setError(null); }).catch((e) => setError((e as Error).message));
  }, []);
  useEffect(load, [load]);

  const signOutOthers = async () => {
    const ok = await confirm({
      title: "Sign out your other devices?",
      body: "They signed in before two-factor was on. Signing them out means each one needs your password and a code next time. This one stays signed in.",
      confirmLabel: "Sign out others",
      tone: "danger",
    });
    if (!ok) return;
    try {
      const r = await accountApi.signOutOthers();
      toast(r.signed_out > 0 ? `Signed out of ${r.signed_out} other ${r.signed_out === 1 ? "device" : "devices"}` : "No other devices were signed in", { tone: "good" });
    } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };

  if (error) return <div role="alert" className="px-3.5 py-3 text-sm font-medium" style={{ color: "var(--reject-text)" }}>Couldn’t load two-factor sign-in — {error}</div>;
  if (!state) return <div className="px-3.5 py-3 text-sm text-ink-faint">Loading…</div>;

  const body = (() => {
    switch (view.kind) {
      case "start":
        return <PasswordStep onCancel={() => setView({ kind: "idle" })} onSetup={(setup) => setView({ kind: "enrol", setup })} />;
      case "enrol":
        return (
          <EnrolStep
            setup={view.setup}
            confirm={(code) => twoFactorApi.confirm(code).then((r) => r.recovery_codes)}
            onConfirmed={(codes) => { setView({ kind: "codes", codes, fresh: true }); load(); }}
            onCancel={() => setView({ kind: "idle" })}
          />
        );
      case "codes":
        return (
          <div className="flex flex-col gap-3">
            {view.fresh && <div role="status" className="text-sm font-semibold" style={{ color: "var(--good-text)" }}>Two-factor sign-in is on.</div>}
            <RecoveryCodes codes={view.codes} username={user?.username} onDone={() => setView({ kind: "idle" })} />
            {view.fresh && (
              <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3 text-sm text-ink-dim" style={{ borderColor: "var(--line-soft)" }}>
                <span className="min-w-0 flex-1">Devices already signed in stay signed in.</span>
                <Button size="sm" onClick={signOutOthers}>Sign out other devices</Button>
              </div>
            )}
          </div>
        );
      case "manage":
        return (
          <ManageStep
            action={view.action}
            onCancel={() => setView({ kind: "idle" })}
            onDone={(codes) => {
              load();
              if (codes) setView({ kind: "codes", codes, fresh: false });
              else { setView({ kind: "idle" }); toast("Two-factor sign-in is off", { tone: "good" }); }
            }}
          />
        );
    }
    return null;
  })();

  return (
    <div className="px-3.5 py-3" style={{ borderTop: "1px solid var(--line-soft)" }} id="two-factor">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h3 className="m-0 flex items-center gap-2 text-md font-semibold">
            Two-factor sign-in
            <span className="rounded-full px-1.5 py-px text-xs font-semibold" style={state.enabled ? { background: "var(--good-soft)", color: "var(--good-text)" } : { background: "var(--panel-2)", color: "var(--ink-faint)" }}>
              {state.enabled ? "On" : "Off"}
            </span>
          </h3>
          <div className="text-sm text-ink-dim">
            {state.enabled
              ? `A code from your authenticator app after your password. ${state.recovery_codes_left} of 10 recovery codes left.`
              : state.password_set
                ? "Ask for a code from an authenticator app after your password."
                : "You sign in with Plex or Jellyfin, which check who you are themselves. Set a password above to use two-factor with it."}
          </div>
        </div>
        {view.kind === "idle" && !state.enabled && state.password_set && <Button size="sm" onClick={() => setView({ kind: "start" })}>Set up</Button>}
      </div>
      {state.required && !state.enabled && view.kind === "idle" && (
        <div role="status" className="mt-2 rounded-lg px-3 py-2 text-sm" style={{ background: "var(--avoid-soft)", color: "var(--avoid-text)" }}>
          Admins on this server must use two-factor sign-in. If you don’t set it up here, you’ll be asked to the next time you sign in with your password.
        </div>
      )}
      {view.kind === "idle" && state.enabled && (
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setView({ kind: "manage", action: "codes" })}>New recovery codes</Button>
          <Button size="sm" variant="ghost" style={{ color: "var(--reject-text)" }} onClick={() => setView({ kind: "manage", action: "disable" })}>Turn off</Button>
        </div>
      )}
      {body && <div className="mt-3">{body}</div>}
      <p className="m-0 mt-2 text-xs text-ink-faint">{WHAT_IT_COVERS}</p>
    </div>
  );
}

// PasswordStep asks for the password before a new secret is made.
function PasswordStep({ onSetup, onCancel }: { onSetup: (s: TwoFactorSetup) => void; onCancel: () => void }) {
  const { user } = useMe();
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try { onSetup(await twoFactorApi.setup(password)); }
    catch (err) { setError((err as Error).message); setBusy(false); }
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2" aria-label="Confirm your password">
      <input type="text" autoComplete="username" value={user?.username ?? ""} readOnly hidden />
      <Field label="Your password" error={error ?? undefined}>
        {/* The step opens on a click of Set up, so the cursor goes where the password is typed. */}
        {/* eslint-disable-next-line jsx-a11y/no-autofocus */}
        <Input type="password" autoComplete="current-password" autoFocus required value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
        <Button size="sm" variant="primary" type="submit" busy={busy} busyLabel="Checking…">Continue</Button>
      </div>
    </form>
  );
}

// ManageStep is turning it off, or making new recovery codes: the password and a current
// code (or a recovery code) both.
function ManageStep({ action, onDone, onCancel }: { action: "disable" | "codes"; onDone: (codes?: string[]) => void; onCancel: () => void }) {
  const { user } = useMe();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      if (action === "disable") { await twoFactorApi.disable(password, code.trim()); onDone(); }
      else onDone((await twoFactorApi.newRecoveryCodes(password, code.trim())).recovery_codes);
    } catch (err) { setError((err as Error).message); setBusy(false); }
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-2" aria-label={action === "disable" ? "Turn off two-factor sign-in" : "Make new recovery codes"}>
      <p className="m-0 text-sm text-ink-dim">
        {action === "disable"
          ? "Your password will be enough to sign in again. Your recovery codes and trusted devices are forgotten."
          : "The old recovery codes stop working, used or not."}
      </p>
      <input type="text" autoComplete="username" value={user?.username ?? ""} readOnly hidden />
      <Field label="Your password">
        <Input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
      </Field>
      <Field label="Code from the app, or a recovery code" error={error ?? undefined}>
        <Input autoComplete="one-time-code" required value={code} onChange={(e) => setCode(e.target.value)} className="w-48 px-3 py-2 font-mono text-md" />
      </Field>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
        <Button size="sm" variant={action === "disable" ? "danger" : "primary"} type="submit" busy={busy} busyLabel="Checking…">
          {action === "disable" ? "Turn off" : "Make new codes"}
        </Button>
      </div>
    </form>
  );
}
