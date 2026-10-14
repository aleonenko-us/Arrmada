import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Section, input, inputStyle } from "../../components/settings/ui";
import { LINKS } from "../../lib/links";
import { api, type AlertCatalog, type AlertEvent, type AlertOptions, type AlertPreset, type NotificationConn, type NotificationInput } from "../../lib/api";
import { alertsApi } from "../../lib/alertsApi";
import { insightsApi } from "../../lib/insightsApi";
import { ago, until } from "../../lib/taskTime";
import { isAdmin, useMe } from "../../lib/me";
import { useQuery, invalidate } from "../../lib/query";
import { useUnsavedGuard } from "../../lib/useUnsavedGuard";
import { pushSupported, subscribeThisDevice, thisDeviceEndpoint } from "../../lib/webpush";
import { Button, useConfirm, useToast } from "../../ui";
import { OutgoingEmail } from "./OutgoingEmail";

// Settings → Alerts: where the owner's own alerts go (a Discord channel, a phone, an
// inbox). Requesters' "your request is ready" messages are separate — each person sets
// those up from the bell on Discover.
//
// A saved link is never sent back to the browser (it usually holds a token or a
// password): the field shows a hint, and leaving it blank keeps what's saved, like the
// Plex token. Managers see the list; only admins change it.
//
// "This device" is the other kind of connection: Web Push to the admin's own browsers
// and phones, no outside service needed.
//
// "Add alert" first asks where alerts should go. Discord, Telegram, ntfy, Pushover and
// email are short forms (the server builds the Apprise link from them); anything else is
// "Custom (Apprise URL)", which is also how every connection made before the forms shows.

const PUSH_NAME = "This device";

export function AlertsSettings() {
  const { user, booksEnabled, musicEnabled } = useMe();
  const admin = isAdmin(user);
  const { data: conns, error } = useQuery("notifications:list", alertsApi.notifications);
  const { data: catalog, error: catalogError } = useQuery("notifications:catalog", alertsApi.alertCatalog, { staleMs: 5 * 60_000 });
  const presetsQ = useQuery("notifications:presets", alertsApi.alertPresets, { staleMs: 10 * 60_000 });
  // If the forms can't be loaded, a raw Apprise link still can be added.
  const presets = presetsQ.data ?? (presetsQ.error ? [CUSTOM_PRESET] : undefined);
  // null: not adding; "choose": the "Where should alerts go?" tiles; else the chosen form.
  const [adding, setAdding] = useState<null | "choose" | AlertPreset>(null);
  // The list and the delivery logs: a save, a Test or a delete changes both.
  const reload = () => { invalidate("notifications:list"); invalidate("notifications:deliveries"); };

  // Events of a module that's switched off are hidden (a connection keeps them ticked).
  const shown = catalog && {
    ...catalog,
    events: catalog.events.filter((e) => (e.module !== "music" || musicEnabled) && (e.module !== "books" || booksEnabled)),
  };
  const blank = (): NotificationConn => ({ name: "", kind: "", enabled: true, events: (catalog?.events ?? []).filter((e) => e.default_on).map((e) => e.key) });
  const failed = error ?? catalogError;
  const toast = useToast();
  const [pushBusy, setPushBusy] = useState(false);
  const myPush = conns?.find((c) => c.kind === "webpush" && c.config?.user_id === user?.id);

  // Turn on push here, then make the connection if this admin hasn't one yet (one
  // connection reaches every device they've turned push on for).
  const addPush = async () => {
    setPushBusy(true);
    try {
      await subscribeThisDevice();
      if (!myPush) {
        await alertsApi.createNotification({ name: PUSH_NAME, kind: "webpush", enabled: true, events: blank().events });
        toast("Push alerts are on for this device", { tone: "good" });
      } else {
        toast(`This device now gets “${myPush.name}” alerts`, { tone: "good" });
      }
      reload();
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setPushBusy(false); }
  };

  return (
    <div className="flex flex-col gap-6">
      <Section id="alerts" title="Alerts" subtitle="Get a message when something needs you or when new things arrive. Each connection is one place a message goes, with its own choice of events.">
        {!admin && <p className="text-sm text-ink-faint">Only an admin can add or change alert connections.</p>}
        {failed && !(conns && shown) ? (
          <p className="text-sm" style={{ color: "var(--reject-text)" }}>Couldn’t load alert connections — {failed.message}</p>
        ) : !conns || !shown ? (
          <p className="text-sm text-ink-dim">Loading…</p>
        ) : (
          <>
            {conns.length === 0 && !adding && <p className="text-sm text-ink-dim">No alert connections yet.</p>}
            {conns.map((c) => <ConnCard key={c.id} conn={c} preset={presetOf(presets, c)} catalog={shown} readOnly={!admin} onChange={reload} />)}
            {admin && (adding === "choose" ? (
              <PresetChooser
                presets={presets}
                push={pushSupported()}
                pushBusy={pushBusy}
                onPick={(p) => { if (p.kind === "webpush") { setAdding(null); void addPush(); } else setAdding(p); }}
                onCancel={() => setAdding(null)}
              />
            ) : adding ? (
              <ConnCard
                key={adding.kind}
                conn={{ ...blank(), kind: adding.kind, name: adding.kind === "custom" ? "" : adding.label, preset: adding.kind }}
                preset={adding}
                catalog={shown}
                isNew
                onChange={() => { setAdding(null); reload(); }}
                onCancel={() => setAdding(null)}
              />
            ) : (
              <div className="flex flex-wrap items-center gap-2">
                <Button onClick={() => setAdding("choose")}>+ Add alert</Button>
              </div>
            ))}
            {admin && <p className="text-xs text-ink-faint">On iPhone, push works only when Arrmada is added to the Home Screen.</p>}
          </>
        )}
      </Section>
      {/* The mail account Send to Kindle goes out through: a login, so admins only. */}
      {admin && <OutgoingEmail />}
    </div>
  );
}

