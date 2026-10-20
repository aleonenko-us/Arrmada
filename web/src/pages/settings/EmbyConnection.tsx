import { useState, type FormEvent } from "react";
import { Note } from "../../components/settings/ui";
import { embyApi, type EmbyConfig, type EmbyTestResult } from "../../lib/embyApi";
import { lanOnly } from "../../lib/lanAddress";
import { setQueryData, useQuery } from "../../lib/query";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { Button, StatusChip, useConfirm, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";

// EmbyConnect points Arrmada at an Emby server: its URL, an API key made in
// Emby, and the public address Watch links use. It opens inside the Emby card on
// Settings → Connections and reads and writes the connection through the API on its own.
// The key is a credential: it's typed here, saved with Save (not auto-saved) and never
// comes back — the form only says a key is set, and a blank key field keeps it.
//
// testFirst (if requested): Save tests what's typed first and only saves an address or
// key Emby turned down if the admin says so.

const EMBY_KEY = "emby";

export function EmbyConnect({ onChanged, testFirst }: { onChanged?: () => void; testFirst?: boolean }) {
  const q = useQuery(EMBY_KEY, embyApi.get, { staleMs: 0 });
  return (
    <div className="mt-2 flex flex-col gap-4 border-t pt-3" style={{ borderColor: "var(--line)" }}>
      {q.data ? (
        <EmbyForm cfg={q.data} onChanged={onChanged} testFirst={testFirst} />
      ) : q.error ? (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span role="alert" style={{ color: "var(--reject-text)" }}>Couldn't load the Emby connection: {q.error.message}</span>
          <Button size="sm" onClick={() => void q.refetch()} busy={q.loading} busyLabel="Retrying…">Retry</Button>
        </div>
      ) : (
        <p className="m-0 text-sm text-ink-dim">Loading…</p>
      )}
    </div>
  );
}

function EmbyForm({ cfg, onChanged, testFirst }: { cfg: EmbyConfig; onChanged?: () => void; testFirst?: boolean }) {
  const toast = useToast();
  const confirm = useConfirm();
  const [url, setUrl] = useState(cfg.url);
  const [key, setKey] = useState("");
  // The public address follows the URL until it's typed in itself.
  const [ownPublic, setOwnPublic] = useState<string | null>(cfg.public_url || null);
  const publicUrl = ownPublic ?? url;
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<EmbyTestResult | null>(null);

  const savedPublic = cfg.public_url || cfg.url;
  const dirty = url.trim() !== cfg.url || key.trim() !== "" || publicUrl.trim() !== savedPublic;
  const markClean = useUnsavedGuard(dirty);
  const needsKey = !cfg.key_set && !key.trim();
  const homeOnly = lanOnly(publicUrl);

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!url.trim() || needsKey) return;
    if (testFirst && (url.trim() !== cfg.url || key.trim())) {
      setBusy("test");
      setTest(null);
      const r = await embyApi.test({ url: url.trim(), api_key: key.trim() || undefined }).catch((err: Error) => ({ ok: false, detail: err.message }) as EmbyTestResult);
      setTest(r);
      setBusy(null);
      if (!r.ok && !(await confirm({
        title: "Save it anyway?",
        body: `Emby didn't answer as expected: ${r.detail || "no answer"}. Watch links and library updates won't work until it does.`,
        confirmLabel: "Save anyway",
        cancelLabel: "Keep editing",
      }))) return;
    }
    setBusy("save");
    try {
      const next = await embyApi.save({ url: url.trim(), public_url: publicUrl.trim(), api_key: key.trim() || undefined });
      setQueryData(EMBY_KEY, next);
      setKey("");
      setUrl(next.url);
      setOwnPublic(next.public_url || null);
      markClean();
      onChanged?.();
      toast("Emby connection saved", { tone: "good" });
    } catch (err) {
      toast((err as Error).message, { tone: "error" });
    } finally {
      setBusy(null);
    }
  };

  const runTest = async () => {
    setBusy("test");
    setTest(null);
    try {
      setTest(await embyApi.test({ url: url.trim() || undefined, api_key: key.trim() || undefined }));
    } catch (err) {
      setTest({ ok: false, detail: (err as Error).message });
    } finally {
      setBusy(null);
    }
  };

  return (
    <form onSubmit={save} aria-label="Emby connection" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {cfg.key_set ? <StatusChip tone="good">Key set</StatusChip> : <StatusChip tone="faint">No key yet</StatusChip>}
        {cfg.server_name && (
          <span className="text-ink-dim">{cfg.server_name}{cfg.version ? ` · Emby ${cfg.version}` : ""}</span>
        )}
      </div>

      <Field label="Server URL" hint="As Arrmada reaches it, for example http://192.168.1.10:8096 or http://emby:8096.">
        <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://192.168.1.10:8096" autoComplete="off" spellCheck={false} />
      </Field>

      <Field label="API key" hint="Make one in Emby: Dashboard → API Keys → +. It's never shown again once saved.">
        <Input
          type="password"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          placeholder={cfg.key_set ? "Key set — leave blank to keep it" : "Paste the API key"}
          autoComplete="new-password"
        />
      </Field>

      <div className="flex flex-col gap-2">
        <Field label="Public address for Watch links" hint="Where people open Emby from outside your home. Starts as the server URL.">
          <Input value={publicUrl} onChange={(e) => setOwnPublic(e.target.value)} placeholder="https://emby.example.com" autoComplete="off" spellCheck={false} />
        </Field>
        {homeOnly && (
          <Note tone="warn">
            This address only works on your home network, so Watch links won't open on a phone away from home.
            Use the address you reach Emby on from outside, like https://emby.example.com.
          </Note>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={runTest} busy={busy === "test"} busyLabel="Testing…" disabled={busy !== null || !url.trim()}>Test</Button>
        <Button type="submit" variant="primary" busy={busy === "save"} busyLabel="Saving…" disabled={busy !== null || !url.trim() || needsKey}>Save</Button>
        {needsKey && url.trim() && <span className="text-xs text-ink-faint">Add an API key to save.</span>}
      </div>

      {test && (
        <div
          role="status"
          className="break-words rounded-lg p-3 text-sm font-semibold"
          style={{
            border: `1px solid ${test.ok ? "var(--good)" : "var(--reject)"}`,
            background: test.ok ? "var(--good-soft)" : "var(--reject-soft)",
            color: test.ok ? "var(--good-text)" : "var(--reject-text)",
          }}
        >
          {test.ok
            ? `✓ Connected${test.server_name ? ` to ${test.server_name}` : ""}${test.version ? ` · Emby ${test.version}` : ""}`
            : `✕ ${test.detail || "Couldn't reach Emby"}`}
        </div>
      )}

    </form>
  );
}
