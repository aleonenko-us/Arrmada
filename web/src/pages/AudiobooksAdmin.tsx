import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { LINKS } from "../lib/links";
import { api, type AudioListening, type AudioServerAdmin } from "../lib/api";
import { Card, DailyBars, DaysPicker, Loading, Sessions, card, danger, fmtAgo, fmtHours, inputStyle } from "./audiobooks/shared";

// The admin half of Audiobooks: who may connect and everyone's listening (how much and
// when, never what). It is its own chunk, loaded only when an admin opens one of these
// tabs, so requesters never download it. The server's own settings live in Settings →
// Audiobook server, and the Audiobookshelf import in Settings → Import & migrate.

export type AdminTab = "people" | "listening";

export function AudiobooksAdmin({ tab }: { tab: AdminTab }) {
  if (tab === "people") return <PeopleView />;
  return <AllListeningView />;
}

function useAdmin() {
  const [data, setData] = useState<AudioServerAdmin | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = () => api.audioServer().then(setData).catch((e) => setError((e as Error).message));
  useEffect(() => { load(); }, []);
  return { data, setData, error, load };
}

function PeopleView() {
  const { data, error, load } = useAdmin();
  if (!data) return <Loading error={error} />;
  const eligible = data.users.filter((u) => u.eligible).length;
  const allowed = data.users.filter((u) => u.eligible && u.allowed).length;
  const withPassword = data.users.filter((u) => u.eligible && u.has_password).length;
  return (
    <div className="flex flex-col gap-4">
      {/* Who may connect is managed with everything else about a person. */}
      <Card title="Who can connect">
        <p className="m-0 text-[12.5px] text-ink-dim">
          {allowed} of {eligible} {eligible === 1 ? "person" : "people"} may connect a listening app; {withPassword} {withPassword === 1 ? "has" : "have"} set an audiobook password.{" "}
          <Link to={LINKS.users} className="font-semibold" style={{ color: "var(--accent)" }}>Manage access in Settings → People &amp; access</Link>
        </p>
      </Card>
      <Card title="Signed-in devices">
        {data.devices.length === 0 ? <div className="text-[12px] text-ink-faint">No devices yet.</div> : data.devices.map((d) => (
          <div key={d.id} className="flex items-center justify-between gap-3 py-1.5 text-[12.5px]" style={{ borderTop: "1px solid var(--line-soft)" }}>
            <div className="min-w-0">
              <div className="truncate"><b>{d.username}</b> · {d.device || d.client || "App"}</div>
              <div className="text-[11px] text-ink-faint">Signed in {fmtAgo(d.created_at)} · last used {fmtAgo(d.last_used_at)}</div>
            </div>
            <button onClick={() => api.revokeAudioDevice(d.id).then(load)} className="flex-none rounded-lg px-2.5 py-1 text-[11.5px] font-semibold" style={danger}>Sign out</button>
          </div>
        ))}
      </Card>
    </div>
  );
}

function AllListeningView() {
  const [days, setDays] = useState(30);
  const [who, setWho] = useState(0);
  const [data, setData] = useState<AudioListening | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.audioListening(days).then(setData).catch((e) => setError((e as Error).message)); }, [days]);
  const perDay = useMemo(() => {
    const m = new Map<string, number>();
    for (const d of data?.daily ?? []) if (!who || d.user_id === who) m.set(d.day, (m.get(d.day) ?? 0) + d.seconds);
    return m;
  }, [data, who]);
  if (!data) return <Loading error={error} />;
  const sessions = who ? data.sessions.filter((s) => s.user_id === who) : data.sessions;
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[11.5px] text-ink-faint">How much and when people listen — never what. Nothing in Arrmada, logs included, records which book anyone plays.</p>
      <div className="overflow-x-auto rounded-xl" style={card}>
        <table className="w-full text-[12.5px]">
          <thead><tr className="text-left text-[11px] text-ink-faint"><th className="p-2.5">Person</th><th className="p-2.5">Today</th><th className="p-2.5">7 days</th><th className="p-2.5">30 days</th><th className="p-2.5">All time</th><th className="p-2.5">Last listened</th></tr></thead>
          <tbody>
            {data.totals.length === 0 ? <tr><td colSpan={6} className="p-3 text-ink-faint">No listening yet.</td></tr> : data.totals.map((t) => (
              <tr key={t.user_id} onClick={() => setWho(who === t.user_id ? 0 : t.user_id)} className="cursor-pointer" style={{ borderTop: "1px solid var(--line-soft)", background: who === t.user_id ? "var(--accent-soft)" : undefined }}>
                <td className="p-2.5 font-semibold">{t.username || `#${t.user_id}`}</td><td className="p-2.5">{fmtHours(t.today)}</td><td className="p-2.5">{fmtHours(t.week)}</td>
                <td className="p-2.5">{fmtHours(t.month)}</td><td className="p-2.5">{fmtHours(t.all_time)}</td><td className="p-2.5 text-ink-dim">{fmtAgo(t.last_listen)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Card
        title="Listening per day"
        right={
          <div className="flex flex-none gap-2">
            <select value={who} onChange={(e) => setWho(Number(e.target.value))} className="rounded-lg px-2 py-1 text-[12px]" style={inputStyle}>
              <option value={0}>Everyone</option>
              {data.totals.map((t) => <option key={t.user_id} value={t.user_id}>{t.username || `#${t.user_id}`}</option>)}
            </select>
            <DaysPicker days={days} setDays={setDays} />
          </div>
        }
      >
        <DailyBars since={data.since} days={data.days} perDay={perDay} />
        <div className="mb-1 mt-4 text-[12.5px] font-bold">Sessions</div>
        <Sessions list={sessions} showUser={!who} />
      </Card>
    </div>
  );
}
