import { useEffect, useId, useState } from "react";
import { type DownloadCategories, type DownloadClient, type NewDownloadClient } from "../../lib/api";
import { downloadClientApi, type UsenetFacts } from "../../lib/downloadClientApi";
import { isUsenetClient } from "../../lib/network";
import { Button } from "../../ui";
import { PathCheckLine, PathMappings } from "./PathMappings";
import { Labeled } from "./IndexerEditor";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";

// The download-client form, moved from the old Download clients page into the cards on
// Settings → Connections. It adds a client — qBittorrent for torrents or SABnzbd for
// Usenet — or edits one in place; the stored password or API key is never sent to the
// browser, so on an edit a blank one keeps it.

const field = "w-full rounded-lg px-3 py-2 text-md";
const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" } as const;

type Kind = "qbittorrent" | "sabnzbd";

// The kinds a new client can be, as the picker names them.
const KINDS: { kind: Kind; name: string; network: string }[] = [
  { kind: "qbittorrent", name: "qBittorrent", network: "Torrents" },
  { kind: "sabnzbd", name: "SABnzbd", network: "Usenet" },
];
const defaultName = (k: Kind) => (k === "sabnzbd" ? "SABnzbd" : "qBittorrent");

// clientBody is a client's saved settings as a PUT body with a change swapped in. The
// password (a SABnzbd's API key) goes back blank, which keeps the stored one; the order is
// left out, which keeps its own.
export function clientBody(c: DownloadClient, change: Partial<NewDownloadClient>): NewDownloadClient {
  return { name: c.name, kind: c.kind, url: c.url, username: c.username ?? "", password: "", enabled: c.enabled, ...change };
}

