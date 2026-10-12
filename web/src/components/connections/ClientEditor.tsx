import { useEffect, useState } from "react";
import { type DownloadCategories, type DownloadClient, type NewDownloadClient } from "../../lib/api";
import { downloadClientApi } from "../../lib/downloadClientApi";
import { PathMappings } from "./PathMappings";

// The download-client form, moved from the old Download clients page into the cards on
// Settings → Connections. It adds a client, or edits one in place; the stored password is
// never sent to the browser, so on an edit a blank password keeps it.

const field = "w-full rounded-lg px-3 py-2 text-[13px]";
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

// clientBody is a client's saved settings as a PUT body with a change swapped in. The
// password goes back blank, which keeps the stored one; the order is left out, which
// keeps its own.
export function clientBody(c: DownloadClient, change: Partial<NewDownloadClient>): NewDownloadClient {
  return { name: c.name, kind: c.kind, url: c.url, username: c.username ?? "", password: "", enabled: c.enabled, ...change };
}

export function ClientForm({ editing, onSaved }: { editing?: DownloadClient; onSaved: (c: DownloadClient) => void }) {
  const [name, setName] = useState(editing?.name ?? "qBittorrent");
  // No default URL: localhost:8080 inside the Arrmada container is Arrmada itself.
  const [url, setUrl] = useState(editing?.url ?? "");
  const [username, setUsername] = useState(editing?.username ?? "");
  const [password, setPassword] = useState("");
  const [priority, setPriority] = useState(String(editing?.priority ?? 25));
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const order = Number(priority);
    if (!Number.isInteger(order) || order < 1 || order > 99) {
      setError("Order must be a whole number from 1 to 99.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const c = editing
        ? await downloadClientApi.updateDownloadClient(editing.id, { name, kind: editing.kind, url, username, password, enabled: editing.enabled, priority: order })
        : await downloadClientApi.createDownloadClient({ name, kind: "qbittorrent", url, username, password, priority: order });
      onSaved(c);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      onSubmit={submit}
      aria-label={editing ? `Edit ${editing.name}` : "Add a download client"}
      className={editing ? "mt-2 rounded-lg p-3.5" : "rounded-xl p-4"}
      style={{ background: editing ? "var(--panel-2)" : "var(--panel)", border: "1px solid var(--line)" }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Labeled label="Name">
          <input className={field} style={fieldStyle} value={name} onChange={(e) => setName(e.target.value)} required />
        </Labeled>
        <Labeled label="WebUI URL">
          <input
            className={field}
            style={fieldStyle}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="http://qbittorrent:8080 (as reached from the Arrmada container)"
            required
          />
        </Labeled>
        <Labeled label="Username">
          <input className={field} style={fieldStyle} value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" />
        </Labeled>
        <Labeled label="Password">
          <input
            type="password"
            className={field}
            style={fieldStyle}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={editing ? "unchanged; leave blank to keep" : undefined}
            autoComplete="new-password"
          />
        </Labeled>
        <Labeled label="Order (1 = first)" hint="New downloads go to the lowest number. The next client is used only when this one can't be reached at all.">
          <input type="number" min={1} max={99} className={field} style={fieldStyle} value={priority} onChange={(e) => setPriority(e.target.value)} required />
        </Labeled>
      </div>
      <p className="mt-3 text-[11px] text-ink-faint">
        Points at your qBittorrent WebUI as the Arrmada container reaches it: its container name or your server’s IP. localhost here means the Arrmada container itself, not your server. Credentials are stored on your server.
      </p>
      {error && <div className="mt-3 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}
      <button
        type="submit"
        disabled={saving}
        className="mt-3.5 rounded-lg px-4 py-2 text-[12.5px] font-semibold"
        style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: saving ? 0.6 : 1 }}
      >
        {saving ? "Saving…" : editing ? "Save changes" : "Save client"}
      </button>
    </form>
  );
}

// ClientEditor opens inside a client's card: the port to forward, the form, then its path
// mappings and path check. A switched-off client may not be running at all, so its port
// isn't asked.
export function ClientEditor({ client, onSaved, onChanged }: { client: DownloadClient; onSaved: (c: DownloadClient) => void; onChanged?: () => void }) {
  const [port, setPort] = useState(0);
  useEffect(() => {
    if (client.kind !== "qbittorrent" || !client.enabled) return;
    let live = true;
    downloadClientApi.downloadClientStatus(client.id).then((s) => { if (live) setPort(s.listen_port); }).catch(() => {});
    return () => { live = false; };
  }, [client.id, client.kind, client.enabled]);
  return (
    <div className="mt-1 flex flex-col gap-1">
      {port > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
          <span className="rounded px-1.5 py-0.5 font-mono text-[10px]" style={{ background: "var(--accent-soft)", color: "var(--accent)" }}>
            incoming port {port}
          </span>
          <span className="text-ink-faint">forward TCP + UDP on your router to seed properly</span>
        </div>
      )}
      <ClientForm editing={client} onSaved={onSaved} />
      <PathMappings client={client} body={(maps) => clientBody(client, { path_maps: maps })} onChanged={onChanged} />
    </div>
  );
}

// CategoryLine names the categories Arrmada files downloads under in every client. They're
// fixed: each importer reads only its own, so they aren't a setting.
export function CategoryLine({ c }: { c: DownloadCategories }) {
  return (
    <p className="m-0 text-[11px] text-ink-dim" title="Arrmada files every download under one of these, so each importer finds its own.">
      Categories in qBittorrent: <span className="font-mono">{c.movies}</span> (movies) · <span className="font-mono">{c.tv}</span> · <span className="font-mono">{c.books}</span> · <span className="font-mono">{c.music}</span>
    </p>
  );
}

function Labeled({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="font-mono text-[9.5px] font-bold uppercase tracking-[0.1em] text-ink-faint">{label}</span>
      {children}
      {hint && <span className="text-[10.5px] text-ink-faint">{hint}</span>}
    </label>
  );
}
