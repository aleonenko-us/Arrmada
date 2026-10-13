import { useState } from "react";
import { Button, Modal } from "../../ui";

// ScanDialog asks how a library scan should catalog the films it finds. By default they're
// added unmonitored, so Arrmada never searches for or upgrades them; monitoring them on a
// profile lets the upgrade sweep replace their files. Films already in the library get the
// file found either way.
export function ScanDialog({ profiles, onScan, onClose }: {
  profiles: { key: string; name: string }[];
  onScan: (opts: { monitor: boolean; quality_profile?: string }) => void;
  onClose: () => void;
}) {
  const [monitor, setMonitor] = useState(false);
  const [profile, setProfile] = useState(""); // "" = the default profile
  return (
    <Modal
      onClose={onClose}
      size="sm"
      title="Catalog existing films"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="primary" onClick={() => onScan(monitor ? { monitor: true, quality_profile: profile } : { monitor: false })}>Scan library</Button>
        </>
      }
    >
      <p className="m-0 text-base text-ink-dim">
        Arrmada adds the films it finds in your movies folder, and gives films already in your library the file it finds for them.
      </p>
      <label className="mt-4 flex items-start gap-2.5 text-base">
        <input type="checkbox" className="mt-0.5" checked={monitor} onChange={(e) => setMonitor(e.target.checked)} />
        <span>
          Monitor them so they can be upgraded
          <span className="mt-0.5 block text-sm text-ink-faint">Unmonitored films are never searched for or upgraded — Arrmada only catalogs them.</span>
        </span>
      </label>
      {monitor && (
        <label className="mt-3 flex flex-wrap items-center gap-2 text-sm">
          <span className="font-mono text-2xs uppercase text-ink-faint">Profile</span>
          <select value={profile} onChange={(e) => setProfile(e.target.value)} className="rounded-lg px-2.5 py-1.5 text-sm" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
            <option value="">Default profile</option>
            {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
          </select>
        </label>
      )}
    </Modal>
  );
}