export function ClientForm({ editing, onSaved }: { editing?: DownloadClient; onSaved: (c: DownloadClient) => void }) {
  const [kind, setKind] = useState<Kind>(editing?.kind === "sabnzbd" ? "sabnzbd" : "qbittorrent");
  const usenet = kind === "sabnzbd";
  const [name, setName] = useState(editing?.name ?? defaultName(kind));
  // No default URL: localhost:8080 inside the Arrmada container is Arrmada itself.
  const [url, setUrl] = useState(editing?.url ?? "");
  const [username, setUsername] = useState(editing?.username ?? "");
  // qBittorrent's password, or SABnzbd's API key: both are the client's stored secret.
  const [password, setPassword] = useState("");
  const [priority, setPriority] = useState(String(editing?.priority ?? 25));
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const kindName = useId();
  // Dirty once the form differs from how it opened.
  const shape = JSON.stringify([kind, name, url, username, password, priority]);
  const [opened] = useState(shape);
  const markClean = useUnsavedGuard(shape !== opened);

  // Switching the kind of a new client renames it too, unless a name was typed.
  const pickKind = (k: Kind) => {
    if (name === defaultName(kind)) setName(defaultName(k));
    setKind(k);
    setError(null);
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const order = Number(priority);
    if (!Number.isInteger(order) || order < 1 || order > 99) {
      setError("Order must be a whole number from 1 to 99.");
      return;
    }
    setSaving(true);
    setError(null);
    // SABnzbd signs in with its API key alone: it travels as the password, never a username.
    const login = usenet ? { username: "", password: password.trim() } : { username, password };
    try {
      const c = editing
        ? await downloadClientApi.updateDownloadClient(editing.id, { name, kind: editing.kind, url, ...login, enabled: editing.enabled, priority: order })
        : await downloadClientApi.createDownloadClient({ name, kind, url, ...login, priority: order });
      markClean();
      setPassword("");
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
      {!editing && (
        <fieldset className="m-0 mb-3 border-0 p-0">
          <legend className="mb-1.5 p-0 font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Kind</legend>
          <div className="grid grid-cols-1 gap-2 min-[400px]:grid-cols-2">
            {KINDS.map((k) => {
              const on = kind === k.kind;
              return (
                <label
                  key={k.kind}
                  className="flex cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2"
                  style={{ border: `1px solid ${on ? "var(--accent)" : "var(--line)"}`, background: on ? "var(--accent-soft)" : "var(--panel-2)" }}
                >
                  <input type="radio" name={kindName} value={k.kind} checked={on} onChange={() => pickKind(k.kind)} style={{ accentColor: "var(--accent)" }} />
                  <span className="text-md font-semibold" style={{ color: on ? "var(--accent-text)" : "var(--ink)" }}>{k.name}</span>
                  <span className="text-sm text-ink-dim">· {k.network}</span>
                </label>
              );
            })}
          </div>
        </fieldset>
      )}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Labeled label="Name">
          <input className={field} style={fieldStyle} value={name} onChange={(e) => setName(e.target.value)} required />
        </Labeled>
        {usenet ? (
          <>
            <Labeled label="URL" hint="As reached from Arrmada, e.g. http://sabnzbd:8080">
              <input className={field} style={fieldStyle} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://sabnzbd:8080" inputMode="url" autoComplete="off" required />
            </Labeled>
            <Labeled label="API key" hint="SABnzbd → Config → General → API Key">
              <input
                type="password"
                className={field}
                style={fieldStyle}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={editing ? "unchanged; leave blank to keep" : undefined}
                autoComplete="off"
                required={!editing}
              />
            </Labeled>
          </>
        ) : (
          <>
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
          </>
        )}
        <Labeled label="Order (1 = first)" hint={usenet ? "Usenet releases go to the lowest-numbered usenet client; the next is used only when it can't be reached at all." : "New downloads go to the lowest number. The next client is used only when this one can't be reached at all."}>
          <input type="number" min={1} max={99} className={field} style={fieldStyle} value={priority} onChange={(e) => setPriority(e.target.value)} required />
        </Labeled>
      </div>
      <p className="mt-3 text-xs text-ink-faint">
        {usenet
          ? "Use the full API key, not the NZB key. Saving tests it straight away. The key is stored on your server and never shown again."
          : "Points at your qBittorrent WebUI as the Arrmada container reaches it: its container name or your server’s IP. localhost here means the Arrmada container itself, not your server. Credentials are stored on your server."}
      </p>
      {error && <div role="alert" className="mt-3 text-sm" style={{ color: "var(--reject-text)" }}>{error}</div>}
      <button
        type="submit"
        disabled={saving}
        className="mt-3.5 rounded-lg px-4 py-2 text-base font-semibold"
        style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)", opacity: saving ? 0.6 : 1 }}
      >
        {saving ? "Saving…" : editing ? "Save changes" : "Save client"}
      </button>
    </form>
  );
}

// ClientEditor opens inside a client's card: for qBittorrent the port to forward, for
// SABnzbd what Arrmada needs from it (categories, completed folder, news servers); then the
// form, then its path mappings and path check. The status is read once, when the card
// opens: for SABnzbd it's several calls, so it's never polled. A switched-off qBittorrent
// may not be running at all, so its port isn't asked.
export function ClientEditor({ client, onSaved, onChanged, admin }: { client: DownloadClient; onSaved: (c: DownloadClient) => void; onChanged?: () => void; admin?: boolean }) {
  const usenet = isUsenetClient(client);
  const [port, setPort] = useState(0);
  useEffect(() => {
    if (usenet || client.kind !== "qbittorrent" || !client.enabled) return;
    let live = true;
    downloadClientApi.downloadClientStatus(client.id).then((s) => { if (live) setPort(s.listen_port); }).catch(() => {});
    return () => { live = false; };
  }, [client.id, client.kind, client.enabled, usenet]);
  return (
    <div className="mt-1 flex flex-col gap-1">
      {port > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="rounded px-1.5 py-0.5 font-mono text-xs" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }}>
            incoming port {port}
          </span>
          <span className="text-ink-faint">forward TCP + UDP on your router to seed properly</span>
        </div>
      )}
      {usenet && <UsenetPanel client={client} onChanged={onChanged} admin={!!admin} />}
      {client.has_login && client.kind === "qbittorrent" && <BuiltInQbit port={client.ui_port} builtIn={!!client.built_in} local={!!client.ui_local} admin={!!admin} />}
      <ClientForm editing={client} onSaved={onSaved} />
      <PathMappings client={client} body={(maps) => clientBody(client, { path_maps: maps })} onChanged={onChanged} />
    </div>
  );
}

