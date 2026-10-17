import { useState, type FormEvent } from "react";
import { Button, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";
import type { TwoFactorSetup } from "../../lib/twoFactorApi";

// The pieces of setting up two-factor sign-in, shared by the Me page's card and the
// sign-in page (an admin the policy requires it of sets it up there): the QR code and the
// secret, the first code, and the recovery codes shown once. Its own chunk.

// QrCode draws the server's QR modules. Always dark on white with a quiet zone, whatever
// the theme: that's what phone cameras read reliably.
export function QrCode({ qr }: { qr: TwoFactorSetup["qr"] }) {
  const pad = 4;
  const box = qr.size + pad * 2;
  return (
    <svg role="img" aria-label="QR code to scan with your authenticator app" viewBox={`${-pad} ${-pad} ${box} ${box}`} width={184} height={184} shapeRendering="crispEdges" className="flex-none rounded-lg">
      <rect x={-pad} y={-pad} width={box} height={box} fill="#ffffff" />
      <path d={qr.path} fill="#000000" />
    </svg>
  );
}

// groupSecret splits the secret into fours, the way people type it into an app by hand.
export function groupSecret(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, "$1 ");
}

// EnrolStep shows the QR code and the secret and asks for the first code. confirm turns it
// on and answers the recovery codes (or throws with the reason).
export function EnrolStep({ setup, confirm, onConfirmed, onCancel }: {
  setup: TwoFactorSetup;
  confirm: (code: string) => Promise<string[]>;
  onConfirmed: (codes: string[]) => void;
  onCancel?: () => void;
}) {
  const toast = useToast();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      onConfirmed(await confirm(code.trim()));
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };
  const copy = async () => {
    try { await navigator.clipboard.writeText(setup.secret); toast("Key copied", { tone: "good" }); }
    catch { toast("Couldn’t copy — select the key and copy it yourself", { tone: "error" }); }
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3" aria-label="Set up two-factor sign-in">
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5 text-sm text-ink-dim">
        <li>Open your authenticator app (Aegis, 1Password, Google Authenticator, Authy…) and add an account.</li>
        <li>Scan the code, or type the key in.</li>
        <li>Enter the six-digit code the app shows.</li>
      </ol>
      <div className="flex flex-wrap items-center gap-4">
        <QrCode qr={setup.qr} />
        <div className="flex min-w-0 flex-1 flex-col gap-1.5">
          <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Key</span>
          <code data-testid="totp-secret" data-secret={setup.secret} className="break-all rounded-lg px-2.5 py-2 font-mono text-base" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>{groupSecret(setup.secret)}</code>
          <div><Button size="sm" variant="ghost" onClick={copy}>Copy key</Button></div>
          <span className="text-xs text-ink-faint">Time-based, six digits, every 30 seconds. This key is shown only now.</span>
        </div>
      </div>
      <Field label="Code from the app" error={error ?? undefined}>
        <Input inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" maxLength={7} required value={code} onChange={(e) => setCode(e.target.value)} placeholder="123456" className="w-40 px-3 py-2 font-mono text-md tracking-widest" />
      </Field>
      <div className="flex justify-end gap-2">
        {onCancel && <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>}
        <Button size="sm" variant="primary" type="submit" busy={busy} busyLabel="Checking…">Turn on</Button>
      </div>
    </form>
  );
}

// RecoveryCodes lists the one-time recovery codes, shown once, to copy or download.
export function RecoveryCodes({ codes, username, onDone, doneLabel = "I’ve saved them" }: { codes: string[]; username?: string; onDone: () => void; doneLabel?: string }) {
  const toast = useToast();
  const text = `Arrmada recovery codes${username ? ` for ${username}` : ""}\nEach one works once, in place of a code from your authenticator app.\n\n${codes.join("\n")}\n`;
  const copy = async () => {
    try { await navigator.clipboard.writeText(text); toast("Recovery codes copied", { tone: "good" }); }
    catch { toast("Couldn’t copy — select the codes and copy them yourself", { tone: "error" }); }
  };
  const download = () => {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "arrmada-recovery-codes.txt";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };
  return (
    <div className="flex flex-col gap-3" role="region" aria-label="Recovery codes">
      <p className="m-0 text-sm text-ink-dim">
        Keep these somewhere safe, away from your phone. If you lose the phone, each code signs you in once instead of an app code. <b>They won’t be shown again.</b>
      </p>
      <ul className="m-0 grid list-none grid-cols-2 gap-x-4 gap-y-1 rounded-lg p-3 font-mono text-md" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }} data-testid="recovery-codes">
        {codes.map((c) => <li key={c}>{c}</li>)}
      </ul>
      <div className="flex flex-wrap justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={copy}>Copy</Button>
        <Button size="sm" variant="ghost" onClick={download}>Download</Button>
        <Button size="sm" variant="primary" onClick={onDone}>{doneLabel}</Button>
      </div>
    </div>
  );
}

// WHAT_IT_COVERS is the plain answer to "does this protect X?".
export const WHAT_IT_COVERS = "It covers signing in with your email and password. Sign in with Plex or Jellyfin is checked by that server instead, API keys are separate, and audiobook apps keep using your audiobook password — they can’t ask for a code.";