// The tile for a raw Apprise link, used when the preset list can't be loaded.
const CUSTOM_PRESET: AlertPreset = {
  kind: "custom", label: "Custom (Apprise URL)", hint: "Any of Apprise's 80+ services as one link.",
  fields: [{ name: "url", label: "Apprise URL", secret: true, required: true }],
};

// presetOf is the form a saved connection is edited with: its preset when it was made
// from one, "This device" for push, and Custom for a raw link (every older connection).
function presetOf(presets: AlertPreset[] | undefined, c: NotificationConn): AlertPreset | undefined {
  const kind = c.preset || (c.kind === "webpush" ? "webpush" : "custom");
  return presets?.find((p) => p.kind === kind) ?? (kind === "custom" ? CUSTOM_PRESET : undefined);
}

// usesFields: the preset is a short form the server builds the link from (not Custom's
// raw link, not push).
function usesFields(p: AlertPreset | undefined): p is AlertPreset {
  return !!p && p.kind !== "custom" && p.kind !== "webpush" && p.fields.length > 0;
}

// PresetChooser is "Where should alerts go?": one tile per destination.
function PresetChooser({ presets, push, pushBusy, onPick, onCancel }: { presets?: AlertPreset[]; push: boolean; pushBusy: boolean; onPick: (p: AlertPreset) => void; onCancel: () => void }) {
  const tiles = (presets ?? []).filter((p) => p.kind !== "webpush" || push);
  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="mb-2.5 flex items-center justify-between gap-2">
        <span className="text-md font-semibold">Where should alerts go?</span>
        <Button variant="ghost" size="sm" onClick={onCancel}>Cancel</Button>
      </div>
      {!presets ? (
        <p className="text-sm text-ink-dim">Loading…</p>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {tiles.map((p) => (
            <button
              key={p.kind}
              type="button"
              onClick={() => onPick(p)}
              disabled={p.kind === "webpush" && pushBusy}
              className="flex min-w-0 flex-col gap-0.5 rounded-lg p-3 text-left"
              style={{ background: "var(--panel)", border: "1px solid var(--line)" }}
            >
              <span className="text-base font-semibold">{p.kind === "webpush" && pushBusy ? "Turning on…" : p.label}</span>
              <span className="text-xs text-ink-faint">{p.hint}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// initialValues is a preset form's starting values: what's saved, with every secret blank
// (the server never sends one back; blank means "keep the saved one").
function initialValues(p: AlertPreset | undefined, conn: NotificationConn): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of p?.fields ?? []) {
    const v = conn.fields?.[f.name];
    out[f.name] = !f.secret && typeof v === "string" ? v : "";
  }
  return out;
}

function ConnCard({ conn, preset, catalog, isNew, readOnly, onChange, onCancel }: { conn: NotificationConn; preset?: AlertPreset; catalog: AlertCatalog; isNew?: boolean; readOnly?: boolean; onChange: () => void; onCancel?: () => void }) {
  const [c, setC] = useState<NotificationConn>(conn);
  // Only what's typed here; blank on a saved card means "keep the saved link".
  const [url, setUrl] = useState("");
  const fieldMode = usesFields(preset) && conn.kind !== "webpush";
  const [start, setStart] = useState(() => initialValues(preset, conn));
  const [values, setValues] = useState<Record<string, string>>(start);
  // Bumped on each save so the form starts over: typed secrets are cleared and show as
  // "•••• saved" again.
  const [saves, setSaves] = useState(0);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const toast = useToast();
  const confirm = useConfirm();
  const set = (patch: Partial<NotificationConn>) => setC((p) => ({ ...p, ...patch }));
  const dirty = JSON.stringify(values) !== JSON.stringify(start);
  // Anything typed or changed on the card and not yet saved (or a new card not yet added).
  const edited = dirty || url.trim() !== "" || c.name !== conn.name || c.enabled !== conn.enabled || JSON.stringify(c.events) !== JSON.stringify(conn.events)
    || JSON.stringify(c.options ?? {}) !== JSON.stringify(conn.options ?? {});
  useUnsavedGuard(edited && !readOnly);
  const savedSecret = (name: string) => !isNew && conn.fields?.[`${name}_set`] === true;
  const body = (): NotificationInput => ({
    name: c.name, kind: c.kind, enabled: c.enabled, events: c.events, options: c.options ?? {},
    ...(fieldMode ? { fields: values } : url.trim() ? { url: url.trim() } : {}),
  });
  // What's still missing before the form can be saved: a required field that's empty
  // and not already saved.
  const missing = (): string | null => {
    if (!c.name.trim()) return "A name is needed";
    if (!fieldMode) return isNew && !url.trim() ? "A link is needed" : null;
    const f = preset?.fields.find((f) => f.required && !values[f.name]?.trim() && !(f.secret && savedSecret(f.name)));
    return f ? `${f.label} is needed` : null;
  };

  const save = async () => {
    const why = missing();
    if (why) { toast(why, { tone: "error" }); return; }
    setBusy("save");
    try {
      if (isNew) await alertsApi.createNotification(body());
      else await alertsApi.updateNotification(c.id!, body());
      setUrl("");
      if (fieldMode) {
        const secret = new Set(preset?.fields.filter((f) => f.secret).map((f) => f.name));
        const next = Object.fromEntries(Object.entries(values).map(([k, v]) => [k, secret.has(k) ? "" : v]));
        setValues(next);
        setStart(next);
        setSaves((n) => n + 1);
      }
      toast("Saved", { tone: "good" });
      onChange();
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(null); }
  };
  // What's typed is tried as typed (before saving), with saved secrets filled in on the
  // server; an untouched saved card is tested through what's stored, and logged.
  const test = async () => {
    const saved = !isNew && !!c.id && (fieldMode ? !dirty : !url.trim());
    if (!saved) {
      const why = missing();
      if (why && why !== "A name is needed") { toast(why, { tone: "error" }); return; }
    }
    setBusy("test");
    try {
      const r = saved ? await alertsApi.testSavedNotification(c.id!) : await alertsApi.testNotification({ ...body(), ...(c.id ? { id: c.id } : {}) });
      toast(r.ok ? "Test sent" : `Test failed — ${r.error || "no reason given"}`, { tone: r.ok ? "good" : "error" });
      if (saved) onChange(); // the test is in the card's log and status now
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(null); }
  };
  const del = async () => {
    if (!c.id) return;
    const ok = await confirm({ title: `Delete “${c.name}”?`, body: "Alerts stop going there. You can add it again later.", confirmLabel: "Delete", tone: "danger" });
    if (!ok) return;
    try { await alertsApi.deleteNotification(c.id); onChange(); } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };

  const placeholder = !isNew && conn.url_set ? `${conn.url_hint ?? "saved"} — saved, leave blank to keep` : "gotify://host/token";

  return (
    <div className="rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <div className="flex flex-wrap items-center gap-2">
        {!isNew && <StatusDot conn={conn} />}
        <input aria-label="Connection name" value={c.name} disabled={readOnly} onChange={(e) => set({ name: e.target.value })} placeholder="Name (e.g. My Discord)" className="min-w-[160px] flex-1 rounded-lg px-2.5 py-1.5 text-base" style={inputStyle} />
        {preset && <span className="rounded px-1.5 py-0.5 text-xs text-ink-dim" style={{ background: "var(--panel)" }}>{preset.label}</span>}
        <label className="flex cursor-pointer items-center gap-1.5 text-sm">
          <input type="checkbox" checked={c.enabled} disabled={readOnly} onChange={(e) => set({ enabled: e.target.checked })} /> Enabled
        </label>
      </div>
      {conn.kind === "webpush" ? (
        <PushDevices readOnly={readOnly} onChange={onChange} />
      ) : fieldMode && preset ? (
        <PresetForm
          key={saves}
          preset={preset}
          values={values}
          readOnly={readOnly}
          savedSecret={savedSecret}
          onChange={(name, v) => setValues((p) => ({ ...p, [name]: v }))}
        />
      ) : readOnly ? (
        <div className={`mt-2 ${input}`} style={inputStyle}>{conn.url_hint || "—"}</div>
      ) : (
        <>
          <input aria-label="Link" type="password" value={url} onChange={(e) => setUrl(e.target.value)} placeholder={placeholder} className={`mt-2 ${input}`} style={inputStyle} autoComplete="off" />
          <p className="mt-1 text-xs text-ink-faint">
            An <a href="https://github.com/caronc/apprise/wiki" target="_blank" rel="noreferrer" style={{ color: "var(--accent-text)" }}>Apprise link</a> — e.g. <code>discord://id/token</code>, <code>tgram://token/chatid</code>, <code>ntfy://topic</code>, <code>mailtos://user:pass@host</code>.
          </p>
        </>
      )}
      {conn.invalid_reason && (
        <p className="mt-1 text-xs" style={{ color: "var(--avoid-text)" }}>The saved link no longer passes the check ({conn.invalid_reason}). It still sends; enter a corrected link to edit this connection’s link.</p>
      )}
      <EventPicker catalog={catalog} value={c.events} readOnly={readOnly} onChange={(events) => set({ events })} />
      {c.events.some((e) => STREAM_EVENTS.has(e)) && (
        <StreamFilters value={c.options ?? {}} readOnly={readOnly} onChange={(options) => set({ options })} />
      )}
      <MessageTemplates
        catalog={catalog}
        events={c.events}
        value={c.options?.templates ?? {}}
        readOnly={readOnly}
        onChange={(templates) => set({ options: { ...(c.options ?? {}), templates: Object.keys(templates).length ? templates : undefined } })}
      />
      {!isNew && c.id ? <DeliveryLog id={c.id} catalog={catalog} /> : null}
      {!readOnly && (
        <div className="mt-3 flex items-center gap-2">
          <Button variant="primary" size="sm" onClick={save} busy={busy === "save"} busyLabel="Saving…" disabled={busy !== null}>{isNew ? "Add" : "Save"}</Button>
          <Button size="sm" onClick={test} busy={busy === "test"} busyLabel="Testing…" disabled={busy !== null}>Test</Button>
          <span className="flex-1" />
          {isNew ? <Button variant="ghost" size="sm" onClick={onCancel}>Cancel</Button> : <Button variant="ghost" size="sm" onClick={del} style={{ color: "var(--reject-text)" }}>Delete</Button>}
        </div>
      )}
    </div>
  );
}

// PresetForm is a preset's fields with its one-line hint. A saved secret shows as
// "•••• saved" with Replace; until it's replaced, the server keeps the saved one.
function PresetForm({ preset, values, readOnly, savedSecret, onChange }: {
  preset: AlertPreset; values: Record<string, string>; readOnly?: boolean;
  savedSecret: (name: string) => boolean; onChange: (name: string, value: string) => void;
}) {
  const [replacing, setReplacing] = useState<Record<string, boolean>>({});
  return (
    <div className="mt-2">
      <p className="mb-2 text-xs text-ink-faint">{preset.hint}</p>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {preset.fields.map((f) => {
          const kept = f.secret && savedSecret(f.name) && !replacing[f.name];
          const label = `${f.label}${f.required ? "" : " (optional)"}`;
          return (
            <label key={f.name} className="flex min-w-0 flex-col gap-1">
              <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">{label}</span>
              {kept ? (
                <span className="flex items-center gap-2 text-sm text-ink-dim">
                  <span>•••• saved</span>
                  {!readOnly && (
                    <button type="button" className="text-xs font-semibold" style={{ color: "var(--accent-text)" }} onClick={() => setReplacing((r) => ({ ...r, [f.name]: true }))}>
                      Replace
                    </button>
                  )}
                </span>
              ) : f.options ? (
                <select aria-label={f.label} value={values[f.name] || f.options[0]} disabled={readOnly} onChange={(e) => onChange(f.name, e.target.value)} className={input} style={inputStyle}>
                  {f.options.map((o) => <option key={o} value={o}>{o}</option>)}
                </select>
              ) : (
                <span className="flex items-center gap-2">
                  <input
                    aria-label={f.label}
                    type={f.secret ? "password" : "text"}
                    value={values[f.name] ?? ""}
                    disabled={readOnly}
                    onChange={(e) => onChange(f.name, e.target.value)}
                    placeholder={f.placeholder}
                    autoComplete={f.secret ? "new-password" : "off"}
                    className={`min-w-0 flex-1 ${input}`}
                    style={inputStyle}
                  />
                  {f.secret && savedSecret(f.name) && !readOnly && (
                    <button type="button" className="flex-none text-xs" style={{ color: "var(--accent-text)" }} onClick={() => { onChange(f.name, ""); setReplacing((r) => ({ ...r, [f.name]: false })); }}>
                      Keep saved
                    </button>
                  )}
                </span>
              )}
              {f.hint && <span className="text-xs text-ink-faint">{f.hint}</span>}
            </label>
          );
        })}
      </div>
    </div>
  );
}

// PushDevices is a push connection's in place of a link: it reaches every device its
// admin turned push on for, and says whether this browser is one of them.
function PushDevices({ readOnly, onChange }: { readOnly?: boolean; onChange: () => void }) {
  const [here, setHere] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const endpoint = await thisDeviceEndpoint();
        const on = endpoint ? (await api.pushStatus(endpoint)).subscribed : false;
        if (alive) setHere(on);
      } catch { if (alive) setHere(false); }
    })();
    return () => { alive = false; };
  }, []);
  const turnOn = async () => {
    setBusy(true);
    try { await subscribeThisDevice(); setHere(true); onChange(); } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-ink-dim">
      <span>Web Push to every device the account turned push on for.</span>
      {pushSupported() && here === false && (
        <>
          <span style={{ color: "var(--avoid-text)" }}>No devices subscribed on this browser.</span>
          {!readOnly && <Button size="sm" onClick={turnOn} busy={busy} busyLabel="Turning on…">Turn on here</Button>}
        </>
      )}
      {here === true && <span style={{ color: "var(--good-text)" }}>This device gets them.</span>}
    </div>
  );
}

