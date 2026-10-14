import { useState, type FormEvent } from "react";
import { Note } from "../../components/settings/ui";
import { jellyfinApi, type JellyfinConfig, type JellyfinTestResult } from "../../lib/jellyfinApi";
import { lanOnly } from "../../lib/lanAddress";
import { setQueryData, useQuery } from "../../lib/query";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { Button, StatusChip, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";

// JellyfinConnect points Arrmada at a Jellyfin server: its URL, an API key made in
// Jellyfin, and the public address Watch links use. It opens inside the Jellyfin card on
// Settings → Connections and reads and writes the connection through the API on its own.
// The key is a credential: it's typed here, saved with Save (not auto-saved) and never
// comes back — the form only says a key is set, and a blank key field keeps it.

const JELLYFIN_KEY = "jellyfin";

export function JellyfinConnect({ onChanged }: { onChanged?: () => void }) {
  const q = useQuery(JELLYFIN_KEY, jellyfinApi.get, { staleMs: 0 });
  return (
    <div className="mt-2 flex flex-col gap-4 border-t pt-3" style={{ borderColor: "var(--line)" }}>
      {q.data ? (
        <JellyfinForm cfg={q.data} onChanged={onChanged} />
      ) : q.error ? (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span role="alert" style={{ color: "var(--reject-text)" }}>Couldn't load the Jellyfin connection: {q.error.message}</span>
          <Button size="sm" onClick={() => void q.refetch()} busy={q.loading} busyLabel="Retrying…">Retry</Button>
        </div>
      ) : (
        <p className="m-0 text-sm text-ink-dim">Loading…</p>
      )}
    </div>
  );
}

function JellyfinForm({ cfg, onChanged }: { cfg: JellyfinConfig; onChanged?: () => void }) {
  const toast = useToast();
  const [url, setUrl] = useState(cfg.url);
  const [key, setKey] = useState("");
  // The public address follows the URL until it's typed in itself.
  const [ownPublic, setOwnPublic] = useState<string | null>(cfg.public_url || null);
  const publicUrl = ownPublic ?? url;
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<JellyfinTestResult | null>(null);

  const savedPublic = cfg.public_url || cfg.url;
  const dirty = url.trim() !== cfg.url || key.trim() !== "" || publicUrl.trim() !== savedPublic;
  const markClean = useUnsavedGuard(dirty);
  const needsKey = !cfg.key_set && !key.trim();
  const homeOnly = lanOnly(publicUrl);

  const save = async (e?: FormEvent) => {
    e?.preventDefault();
    if (!url.trim() || needsKey) return;
    setBusy("save");
    try {
      const next = await jellyfinApi.save({ url: url.trim(), public_url: publicUrl.trim(), api_key: key.trim() || undefined });
      setQueryData(JELLYFIN_KEY, next);
      setKey("");
      setUrl(next.url);
      setOwnPublic(next.public_url || null);
      markClean();
      onChanged?.();
      toast("Jellyfin connection saved", { tone: "good" });
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
      setTest(await jellyfinApi.test({ url: url.trim() || undefined, api_key: key.trim() || undefined }));
    } catch (err) {
      setTest({ ok: false, detail: (err as Error).message });
    } finally {
      setBusy(null);
    }
  };

  return (
    <form onSubmit={save} aria-label="Jellyfin connection" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {cfg.key_set ? <StatusChip tone="good">Key set</StatusChip> : <StatusChip tone="faint">No key yet</StatusChip>}
        {cfg.server_name && (
          <span className="text-ink-dim">{cfg.server_name}{cfg.version ? ` · Jellyfin ${cfg.version}` : ""}</span>
        )}
      </div>

      <Field label="Server URL" hint="As Arrmada reaches it, for example http://192.168.1.10:8096 or http://jellyfin:8096.">
        <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://192.168.1.10:8096" autoComplete="off" spellCheck={false} />
      </Field>

      <Field label="API key" hint="Make one in Jellyfin: Dashboard → API Keys → +. It's never shown again once saved.">
        <Input
          type="password"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          placeholder={cfg.key_set ? "Key set — leave blank to keep it" : "Paste the API key"}
          autoComplete="new-password"
        />
      </Field>

      <div className="flex flex-col gap-2">
        <Field label="Public address for Watch links" hint="Where people open Jellyfin from outside your home. Starts as the server URL.">
          <Input value={publicUrl} onChange={(e) => setOwnPublic(e.target.value)} placeholder="https://jellyfin.example.com" autoComplete="off" spellCheck={false} />
        </Field>
        {homeOnly && (
          <Note tone="warn">
            This address only works on your home network, so Watch links won't open on a phone away from home.
            Use the address you reach Jellyfin on from outside, like https://jellyfin.example.com.
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
            ? `✓ Connected${test.server_name ? ` to ${test.server_name}` : ""}${test.version ? ` · Jellyfin ${test.version}` : ""}`
            : `✕ ${test.detail || "Couldn't reach Jellyfin"}`}
        </div>
      )}
    </form>
  );
}
