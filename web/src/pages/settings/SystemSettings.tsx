import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { AutoToggle } from "../../components/settings/auto";
import { Section } from "../../components/settings/ui";
import { api, type PendingRestart } from "../../lib/api";
import { busyLines, restartAppAndWait } from "../../lib/restart";
import { useSetting } from "../../lib/useSettings";
import { Backups } from "./Backups";

// Settings → System (admin only): which modules are on, restarting Arrmada and the
// database backups. The API keys live with the other integrations on Settings → Connections.
export function SystemSettings() {
  // A module switch shows in the nav and Discover as soon as it's saved.
  const books = useSetting("books_enabled");
  const music = useSetting("music_enabled");
  return (
    <div className="flex flex-col gap-6">
      <Section id="modules" title="Modules" subtitle="Turn modules on or off. Disabling hides a module from the navigation and from Discover — nothing is deleted, and it can be re-enabled anytime.">
        <AutoToggle label="Books" hint="Ebook and audiobook library, and the Books tab in Discover. Metadata comes from Hardcover when a key is set, otherwise Open Library." save={books} />
        <AutoToggle label="Music (preview)" hint="Artists and albums from MusicBrainz with automatic album downloads. Still being hardened. Turning it off hides Music and stops its searches and imports; finished downloads wait until it's back on. Nothing is deleted." save={music} />
      </Section>
      <RestartSection />
      <SetupAgainSection />
      <Backups />
    </div>
  );
}

// RestartSection restarts Arrmada on demand — the same restart the folder banner offers,
// for any change that only applies at startup — and points at the log.
function RestartSection() {
  const [st, setSt] = useState<PendingRestart | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = () => { api.pendingRestart().then(setSt).catch(() => setSt(null)); };
  useEffect(load, []);

  // Fresh numbers for the confirm: the page may have been open for hours.
  const openConfirm = () => { setErr(null); load(); setConfirming(true); };
  const restart = async () => {
    setRestarting(true); setErr(null);
    try { await restartAppAndWait(); } catch (e) { setErr((e as Error).message); setRestarting(false); }
  };
  const lines = st ? busyLines(st.busy) : [];

  return (
    <Section id="restart" title="Restart" subtitle="Restart Arrmada, for example after a change that only applies at startup. This page reloads once it's back, usually in a few seconds.">
      {st && !st.can_restart && (
        <p className="m-0 text-sm text-ink-dim">Arrmada can't restart itself here. Restart the Arrmada container the way you started it.</p>
      )}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
        {st?.can_restart && (
          <button onClick={openConfirm} className="rounded-lg px-4 py-2 text-base font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Restart now</button>
        )}
        <Link to="/logs" className="text-sm font-semibold" style={{ color: "var(--accent)" }}>Logs →</Link>
      </div>
      {confirming && (
        <ConfirmDialog
          title="Restart Arrmada now?"
          tone="accent"
          body={
            <div className="flex flex-col gap-1.5">
              <span>This page reloads once Arrmada is back, usually in a few seconds.</span>
              {lines.length > 0
                ? lines.map((l) => <span key={l} style={{ color: "var(--avoid)" }}>{l}</span>)
                : <span>Nothing is converting or making subtitles right now.</span>}
            </div>
          }
          confirmLabel="Restart now"
          busyLabel="Restarting…"
          busy={restarting}
          error={err}
          onConfirm={restart}
          onCancel={() => setConfirming(false)}
        />
      )}
    </Section>
  );
}

// SetupAgainSection reopens the first-run wizard with everything already set filled in;
// finishing it comes back here.
function SetupAgainSection() {
  return (
    <Section id="setup" title="Run setup again" subtitle="Walk through the first-run steps again: metadata key, folders, Plex and indexers. What's already set is filled in, and the last step shows what's still left to do.">
      <div>
        <Link to="/setup" className="inline-block rounded-lg px-4 py-2 text-base font-semibold no-underline" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>Run setup again</Link>
      </div>
    </Section>
  );
}