// StatusDot is the card's last-delivery light: green when the last alert went through,
// red when it failed (amber while a retry is still due), grey before anything was sent.
// The reason is in the tooltip and spelled out beside it, so it works on a phone too.
function StatusDot({ conn }: { conn: NotificationConn }) {
  const s = conn.last_status ?? "";
  const color = s === "sent" ? "var(--good)" : s === "failed" ? "var(--reject)" : s === "retrying" ? "var(--avoid)" : "var(--ink-faint)";
  const text = s === "sent" ? `Last alert sent ${ago(conn.last_sent_at)}`
    : s === "failed" ? `Last alert failed: ${conn.last_error || "no reason given"}`
    : s === "retrying" ? `Last try failed, retrying: ${conn.last_error || "no reason given"}`
    : "Nothing sent yet";
  return (
    <span className="flex w-full min-w-0 items-center gap-1.5 text-xs text-ink-faint" title={text}>
      <span aria-hidden className="h-2 w-2 flex-none rounded-full" style={{ background: color }} />
      <span className="truncate" style={s === "failed" ? { color: "var(--reject-text)" } : undefined}>{text}</span>
    </span>
  );
}

// DeliveryLog is the "Recent deliveries" expander: what was sent, when, and how it went.
// It loads only when opened.
function DeliveryLog({ id, catalog }: { id: number; catalog: AlertCatalog }) {
  const [open, setOpen] = useState(false);
  const { data, error } = useQuery(`notifications:deliveries:${id}`, () => alertsApi.alertDeliveries(id), { enabled: open, staleMs: 0 });
  const label = (key: string) => key === "test" ? "Test" : catalog.events.find((e) => e.key === key)?.label ?? key;
  return (
    <div className="mt-3">
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="text-xs font-semibold" style={{ color: "var(--accent-text)" }}>
        {open ? "Hide recent deliveries" : "Recent deliveries"}
      </button>
      {open && (
        <div className="mt-1.5 flex flex-col gap-1">
          {error && !data ? <p className="text-xs" style={{ color: "var(--reject-text)" }}>Couldn’t load deliveries — {error.message}</p>
            : !data ? <p className="text-xs text-ink-faint">Loading…</p>
            : data.length === 0 ? <p className="text-xs text-ink-faint">Nothing sent yet.</p>
            : data.map((d) => (
              <div key={d.id} className="flex flex-wrap items-baseline gap-x-2 text-xs">
                <span className="w-[72px] flex-none text-ink-faint">{ago(d.sent_at || d.created_at)}</span>
                <span className="font-semibold">{label(d.event_key)}</span>
                <span className="min-w-0 flex-1 truncate text-ink-dim">{d.body}</span>
                <span style={{ color: d.status === "sent" ? "var(--good-text)" : d.status === "failed" ? "var(--reject-text)" : "var(--ink-faint)" }}>
                  {d.status === "queued" && d.attempts > 0 ? `retry ${until(d.next_attempt_at)}` : d.status}
                </span>
                {d.last_error && d.status !== "sent" && <span className="w-full pl-[80px] text-xs" style={{ color: "var(--reject-text)" }}>{d.last_error}</span>}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

// MessageTemplates is "Customise message": this connection's own wording per event it
// sends, with placeholders. Blank keeps the standard message. Closed until asked for.
function MessageTemplates({ catalog, events, value, readOnly, onChange }: {
  catalog: AlertCatalog; events: string[]; value: Record<string, string>; readOnly?: boolean;
  onChange: (t: Record<string, string>) => void;
}) {
  const [open, setOpen] = useState(false);
  const shown = catalog.events.filter((e) => events.includes(e.key));
  if (shown.length === 0) return null;
  const custom = shown.filter((e) => value[e.key]?.trim()).length;
  const setOne = (key: string, text: string) => {
    const next = { ...value };
    if (text.trim()) next[key] = text; else delete next[key];
    onChange(next);
  };
  const placeholders = (catalog.placeholders ?? ["message", "title", "user", "year", "media", "link"]).map((p) => `{${p}}`).join(" ");
  return (
    <div className="mt-3">
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="text-xs font-semibold" style={{ color: "var(--accent-text)" }}>
        {open ? "Hide message wording" : `Customise message${custom ? ` (${custom} changed)` : ""}`}
      </button>
      {open && (
        <div className="mt-2 flex flex-col gap-2.5">
          <p className="m-0 text-xs text-ink-faint">
            Write this connection’s own message for any event, or leave it blank for the standard one. Placeholders: <code>{placeholders}</code>.
            {!catalog.links && (
              <> <code>{"{link}"}</code> stays blank until the <Link to={LINKS.publicURL} style={{ color: "var(--accent-text)" }}>outside address</Link> is set.</>
            )}
          </p>
          {shown.map((ev) => (
            <label key={ev.key} className="flex min-w-0 flex-col gap-1">
              <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">{ev.label}</span>
              <textarea
                value={value[ev.key] ?? ""}
                disabled={readOnly}
                rows={2}
                maxLength={1000}
                placeholder={catalog.template ?? "{message}\n{link}"}
                onChange={(e) => setOne(ev.key, e.target.value)}
                className={`${input} resize-y`}
                style={inputStyle}
              />
            </label>
          ))}
        </div>
      )}
    </div>
  );
}

// The Plex events the stream filters apply to.
const STREAM_EVENTS = new Set(["plex.stream.started", "plex.buffering"]);

// StreamFilters narrows a connection's Plex stream alerts: whose streams, and only remote
// ones or only transcodes. The people are the Plex users Insights has seen; ticking none
// means everyone. (A binge of autoplayed episodes is one alert, and a stalling stream one
// buffering alert per quarter hour, whatever is set here.)
function StreamFilters({ value, readOnly, onChange }: { value: AlertOptions; readOnly?: boolean; onChange: (o: AlertOptions) => void }) {
  const { data: users, error } = useQuery("insights:users", insightsApi.insightsUsers, { staleMs: 5 * 60_000 });
  const picked = new Set(value.stream_users ?? []);
  const setUsers = (ids: string[]) => onChange({ ...value, stream_users: ids.length ? ids : undefined });
  const toggle = (id: string, on: boolean) => {
    const next = new Set(picked);
    if (on) next.add(id); else next.delete(id);
    setUsers([...next]);
  };
  // A saved id Insights no longer lists (its history was cleared) still shows, so it can be unticked.
  const known = new Set((users ?? []).map((u) => u.id));
  const gone = [...picked].filter((id) => !known.has(id));
  return (
    <fieldset className="mt-3 min-w-0 rounded-lg p-3" style={{ border: "1px solid var(--line-soft)" }}>
      <legend className="px-1 font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">Plex stream alerts</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        <label className="flex cursor-pointer items-center gap-1.5 text-sm">
          <input type="checkbox" checked={!!value.remote_only} disabled={readOnly} onChange={(e) => onChange({ ...value, remote_only: e.target.checked || undefined })} />
          Only remote streams
        </label>
        <label className="flex cursor-pointer items-center gap-1.5 text-sm">
          <input type="checkbox" checked={!!value.transcode_only} disabled={readOnly} onChange={(e) => onChange({ ...value, transcode_only: e.target.checked || undefined })} />
          Only transcodes
        </label>
      </div>
      <div className="mt-2.5">
        <div className="mb-1 flex items-center justify-between gap-2">
          <span className="text-xs text-ink-faint">{picked.size === 0 ? "Everyone’s streams — tick people to hear about only theirs." : `Only ${picked.size === 1 ? "this person’s" : "these people’s"} streams:`}</span>
          {!readOnly && picked.size > 0 && (
            <button type="button" onClick={() => setUsers([])} className="text-xs" style={{ color: "var(--accent-text)" }}>Everyone</button>
          )}
        </div>
        {error && !users ? (
          <p className="text-xs" style={{ color: "var(--reject-text)" }}>Couldn’t load the Plex users — {error.message}</p>
        ) : !users ? (
          <p className="text-xs text-ink-faint">Loading…</p>
        ) : users.length === 0 && gone.length === 0 ? (
          <p className="text-xs text-ink-faint">No Plex users yet — they appear once Insights has seen someone watch.</p>
        ) : (
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {users.map((u) => (
              <label key={u.id} className="flex cursor-pointer items-center gap-1.5 text-sm">
                <input type="checkbox" checked={picked.has(u.id)} disabled={readOnly} onChange={(e) => toggle(u.id, e.target.checked)} />
                {u.username || u.id}
              </label>
            ))}
            {gone.map((id) => (
              <label key={id} className="flex cursor-pointer items-center gap-1.5 text-sm text-ink-dim">
                <input type="checkbox" checked disabled={readOnly} onChange={() => toggle(id, false)} />
                Plex user {id}
              </label>
            ))}
          </div>
        )}
      </div>
    </fieldset>
  );
}

// EventPicker is the grouped checklist of catalog events, with a tick-all per group.
// Groups with no events (yet) aren't shown.
function EventPicker({ catalog, value, readOnly, onChange }: { catalog: AlertCatalog; value: string[]; readOnly?: boolean; onChange: (events: string[]) => void }) {
  const on = new Set(value);
  const toggle = (keys: string[], tick: boolean) => {
    const next = new Set(on);
    for (const k of keys) { if (tick) next.add(k); else next.delete(k); }
    onChange([...next].sort());
  };
  const byGroup = catalog.groups
    .map((g) => ({ ...g, events: catalog.events.filter((e) => e.group === g.key) }))
    .filter((g) => g.events.length > 0);

  return (
    <div className="mt-3 grid gap-3 sm:grid-cols-2">
      {byGroup.map((g) => {
        const keys = g.events.map((e) => e.key);
        const all = keys.every((k) => on.has(k));
        return (
          <fieldset key={g.key} className="min-w-0">
            <legend className="mb-1 flex w-full items-center justify-between gap-2">
              <span className="font-mono text-2xs font-bold uppercase tracking-[0.1em] text-ink-faint">{g.label}</span>
              {!readOnly && keys.length > 1 && (
                <button type="button" onClick={() => toggle(keys, !all)} className="text-xs" style={{ color: "var(--accent-text)" }}>{all ? "None" : "All"}</button>
              )}
            </legend>
            <div className="flex flex-col gap-1">
              {g.events.map((ev: AlertEvent) => (
                <label key={ev.key} className="flex cursor-pointer items-start gap-1.5 text-sm" title={ev.hint}>
                  <input type="checkbox" aria-label={ev.label} className="mt-0.5" checked={on.has(ev.key)} disabled={readOnly} onChange={(e) => toggle([ev.key], e.target.checked)} />
                  <span className="min-w-0">
                    <span className="block">{ev.label}</span>
                    <span className="block text-xs text-ink-faint">{ev.hint}</span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
        );
      })}
    </div>
  );
}
