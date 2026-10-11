import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { LINKS } from "../lib/links";
import { api, type AudioImportPreview, type AudioImportResult, type AudioListening, type AudioServerAdmin } from "../lib/api";
import { Card, DailyBars, DaysPicker, Loading, Sessions, card, danger, fmtAgo, fmtHours, inputStyle, primary } from "./audiobooks/shared";

// The admin half of Audiobooks: who may connect, everyone's listening (how much and when,
// never what) and the Audiobookshelf import. It is its own chunk, loaded only when an admin
// opens one of these tabs, so requesters never download it. The server's own settings live
// in Settings → Audiobook server.

export type AdminTab = "people" | "listening" | "import";

export function AudiobooksAdmin({ tab }: { tab: AdminTab }) {
  if (tab === "people") return <PeopleView />;
  if (tab === "listening") return <AllListeningView />;
  return <ImportView />;
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

function ImportView() {
  const [preview, setPreview] = useState<AudioImportPreview | null>(null);
  const [result, setResult] = useState<AudioImportResult | null>(null);
  const [map, setMap] = useState<Record<string, number>>({});
  const [users, setUsers] = useState<{ id: number; username: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => { api.audioServer().then((d) => setUsers(d.users.filter((u) => u.eligible))).catch(() => {}); }, []);
  const upload = async (f: File) => {
    setBusy(true); setError(null); setResult(null);
    try {
      const p = await api.audioImportUpload(f);
      setPreview(p);
      setMap(Object.fromEntries(p.users.map((u) => [u.abs_id, u.user_id])));
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  const apply = async () => {
    setBusy(true); setError(null);
    try { setResult(await api.audioImportApply(map)); setPreview(null); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  return (
    <div className="flex flex-col gap-4">
      <Card title="Bring places over from Audiobookshelf">
        <p className="m-0 text-[12px] text-ink-dim">
          Upload Audiobookshelf's database — <code>absdatabase.sqlite</code> in its config folder (copy it while Audiobookshelf is stopped, or use a recent backup). People are matched by username and books by their folder, then title. Nothing is saved until you press Import, and a place that's newer in Arrmada is never replaced.
        </p>
        <div className="mt-3">
          <input type="file" accept=".sqlite,.db,application/octet-stream" disabled={busy} onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} className="text-[12px]" />
        </div>
      </Card>
      {error && <div className="text-[12.5px]" style={{ color: "var(--reject)" }}>{error}</div>}
      {busy && <div className="text-[12.5px] text-ink-dim">Working…</div>}
      {preview && (
        <section className="rounded-xl p-4" style={card}>
          <div className="mb-2 text-[12.5px]">
            {preview.progress} saved place{preview.progress === 1 ? "" : "s"}, {preview.matched} on books Arrmada has · {preview.bookmarks} bookmark{preview.bookmarks === 1 ? "" : "s"}.
          </div>
          <div className="mb-1 text-[12px] font-semibold">People</div>
          {preview.users.map((u) => (
            <div key={u.abs_id} className="flex items-center justify-between gap-3 py-1 text-[12.5px]">
              <span>{u.abs_username} <span className="text-ink-faint">· {u.progress_rows} place{u.progress_rows === 1 ? "" : "s"}</span></span>
              <select value={map[u.abs_id] ?? 0} onChange={(e) => setMap({ ...map, [u.abs_id]: Number(e.target.value) })} className="rounded-lg px-2 py-1 text-[12px]" style={inputStyle}>
                <option value={0}>Skip</option>
                {users.map((x) => <option key={x.id} value={x.id}>{x.username}</option>)}
              </select>
            </div>
          ))}
          {preview.unmatched_books && preview.unmatched_books.length > 0 && (
            <details className="mt-2 text-[12px] text-ink-dim">
              <summary className="cursor-pointer">{preview.unmatched_books.length} book{preview.unmatched_books.length === 1 ? "" : "s"} with progress that Arrmada doesn't have</summary>
              <div className="mt-1">{preview.unmatched_books.slice(0, 50).join(" · ")}</div>
            </details>
          )}
          <div className="mt-3 flex justify-end">
            <button onClick={apply} disabled={busy} className="rounded-lg px-4 py-2 text-[12.5px] font-semibold disabled:opacity-60" style={primary}>Import</button>
          </div>
        </section>
      )}
      {result && (
        <div className="rounded-xl p-4 text-[12.5px]" style={{ ...card, borderColor: "var(--good)" }}>
          Imported {result.imported} place{result.imported === 1 ? "" : "s"} and {result.bookmarks} bookmark{result.bookmarks === 1 ? "" : "s"}.
          {result.kept > 0 && ` Kept ${result.kept} place${result.kept === 1 ? "" : "s"} that were newer in Arrmada.`}
          {result.skipped > 0 && ` Skipped ${result.skipped} (person or book not matched).`}
        </div>
      )}
    </div>
  );
}
