import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button, useConfirm, useToast } from "../ui";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { apiKeysApi, expiryLabel, SCOPE_LABEL, type KeyScope, type MyApiKeys, type PersonalApiKey } from "../lib/apiKeysApi";
import { formatAgo } from "../lib/format";

// Personal API keys: the Me page's card (every role) and the admin's list in People &
// access. A key lets a script or app (n8n, Home Assistant, curl) call Arrmada as you,
// never above the role you had when you made it, and a read-only key only looks things
// up. The key is shown once, when it's made: the server keeps only a fingerprint of it.
// This file loads with the Me page and People & access, never with a requester's first
// load.

const ago = (iso: string | null | undefined) => {
  const ms = iso ? Date.parse(iso) : NaN;
  return ms > 0 ? formatAgo(ms) : "";
};

function networkLabel(n?: string): string {
  if (n === "local") return " from your home network";
  return n ? ` from ${n}` : "";
}

const EXPIRY_LABEL = (d: number) => (d === 0 ? "Never" : d === 365 ? "In a year" : `In ${d} days`);

// PersonalApiKeys is the Me page's card: your keys, a form to make one, and the new key
// shown once with a copy button.
export function PersonalApiKeys() {
  const toast = useToast();
  const confirm = useConfirm();
  const [state, setState] = useState<MyApiKeys | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [made, setMade] = useState<{ key: string; name: string } | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const load = useCallback(() => {
    apiKeysApi.mine().then((s) => { setState(s); setError(null); }).catch((e: Error) => setError(e.message));
  }, []);
  useEffect(load, [load]);

  const revoke = async (k: PersonalApiKey) => {
    const ok = await confirm({
      title: `Revoke “${k.name}”?`,
      body: "Anything using this key stops working straight away. This can’t be undone — you can make a new key any time.",
      confirmLabel: "Revoke key",
      tone: "danger",
    });
    if (!ok) return;
    setBusy(k.id);
    try {
      await apiKeysApi.revoke(k.id);
      setState((s) => s && { ...s, keys: s.keys.filter((x) => x.id !== k.id) });
      toast(`Revoked ${k.name}`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
      load();
    } finally {
      setBusy(null);
    }
  };

  if (error && !state) return <div role="alert" className="px-3.5 py-3 text-sm font-medium" style={{ color: "var(--reject-text)" }}>Couldn’t load your API keys — {error}</div>;
  if (!state) return <div className="px-3.5 py-3 text-sm text-ink-faint">Loading…</div>;

  const live = state.keys.filter((k) => !k.expired).length;
  return (
    <div className="flex flex-col">
      <div className="px-3.5 pt-3 text-sm text-ink-dim">
        Let a script or app — n8n, Home Assistant, curl — use Arrmada as you. Send the key in an <code className="font-mono">X-Api-Key</code> header. Anyone with a key can do what it allows, so treat it like a password.
      </div>

      {made && <NewKeyShown made={made} onDone={() => setMade(null)} />}

      {state.keys.length > 0 && (
        <ul aria-label="Your API keys" className="m-0 mt-2 list-none p-0">
          {state.keys.map((k) => (
            <li key={k.id} className="flex min-h-[52px] items-center justify-between gap-3 px-3.5 py-2 [&+&]:border-t [&+&]:border-[var(--line-soft)]">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2 text-base font-medium">
                  <span className="truncate">{k.name}</span>
                  <span className="flex-none rounded-full px-1.5 py-px text-xs font-semibold" style={{ background: k.scope === "full" ? "var(--accent-soft)" : "var(--panel-2)", color: k.scope === "full" ? "var(--accent-text)" : "var(--ink-dim)" }}>{SCOPE_LABEL[k.scope]}</span>
                  {k.expired && <span className="flex-none rounded-full px-1.5 py-px text-xs font-semibold" style={{ background: "var(--panel-2)", color: "var(--reject-text)" }}>Expired</span>}
                </div>
                <div className="text-xs text-ink-faint">
                  <span className="font-mono">{k.hint}</span> · Made {ago(k.created_at) || "a while ago"} · {k.last_used_at ? `Last used ${ago(k.last_used_at)}${networkLabel(k.last_used_network)}` : "Never used"}{k.expired ? "" : ` · ${expiryLabel(k)}`}
                </div>
              </div>
              <Button size="sm" variant="ghost" onClick={() => revoke(k)} busy={busy === k.id} busyLabel="…" aria-label={`Revoke ${k.name}`}>{k.expired ? "Remove" : "Revoke"}</Button>
            </li>
          ))}
        </ul>
      )}

      <div className="px-3.5 py-3">
        {creating ? (
          <CreateKeyForm
            state={state}
            onCancel={() => setCreating(false)}
            onMade={(key, k) => {
              setCreating(false);
              setMade({ key, name: k.name });
              setState((s) => s && { ...s, keys: [k, ...s.keys] });
            }}
          />
        ) : live >= state.max ? (
          <p className="m-0 text-sm text-ink-faint">You have the most keys one account can hold ({state.max}). Revoke one you no longer use to make another.</p>
        ) : (
          <Button size="sm" onClick={() => { setMade(null); setCreating(true); }}>New API key</Button>
        )}
      </div>
    </div>
  );
}

// NewKeyShown is the key, just made: the only time it's ever on screen.
function NewKeyShown({ made, onDone }: { made: { key: string; name: string }; onDone: () => void }) {
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(made.key);
      setCopied(true);
      toast("Key copied", { tone: "good" });
    } catch {
      toast("Couldn’t copy — select the key and copy it yourself", { tone: "error" });
    }
  };
  return (
    <div role="status" className="mx-3.5 mt-3 flex flex-col gap-2 rounded-lg p-3" style={{ background: "var(--accent-soft)", border: "1px solid var(--accent-line)" }}>
      <div className="text-md font-semibold">Your new key “{made.name}”</div>
      <Field label="API key" hint="Copy it now: this is the only time it’s shown. If you lose it, revoke it and make another.">
        <Input readOnly value={made.key} onFocus={(e) => e.currentTarget.select()} className="w-full px-3 py-2 font-mono text-sm" />
      </Field>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="primary" onClick={copy}>{copied ? "Copied" : "Copy key"}</Button>
        <Button size="sm" variant="ghost" onClick={onDone}>Done</Button>
      </div>
    </div>
  );
}

