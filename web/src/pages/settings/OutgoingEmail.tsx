import { useEffect, useState } from "react";
import { Note, Section, input, inputStyle, useScrollWhenLoaded } from "../../components/settings/ui";
import { Field } from "../../ui/Field";
import { DEFAULT_PORT, mailApi, type MailSecurity, type SMTPView } from "../../lib/mailApi";
import { useQuery, setQueryData } from "../../lib/query";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { Button, useConfirm, useToast } from "../../ui";

// Settings → Alerts → Outgoing email: the mail account Arrmada sends from (Send to Kindle
// today). Admin-only. The server, the login and the password are credentials: they're
// typed here and never shown again — the card says they're saved, with a short hint, and a
// blank field keeps what's saved. So unlike the auto-saving cards, this one has a Save.

const KEY = "mail:smtp";

const SECURITY: { value: MailSecurity; label: string }[] = [
  { value: "starttls", label: "STARTTLS (port 587)" },
  { value: "tls", label: "TLS (port 465)" },
  { value: "none", label: "None (a relay on your network only)" },
];

export function OutgoingEmail() {
  const { data, error } = useQuery(KEY, mailApi.get);
  useScrollWhenLoaded("outgoing-email", !!data);
  return (
    <Section
      id="outgoing-email"
      title="Outgoing email"
      subtitle="The mail account Arrmada sends from — used for Send to Kindle. Use your own provider's SMTP details with an app password, not your normal password."
    >
      {error && !data ? (
        <p className="text-[12px]" style={{ color: "var(--reject)" }}>Couldn’t load the outgoing email settings — {error.message}</p>
      ) : !data ? (
        <p className="text-[12px] text-ink-dim">Loading…</p>
      ) : (
        <MailForm view={data} />
      )}
    </Section>
  );
}

function MailForm({ view }: { view: SMTPView }) {
  const toast = useToast();
  const confirm = useConfirm();
  const [host, setHost] = useState("");
  const [security, setSecurity] = useState<MailSecurity>(view.security);
  const [port, setPort] = useState(String(view.port || DEFAULT_PORT[view.security]));
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [from, setFrom] = useState(view.from);
  const [busy, setBusy] = useState(false);
  const [testTo, setTestTo] = useState("");
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; detail: string } | null>(null);

  // A reload (another tab saved) brings the plain fields along; credentials stay blank.
  useEffect(() => { setSecurity(view.security); setPort(String(view.port || DEFAULT_PORT[view.security])); setFrom(view.from); }, [view.security, view.port, view.from]);

  const dirty = host.trim() !== "" || username.trim() !== "" || password !== "" || security !== view.security
    || Number(port) !== (view.port || DEFAULT_PORT[view.security]) || from !== view.from;
  useUnsavedGuard(dirty);

  const pickSecurity = (s: MailSecurity) => {
    // Moving between the usual ports follows the choice; a custom port is left alone.
    if (Object.values(DEFAULT_PORT).includes(Number(port))) setPort(String(DEFAULT_PORT[s]));
    setSecurity(s);
  };

  const save = async () => {
    setBusy(true);
    try {
      const v = await mailApi.save({
        host: host.trim() || undefined, security, port: Number(port) || 0,
        username: username.trim() || undefined, password: password || undefined, from: from.trim(),
      });
      setQueryData(KEY, v);
      setHost(""); setUsername(""); setPassword("");
      toast("Outgoing email saved", { tone: "good" });
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };

  const removeLogin = async () => {
    const ok = await confirm({ title: "Send without signing in?", body: "The saved username and password are forgotten. Only a relay on your own network takes mail that way.", confirmLabel: "Remove login", tone: "danger" });
    if (!ok) return;
    try { setQueryData(KEY, await mailApi.save({ remove_login: true })); } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };

  const removeAll = async () => {
    const ok = await confirm({ title: "Remove outgoing email?", body: "The server, login and password are forgotten, and Send to Kindle stops until it's set up again.", confirmLabel: "Remove", tone: "danger" });
    if (!ok) return;
    try { setQueryData(KEY, await mailApi.clear()); setResult(null); } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };

  const test = async () => {
    setTesting(true);
    setResult(null);
    try { setResult(await mailApi.test(testTo.trim())); } catch (e) { setResult({ ok: false, detail: (e as Error).message }); } finally { setTesting(false); }
  };

  const keep = (set: boolean, hint?: string) => (set ? `Saved${hint ? ` (${hint})` : ""} — leave blank to keep` : "");

  return (
    <>
      <p className="m-0 text-[12px]" style={{ color: view.configured ? "var(--good-text)" : "var(--ink-dim)" }}>
        {view.configured ? `Set up — mail goes out from ${view.from}.` : "Not set up yet."}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Mail server">
          <input className={input} style={inputStyle} value={host} onChange={(e) => setHost(e.target.value)}
            placeholder={keep(view.host_set, view.host_hint) || "smtp.gmail.com"} autoComplete="off" spellCheck={false} />
        </Field>
        <div className="grid grid-cols-[1fr_96px] gap-2">
          <Field label="Security">
            <select className={input} style={inputStyle} value={security} onChange={(e) => pickSecurity(e.target.value as MailSecurity)}>
              {SECURITY.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
          </Field>
          <Field label="Port">
            <input className={input} style={inputStyle} inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))} />
          </Field>
        </div>
        <Field label="Username">
          <input className={input} style={inputStyle} value={username} onChange={(e) => setUsername(e.target.value)}
            placeholder={keep(view.username_set, view.username_hint) || "you@example.com"} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label="Password">
          <input className={input} style={inputStyle} type="password" value={password} onChange={(e) => setPassword(e.target.value)}
            placeholder={keep(view.password_set) || "An app password"} autoComplete="new-password" />
        </Field>
        <Field label="From address">
          <input className={input} style={inputStyle} value={from} onChange={(e) => setFrom(e.target.value)} placeholder="Arrmada <you@example.com>" spellCheck={false} />
        </Field>
      </div>
      <Note tone="info">
        Most providers want an <b>app password</b> for this (Gmail, iCloud, Fastmail, Outlook all offer one in your account’s security settings).
        Send from your own address so mail isn’t marked as spam. Kindle owners add the From address to Amazon’s approved senders.
      </Note>
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" onClick={save} busy={busy} busyLabel="Saving…">Save</Button>
        {view.username_set && <Button variant="ghost" onClick={removeLogin}>Remove login</Button>}
        {(view.host_set || view.configured) && <Button variant="ghost" onClick={removeAll}>Remove</Button>}
      </div>
      {view.configured && (
        <div className="flex flex-col gap-2 border-t pt-3" style={{ borderColor: "var(--line)" }}>
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex min-w-[220px] flex-1 flex-col gap-1.5">
              <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">Send a test to</span>
              <input className={input} style={inputStyle} type="email" value={testTo} onChange={(e) => setTestTo(e.target.value)} placeholder="you@example.com" />
            </label>
            <Button onClick={test} busy={testing} busyLabel="Sending…" disabled={!testTo.trim()}>Send test email</Button>
          </div>
          {result && (
            <p className="m-0 break-words text-[12px]" role="status" style={{ color: result.ok ? "var(--good-text)" : "var(--reject)" }}>
              {result.ok ? result.detail : `Didn’t send: ${result.detail}`}
            </p>
          )}
        </div>
      )}
    </>
  );
}
