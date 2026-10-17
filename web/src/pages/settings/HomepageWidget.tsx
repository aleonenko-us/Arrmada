import { useEffect, useState } from "react";
import { Note, Section } from "../../components/settings/ui";
import { req } from "../../lib/api";
import { Button, useConfirm, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Textarea } from "../../ui/Textarea";
import { homepageYaml, HOMEPAGE_KEY_VAR, LAN_PLACEHOLDER, widgetOrigin } from "./homepageYaml";

// The dashboard widget card on Settings → System: a key for gethomepage.dev's customapi
// widget, and the services.yaml entry to paste. Like the calendar feed, the server keeps
// only a fingerprint of the key, so it's shown once, when it's made; after that the YAML
// reads the key from a Homepage variable instead.

// outside: this page is open on an outside address, which Homepage can't use.
interface KeyState { enabled: boolean; created_at?: number; outside?: boolean }
interface MadeKey extends KeyState { key: string; url: string }

const widgetApi = {
  status: () => req<KeyState>("/api/v1/system/widget-key"),
  make: () => req<MadeKey>("/api/v1/system/widget-key", { method: "POST" }),
  turnOff: () => req<KeyState>("/api/v1/system/widget-key", { method: "DELETE" }),
};

export function HomepageWidgetSection() {
  const [state, setState] = useState<KeyState | null>(null);
  const [made, setMade] = useState<MadeKey | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  useEffect(() => {
    let alive = true;
    widgetApi.status().then((s) => { if (alive) setState(s); }).catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, []);

  const make = async () => {
    if (state?.enabled) {
      const ok = await confirm({
        title: "Make a new widget key?",
        body: "Dashboards using the current key stop showing Arrmada's numbers until they get the new one.",
        confirmLabel: "Make a new key", tone: "danger",
      });
      if (!ok) return;
    }
    setBusy(true);
    try {
      const m = await widgetApi.make();
      setMade(m);
      setState({ enabled: true, created_at: m.created_at });
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  const turnOff = async () => {
    const ok = await confirm({ title: "Turn the widget key off?", body: "Dashboards using it stop showing Arrmada's numbers. You can make a new key any time.", confirmLabel: "Turn off", tone: "danger" });
    if (!ok) return;
    setBusy(true);
    try {
      setState(await widgetApi.turnOff());
      setMade(null);
      toast("Widget key turned off", { tone: "good" });
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  const copy = async (text: string, what: string) => {
    try { await navigator.clipboard.writeText(text); toast(`${what} copied`, { tone: "good" }); }
    catch { toast("Couldn’t copy — select it and copy it yourself", { tone: "error" }); }
  };

  // The address Homepage's server should use: the one this page was opened on, which is
  // usually the LAN address — or a placeholder to replace when it's an outside address.
  const outside = !!(made?.outside ?? state?.outside);
  const yaml = homepageYaml(widgetOrigin(outside, made?.url, window.location.origin), made?.key ?? "");

  return (
    <Section
      id="dashboard-widget"
      title="Dashboard widget"
      subtitle={<>Show Arrmada’s numbers on a <a href="https://gethomepage.dev" target="_blank" rel="noreferrer" style={{ color: "var(--accent-text)" }}>Homepage</a> dashboard: how many titles you have, what’s wanted, downloads, requests waiting, what needs you, disk space and updates. Counts only — never titles or names. The key opens the widget and nothing else. <a href="https://github.com/tristenlammi/Arrmada/blob/main/docs/homepage.md" target="_blank" rel="noreferrer" style={{ color: "var(--accent-text)" }}>Setup guide</a></>}
    >
      {error && !state && <p className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>Couldn’t load the widget key — {error}</p>}
      {!state && !error && <p className="m-0 text-sm text-ink-faint">Loading…</p>}
      {state && (
        <>
          {made ? (
            <p className="m-0 text-sm">Your widget key is in the entry below. This is the only time it’s shown — copy it now.</p>
          ) : state.enabled ? (
            <p className="m-0 text-sm">A widget key is set{state.created_at ? `, made ${new Date(state.created_at * 1000).toLocaleDateString()}` : ""}. It isn’t shown again: the entry below reads it from <code className="font-mono text-xs">{HOMEPAGE_KEY_VAR}</code>, or make a new key to see one.</p>
          ) : (
            <p className="m-0 text-sm text-ink-dim">Make a key, then paste the entry into Homepage’s services.yaml.</p>
          )}
          {(made || state.enabled) && (
            <>
              {outside && (
                <Note tone="warn">
                  You’re on an outside address, which Homepage can’t use — the widget only answers on your home network. Replace <code className="font-mono text-xs">{LAN_PLACEHOLDER}</code> with the address Homepage reaches Arrmada on, such as <code className="font-mono text-xs">http://192.168.1.10:7878</code> or the container’s name.
                </Note>
              )}
              <Field label="services.yaml" hint="Change the address if Homepage reaches Arrmada another way, such as its container name.">
                <Textarea readOnly value={yaml} rows={Math.min(yaml.split("\n").length, 28)} onFocus={(e) => e.currentTarget.select()} className="w-full px-3 py-2 font-mono text-sm" spellCheck={false} wrap="off" />
              </Field>
              <div className="flex flex-wrap gap-2">
                <Button size="sm" onClick={() => copy(yaml, "Entry")}>Copy entry</Button>
                {made && <Button size="sm" onClick={() => copy(made.key, "Key")}>Copy key only</Button>}
              </div>
            </>
          )}
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant={state.enabled ? "secondary" : "primary"} onClick={make} busy={busy} busyLabel="Working…" disabled={busy}>
              {state.enabled ? "Make a new key" : "Make widget key"}
            </Button>
            {state.enabled && <Button size="sm" variant="ghost" onClick={turnOff} disabled={busy} style={{ color: "var(--reject-text)" }}>Turn off</Button>}
          </div>
        </>
      )}
    </Section>
  );
}
