import { Link } from "react-router-dom";
import { HealthList } from "../../components/system/HealthList";
import { TasksTable } from "../../components/system/TasksTable";
import { UpdateCheck } from "../../components/system/UpdateCheck";
import { api, type SystemDisk } from "../../lib/api";
import { fmtBytes } from "../../lib/disposal";
import { LINKS } from "../../lib/links";
import { useQuery } from "../../lib/query";
import { backupAge, versionLabel } from "../../lib/systemStatus";

// System → Status: is everything working, and what's running in the background. The
// first card says which build is running, how big the database is, when it was last
// backed up and how full each drive is; Updates says whether a newer release is out. Health lists every check with what's wrong and a
// link to fix it; Tasks lists every scheduled job with its last and next run and Run now.
// Both refresh themselves while the page is open. The ids are deep-link targets
// (LINKS.status, LINKS.tasks).
export function StatusSection() {
  return (
    <div className="flex flex-col gap-5">
      <SystemCard />
      <UpdatesCard />
      <section id="health" className="scroll-mt-20 rounded-xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <h2 className="m-0 text-md font-bold">Health</h2>
        <p className="mb-4 mt-0.5 text-sm text-ink-faint">
          Arrmada checks its folders, download clients, indexers and connections in the background, every minute or so. Anything wrong shows here and on the Dashboard, with a link to where it's fixed.
        </p>
        <HealthList />
      </section>
      <section id="tasks" className="scroll-mt-20 rounded-xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
        <h2 className="m-0 text-md font-bold">Tasks</h2>
        <p className="mb-4 mt-0.5 text-sm text-ink-faint">
          The jobs Arrmada runs on a schedule: searching for missing titles, importing finished downloads, backups and the rest. Run now starts one straight away; a task that fails three times in a row also shows under Health.
        </p>
        <TasksTable />
      </section>
    </div>
  );
}

// UpdatesCard is the update check, from the same /system/status answer as the System
// card (one request). A server without the check wired shows no card.
function UpdatesCard() {
  const { data: st } = useQuery("system:status", api.systemStatus, { staleMs: 30_000 });
  return st?.update ? <UpdateCheck update={st.update} /> : null;
}

function uptime(sec: number): string {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

// SystemCard is the build, the database and the drives.
function SystemCard() {
  const { data: st, error } = useQuery("system:status", api.systemStatus, { staleMs: 30_000 });
  const backup = st ? backupAge(st.last_backup?.at) : null;
  return (
    <section id="status" className="scroll-mt-20 rounded-xl p-5" style={{ background: "var(--panel)", border: "1px solid var(--line)" }}>
      <h2 className="m-0 text-md font-bold">System</h2>
      <p className="mb-4 mt-0.5 text-sm text-ink-faint">Which build is running, the database, and how much room each library has.</p>
      {!st ? (
        <p className="m-0 text-sm text-ink-dim">{error ? error.message : "Loading…"}</p>
      ) : (
        <div className="flex flex-col gap-4">
          <dl className="m-0 grid grid-cols-2 gap-x-6 gap-y-3 font-mono text-sm text-ink-dim sm:grid-cols-4">
            <Fact k="Version" v={versionLabel(st.version, st.commit)} />
            <Fact k="Running for" v={uptime(st.uptime_seconds)} title={`Started ${new Date(st.started_at).toLocaleString()} · ${st.go}`} />
            <Fact k="Database" v={fmtBytes(st.db_bytes)} title={st.data_dir} />
            <Fact
              k="Last backup"
              v={<Link to={LINKS.backups} style={{ color: backup?.stale ? "var(--avoid)" : "var(--ink)" }}>{backup?.text}</Link>}
              title={st.last_backup ? `${st.last_backup.file} · ${new Date(st.last_backup.at).toLocaleString()}` : "No backup yet"}
            />
          </dl>
          <div className="flex flex-col gap-3">
            {st.disks.map((d) => <DiskBar key={d.path} d={d} />)}
          </div>
        </div>
      )}
    </section>
  );
}

function Fact({ k, v, title }: { k: string; v: React.ReactNode; title?: string }) {
  return (
    <div title={title}>
      <dt className="text-xs uppercase tracking-[0.1em] text-ink-faint">{k}</dt>
      <dd className="m-0 mt-0.5 truncate text-ink">{v}</dd>
    </div>
  );
}

// DiskBar is one drive: how full it is, and every folder that lives on it.
function DiskBar({ d }: { d: SystemDisk }) {
  const names = [d.name, ...d.shared_with.map((s) => s.name)];
  const pct = Math.min(100, Math.max(0, d.used_pct));
  const tone = pct >= 95 ? "var(--reject)" : pct >= 85 ? "var(--avoid)" : "var(--accent)";
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 text-sm">
        <span className="font-semibold">{names.join(" · ")}</span>
        <span className="font-mono text-xs text-ink-dim">
          {d.measurable ? `${fmtBytes(d.free)} free of ${fmtBytes(d.total)}` : "can't be measured"}
        </span>
      </div>
      {d.measurable && (
        <div className="h-1.5 overflow-hidden rounded-full" style={{ background: "var(--panel-2)" }} role="progressbar" aria-label={`${names[0]} drive used`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
          <div className="h-full rounded-full" style={{ width: `${pct}%`, background: tone }} />
        </div>
      )}
      <span className="truncate font-mono text-xs text-ink-faint" title={[d.path, ...d.shared_with.map((s) => s.path)].join("\n")}>{d.path}</span>
    </div>
  );
}