// CreateKeyForm names the key, says what it may do and for how long, and — when the
// account has a password — asks for it, so a browser left signed in isn't enough to make
// a lasting way in.
function CreateKeyForm({ state, onCancel, onMade }: { state: MyApiKeys; onCancel: () => void; onMade: (key: string, k: PersonalApiKey) => void }) {
  const [name, setName] = useState("");
  const [scope, setScope] = useState<KeyScope>("read");
  const [days, setDays] = useState(state.expiry_days.includes(90) ? 90 : 0);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) { setError("Give the key a name, like “n8n” or “Home Assistant”."); return; }
    setBusy(true); setError(null);
    try {
      const r = await apiKeysApi.create({ name: name.trim(), scope, expires_in_days: days, ...(state.password_required ? { password } : {}) });
      onMade(r.key, r.api_key);
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3" aria-label="New API key">
      <Field label="Name" hint="What it’s for, so you can tell your keys apart.">
        <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder="n8n" autoComplete="off" required />
      </Field>
      <Field label="Access" hint={scope === "read" ? "Can look things up and read your requests, never change anything." : "Can do what you can, such as requesting titles, on the routes keys may use."}>
        <Select value={scope} onChange={(e) => setScope(e.target.value as KeyScope)}>
          <option value="read">Read only</option>
          <option value="full">Full</option>
        </Select>
      </Field>
      <Field label="Expires">
        <Select value={String(days)} onChange={(e) => setDays(Number(e.target.value))}>
          {state.expiry_days.map((d) => <option key={d} value={d}>{EXPIRY_LABEL(d)}</option>)}
        </Select>
      </Field>
      {state.password_required && (
        <Field label="Your password" hint="To make sure it’s you.">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
        </Field>
      )}
      {error && <div role="alert" className="text-sm font-medium" style={{ color: "var(--reject-text)" }}>{error}</div>}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
        <Button size="sm" variant="primary" type="submit" busy={busy} busyLabel="Making…">Make key</Button>
      </div>
    </form>
  );
}

// AllPersonalApiKeys is the admin's list in People & access: everyone's keys, whose and
// when they were last used (never from where — that's the owner's), each revocable.
export function AllPersonalApiKeys() {
  const toast = useToast();
  const confirm = useConfirm();
  const [keys, setKeys] = useState<PersonalApiKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  useEffect(() => {
    apiKeysApi.all().then((k) => { setKeys(k); setError(null); }).catch((e: Error) => setError(e.message));
  }, []);

  const revoke = async (k: PersonalApiKey) => {
    const ok = await confirm({
      title: k.username ? `Revoke ${k.username}’s key “${k.name}”?` : `Revoke the key “${k.name}”?`,
      body: "Anything using it stops working straight away. They can make a new one on their Me page.",
      confirmLabel: "Revoke key",
      tone: "danger",
    });
    if (!ok) return;
    setBusy(k.id);
    try {
      await apiKeysApi.revokeAny(k.id);
      setKeys((ks) => ks && ks.filter((x) => x.id !== k.id));
      toast(k.username ? `Revoked ${k.username}’s key` : "Key revoked", { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
    }
  };

  if (error) return <div role="alert" className="text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>;
  if (!keys) return <p className="m-0 text-sm text-ink-dim">Loading…</p>;
  if (keys.length === 0) return <p className="m-0 text-sm text-ink-dim">Nobody has made one yet.</p>;
  return (
    <ul aria-label="Everyone’s API keys" className="m-0 flex list-none flex-col gap-2 p-0">
      {keys.map((k) => (
        <li key={k.id} className="flex flex-wrap items-center gap-3 rounded-lg px-3 py-2" style={{ background: "var(--panel-2)" }}>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2 text-base">
              <span className="font-semibold">{k.username}</span>
              <span className="truncate text-ink-dim">{k.name}</span>
              <span className="flex-none rounded-full px-1.5 py-px text-xs font-semibold" style={{ background: "var(--panel)", color: "var(--ink-dim)" }}>{SCOPE_LABEL[k.scope]}</span>
            </div>
            <div className="text-xs text-ink-faint">
              <span className="font-mono">{k.hint}</span> · Made {ago(k.created_at) || "a while ago"} · {k.last_used_at ? `Last used ${ago(k.last_used_at)}` : "Never used"} · {expiryLabel(k)}
            </div>
          </div>
          <Button size="sm" variant="ghost" onClick={() => revoke(k)} busy={busy === k.id} busyLabel="…" aria-label={`Revoke ${k.username}’s key ${k.name}`}>Revoke</Button>
        </li>
      ))}
    </ul>
  );
}

export default PersonalApiKeys;