const FOR_LABEL: Record<string, string> = { movies: "movies", tv: "TV", books: "books", music: "music" };

// serversLine sums up SABnzbd's news servers: "1 server, working", "3 servers, all
// working", "2 servers, 1 working", or that there's none.
export function serversLine(f: Pick<UsenetFacts, "servers_configured" | "servers_working">): { text: string; ok: boolean } {
  const n = f.servers_configured;
  if (n === 0) return { text: "No news server set up", ok: false };
  const noun = n === 1 ? "1 server" : `${n} servers`;
  if (f.servers_working >= n) return { text: n === 1 ? `${noun}, working` : `${noun}, all working`, ok: true };
  return { text: `${noun}, ${f.servers_working} working`, ok: false };
}

// UsenetPanel is the SABnzbd card's body: Arrmada's categories and which it has (with Add
// missing categories, which only ever acts on that click), the completed folder and where
// Arrmada sees it, the news servers, and anything else SABnzbd's answers say needs a look.
function UsenetPanel({ client, onChanged, admin }: { client: DownloadClient; onChanged?: () => void; admin: boolean }) {
  const [facts, setFacts] = useState<UsenetFacts | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);
  const [added, setAdded] = useState<string | null>(null);
  const [addError, setAddError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    downloadClientApi.downloadClientStatus(client.id)
      .then((s) => {
        if (!live) return;
        if (s.usenet) setFacts(s.usenet);
        else setError(s.usenet_error ?? "SABnzbd didn't say.");
      })
      .catch((e: Error) => { if (live) setError(e.message); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [client.id]);

  const addMissing = async () => {
    setAdding(true);
    setAdded(null);
    setAddError(null);
    try {
      const r = await downloadClientApi.addMissingCategories(client.id);
      if (r.usenet) setFacts(r.usenet);
      setAdded(r.created.length > 0 ? `Added ${r.created.join(", ")}.` : "Nothing was missing.");
      onChanged?.();
    } catch (e) {
      setAddError(`Couldn't add them: ${(e as Error).message}`);
    } finally {
      setAdding(false);
    }
  };

  const box = "mt-2 rounded-lg p-3.5";
  const boxStyle = { background: "var(--panel-2)", border: "1px solid var(--line)" } as const;
  if (loading) {
    return <section aria-label="SABnzbd" className={`${box} text-sm text-ink-dim`} style={boxStyle}>Asking {client.name}…</section>;
  }
  if (!facts) {
    return (
      <section aria-label="SABnzbd" className={box} style={boxStyle}>
        <p role="alert" className="m-0 break-words text-sm" style={{ color: "var(--reject-text)" }}>Couldn't read {client.name}: {error}</p>
      </section>
    );
  }
  const servers = serversLine(facts);
  const own = facts.categories.filter((c) => c.folder);
  return (
    <section aria-label="SABnzbd" className={`${box} flex flex-col gap-2.5`} style={boxStyle}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <div className="text-base font-semibold">What Arrmada needs from SABnzbd</div>
        {facts.version && <span className="font-mono text-xs text-ink-faint">SABnzbd {facts.version}</span>}
      </div>

      <div>
        <div className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Categories</div>
        <ul aria-label="Categories" className="m-0 mt-1.5 flex list-none flex-wrap gap-1.5 p-0">
          {facts.categories.map((c) => (
            <li
              key={c.name}
              className="rounded px-1.5 py-0.5 font-mono text-xs"
              style={{ background: "var(--panel)", border: "1px solid var(--line)", color: c.present ? "var(--good-text)" : "var(--reject-text)" }}
            >
              {c.present ? "✓" : "✕"} {c.name} <span className="text-ink-dim">({FOR_LABEL[c.for] ?? c.for})</span>
            </li>
          ))}
        </ul>
        {facts.missing_categories.length > 0 && (
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <Button size="sm" variant="primary" onClick={() => void addMissing()} busy={adding} busyLabel="Adding…">Add missing categories</Button>
            <span className="text-xs text-ink-faint">Creates {facts.missing_categories.join(", ")} in SABnzbd. Nothing else there is changed.</span>
          </div>
        )}
        {added && <p role="status" className="mb-0 mt-1.5 text-sm" style={{ color: "var(--good-text)" }}>{added}</p>}
        {addError && <p role="alert" className="mb-0 mt-1.5 break-words text-sm" style={{ color: "var(--reject-text)" }}>{addError}</p>}
      </div>

      {(facts.complete_folder.remote || own.length > 0) && (
        <div className="flex flex-col gap-1">
          {facts.complete_folder.remote && <PathCheckLine c={facts.complete_folder} seesAt />}
          {own.map((c) => c.folder && <PathCheckLine key={c.name} c={{ ...c.folder, category: c.folder.category ?? c.name }} seesAt />)}
        </div>
      )}

      <div className="text-sm" style={{ color: servers.ok ? "var(--good-text)" : "var(--reject-text)" }}>
        {servers.ok ? "✓" : "✕"} <span className="text-ink-dim">Usenet servers:</span> {servers.text}
        {facts.servers.filter((s) => !s.ok && s.error).map((s) => (
          <div key={s.name} className="break-words text-xs text-ink-dim">{s.name}: {s.error}</div>
        ))}
      </div>
      {client.bundled && client.ui_port && <OpenBundledSAB port={client.ui_port} noServer={facts.servers_configured === 0} login={admin && !!client.has_login} />}

      {facts.warnings.length > 0 && (
        <ul aria-label="Needs a look" className="m-0 flex list-none flex-col gap-1 p-0">
          {facts.warnings.map((w) => (
            <li key={w} className="break-words text-sm" style={{ color: "var(--avoid-text)" }}>{w}</li>
          ))}
        </ul>
      )}
      <p className="m-0 text-xs text-ink-faint">
        Arrmada downloads each NZB itself and hands SABnzbd the file, so your indexers&rsquo; keys stay out of SABnzbd&rsquo;s history. When an indexer blocks Arrmada but lets SABnzbd in (some only allow certain apps), SABnzbd is given the NZB link to fetch instead, and that link, with the indexer&rsquo;s key in it, stays in SABnzbd&rsquo;s history.
      </p>
    </section>
  );
}

// bundledSABLink is the bundled SABnzbd's own page as the browser reaches it: this host on
// the port it's published on (not the container name Arrmada talks to it by).
export function bundledSABLink(port: string, loc: { protocol: string; hostname: string }): string {
  return `${loc.protocol}//${loc.hostname}:${port}`;
}

// OpenBundledSAB is where the bundled SABnzbd's news server is added: SABnzbd's own page,
// behind the login Arrmada made for it (which an admin can show). Arrmada never asks for
// the provider's login.
function OpenBundledSAB({ port, noServer, login }: { port: string; noServer: boolean; login: boolean }) {
  const href = bundledSABLink(port, window.location);
  return (
    <div className="flex flex-col gap-1 text-sm">
      <div className="break-words">
        <span className="text-ink-dim">{noServer ? "Add your Usenet provider in SABnzbd (Config → Servers):" : "SABnzbd's own page:"}</span>{" "}
        <a href={href} target="_blank" rel="noreferrer" className="break-all font-mono font-semibold" style={{ color: "var(--accent-text)" }}>{href} ↗</a>
      </div>
      {login && <ShowLogin kind="sabnzbd" app="SABnzbd" />}
    </div>
  );
}

// ShowLogin shows an admin, on request, the login Arrmada made for a bundled client's own
// page (fetched only on the click, never kept).
function ShowLogin({ kind, app }: { kind?: "sabnzbd"; app: string }) {
  const [login, setLogin] = useState<{ username: string; password: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const show = async () => {
    setBusy(true);
    setError(null);
    try {
      setLogin(await downloadClientApi.bundledLogin(kind));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      {!login && (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="secondary" onClick={() => void show()} busy={busy} busyLabel="Loading…">Show its login</Button>
          <span className="text-xs text-ink-faint">The one Arrmada made. If you changed it in {app}, use yours.</span>
        </div>
      )}
      {login && (
        <div className="font-mono text-sm">
          <span className="text-ink-dim">user</span> {login.username} · <span className="text-ink-dim">password</span> <span className="select-all">{login.password}</span>
        </div>
      )}
      {error && <p role="alert" className="m-0 break-words text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>}
    </>
  );
}

// BuiltInQbit is the bundled qBittorrent whose login Arrmada made: the Unraid all-in-one
// image's (builtIn: Arrmada runs it inside its own container) or the Compose install's
// (its own container, given a password by Arrmada). Nothing here needs setting up. Its own
// page is on the port the template or docker-compose.yml publishes (the copy-paste Compose
// file publishes none, so then there's no link), behind that login, which an admin can show.
// docker-compose.yml publishes it on the server's loopback only unless .env says otherwise
// (local): then the card says how to reach it instead of a link no other computer can open.
function BuiltInQbit({ port, builtIn, local, admin }: { port?: string; builtIn: boolean; local: boolean; admin: boolean }) {
  const href = port && !local ? bundledSABLink(port, window.location) : "";
  return (
    <div className="flex flex-col gap-1 text-sm">
      {builtIn ? (
        <p className="m-0 text-ink-dim">Built into this container: Arrmada starts it, connects to it and keeps its port and folders in step. Switch it off in the container’s settings to use your own.</p>
      ) : (
        <p className="m-0 text-ink-dim">Installed with Arrmada: it signs in with a password Arrmada made, and Arrmada keeps its port and folders in step.</p>
      )}
      {href && (
        <div className="break-words">
          <span className="text-ink-dim">qBittorrent’s own page:</span>{" "}
          <a href={href} target="_blank" rel="noreferrer" className="break-all font-mono font-semibold" style={{ color: "var(--accent-text)" }}>{href} ↗</a>
        </div>
      )}
      {port && local && (
        <p className="m-0 break-words text-ink-dim">
          qBittorrent’s own page answers on this server only, at <span className="font-mono">http://127.0.0.1:{port}</span>. To open it
          from another computer, add <span className="font-mono">ARRMADA_QBIT_WEBUI_BIND=0.0.0.0</span> to .env and run{" "}
          <span className="font-mono">docker compose up -d arrmada-qbittorrent</span>.
        </p>
      )}
      {admin && <ShowLogin app="qBittorrent" />}
    </div>
  );
}

// CategoryLine names the categories Arrmada files downloads under in every client. They're
// fixed: each importer reads only its own, so they aren't a setting.
export function CategoryLine({ c, usenet }: { c: DownloadCategories; usenet?: boolean }) {
  return (
    <p className="m-0 text-xs text-ink-dim" title="Arrmada files every download under one of these, so each importer finds its own.">
      Categories in {usenet ? "qBittorrent and SABnzbd" : "qBittorrent"}: <span className="font-mono">{c.movies}</span> (movies) · <span className="font-mono">{c.tv}</span> · <span className="font-mono">{c.books}</span> · <span className="font-mono">{c.music}</span>
    </p>
  );
}
