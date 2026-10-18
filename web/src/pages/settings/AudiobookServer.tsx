import { useEffect, useState } from "react";
import { AutoText, AutoToggle } from "../../components/settings/auto";
import { api, type AudioServerAdmin } from "../../lib/api";
import { useMe } from "../../lib/me";
import { useAutoSave } from "../../lib/useAutoSave";
import { useSetting } from "../../lib/useSettings";
import { useToast } from "../../ui";
import { Card, Loading, card, ghost, primary } from "../audiobooks/shared";
import { OutgoingEmail } from "./OutgoingEmail";

// Settings → Book server (admin): the switch for the server listening apps connect to,
// the address they use from outside and a day of request tracing (it used to be the
// Audiobooks page's Server tab; that page links here now), the KEPUB conversion for Kobo,
// then the mail account Send to Kindle goes out through (it used to be under Alerts). Who
// may connect is in Settings → People & access.
export function AudiobookServerSettings() {
  const [data, setData] = useState<AudioServerAdmin | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.audioServer().then(setData).catch((e: Error) => setError(e.message)); }, []);
  return (
    <div className="flex flex-col gap-6">
      {data ? <ServerView data={data} setData={setData} /> : <Loading error={error} />}
      <KepubCard />
      {/* Its own load: a server that can't be read doesn't hide the mail card. */}
      <OutgoingEmail />
    </div>
  );
}

// KepubCard is "Convert EPUB to KEPUB for Kobo": the Kobo download on My shelf and the
// book page, and the KEPUB link in the OPDS feeds. It belongs to the e-reader side of the
// Book server, not the audiobook server's switch, so it saves on its own.
function KepubCard() {
  const kepub = useSetting("books_kepub");
  return (
    <div id="kobo-kepub" className="scroll-mt-20">
      <Card title="Kobo">
        <AutoToggle
          label="Convert EPUB to KEPUB for Kobo"
          hint="Kobo's own reader shows real page numbers, reading stats and better typography for a KEPUB. Arrmada makes one from the EPUB when someone downloads it for a Kobo; the library file is never changed."
          save={kepub}
        />
      </Card>
    </div>
  );
}

function ServerView({ data, setData }: { data: AudioServerAdmin; setData: (d: AudioServerAdmin) => void }) {
  const { external } = useMe();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const toggle = async () => {
    setBusy(true);
    try { setData(await api.setAudioServer({ enabled: !data.enabled })); }
    catch (e) { toast((e as Error).message, { tone: "error" }); }
    finally { setBusy(false); }
  };
  // The outside address saves when the field is left (or on Enter), like every setting.
  const publicURL = useAutoSave(data.public_url, async (v) => { setData(await api.setAudioServer({ public_url: v.trim() })); }, {
    equal: (a, b) => a.trim() === b.trim(),
    validate: (v) => (v.trim() === "" || /^https?:\/\/[^\s/]+/i.test(v.trim()) ? null : "Use a full address, starting with https://"),
  });
  const state = data.enabled ? (data.running ? "Running" : "Switched on, not running") : "Switched off";
  const dot = data.enabled ? (data.running ? "var(--good)" : "var(--reject)") : "var(--ink-faint)";
  return (
    <div id="audiobook-server" className="flex scroll-mt-20 flex-col gap-4">
      <section className="flex items-center justify-between gap-3 rounded-xl p-4" style={card}>
        <div className="min-w-0">
          {/* Book server holds the mail card too, so say which server this switch is. */}
          <div className="text-sm text-ink-dim">Audiobook server</div>
          <div className="flex items-center gap-2 text-md font-bold"><span className="h-2 w-2 rounded-full" style={{ background: dot }} />{state}</div>
          <div className="text-sm text-ink-dim">
            {data.running ? `${data.items} audiobook${data.items === 1 ? "" : "s"} served${data.items_ready < data.items ? ` · reading chapters for ${data.items - data.items_ready}` : ""}.` : data.error ? data.error : "Listening apps can't connect while it's off."}
            {" "}Switching it off also turns off listening in Arrmada.
          </div>
        </div>
        <button onClick={toggle} disabled={busy} className="flex-none rounded-lg px-4 py-2 text-base font-semibold disabled:opacity-60" style={data.enabled ? ghost : primary}>{data.enabled ? "Switch off" : "Switch on"}</button>
      </section>
      <div id="audiobook-address" className="scroll-mt-20">
        <Card title="Address">
          {!external && <div className="text-sm text-ink-dim">At home, apps connect to <code>http://{window.location.hostname}:{data.host_port}</code> (port {data.host_port}, set by <code>ARRMADA_AUDIOBOOK_PORT</code> in .env).</div>}
          <div className="mt-2 mb-2 text-sm text-ink-dim">
            To listen away from home, point a Cloudflare Tunnel hostname (or another reverse proxy) at port {data.host_port} and put that address here. Only the audiobook server answers on that port, never the rest of Arrmada.
          </div>
          <AutoText label="Address away from home" save={publicURL} placeholder="https://books.example.com" />
        </Card>
      </div>
      <TraceCard data={data} setData={setData} />
      <Card title="How places are kept">
        <p className="m-0 text-sm text-ink-dim">
          Play sessions are saved as they happen and survive restarts. Moving forward is saved straight away. A big jump backwards is held until playback carries on from there for 30 seconds, so a glitch can't reset anyone to the start — and the person can confirm it sooner (Keep it now in the player, or Use this spot on the book). A jump to the very end of a book needs the same proof, so one bad report can't mark it finished. Older offline listening never replaces a newer place, and everyone can put back an earlier place — or a later spot an app sent that wasn't used, or one an app removed. A place an app sets without playing, and Arrmada's own web player, follow the same rules.
        </p>
      </Card>
    </div>
  );
}

// TraceCard switches on a day of logging every request listening apps make — how a new
// app that fails quietly gets debugged. Lines name the kind of call, never the book.
function TraceCard({ data, setData }: { data: AudioServerAdmin; setData: (d: AudioServerAdmin) => void }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const set = async (hours: number) => {
    setBusy(true);
    try { setData(await api.setAudioServer({ trace_hours: hours })); }
    catch (e) { toast((e as Error).message, { tone: "error" }); }
    finally { setBusy(false); }
  };
  const on = data.trace_until > 0; // the server answers 0 once it has run out
  const until = on ? new Date(data.trace_until).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
  return (
    <Card title="Trace app requests">
      <div className="flex items-center justify-between gap-3">
        <p className="m-0 min-w-0 text-sm text-ink-dim">
          {on
            ? <>Tracing until {until} — every request an app makes is logged (routes only, never which book).</>
            : "When an app connects but something doesn't work, switch this on and try again: for the next 24 hours every request apps make is logged on the Logs page (routes only, never which book). It switches itself off."}
        </p>
        <button onClick={() => set(on ? 0 : 24)} disabled={busy} className="flex-none rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-60" style={on ? ghost : primary}>{on ? "Stop" : "Trace for 24 hours"}</button>
      </div>
    </Card>
  );
}
