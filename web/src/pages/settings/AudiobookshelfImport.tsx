import { useState } from "react";
import { Section, inputStyle } from "../../components/settings/ui";
import { api, type AudioImportPreview, type AudioImportResult } from "../../lib/api";

// AudiobookshelfImport (Settings → Import & migrate, admin): bring everyone's listening
// places and bookmarks over from an Audiobookshelf database. It used to be the Audiobooks
// page's Import tab; that address opens this card now. Nothing is saved until Import, and
// a place that's newer in Arrmada is kept.
export function AudiobookshelfImport() {
  const [preview, setPreview] = useState<AudioImportPreview | null>(null);
  const [result, setResult] = useState<AudioImportResult | null>(null);
  const [map, setMap] = useState<Record<string, number>>({});
  const [users, setUsers] = useState<{ id: number; username: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const upload = async (f: File) => {
    setBusy(true); setError(null); setResult(null);
    try {
      // The accounts a place can go to are read only once there's a file to match them
      // against, so opening Import & migrate doesn't ask the audiobook server anything.
      const [p, server] = await Promise.all([api.audioImportUpload(f), api.audioServer().catch(() => null)]);
      if (server) setUsers(server.users.filter((u) => u.eligible));
      setPreview(p);
      setMap(Object.fromEntries(p.users.map((u) => [u.abs_id, u.user_id])));
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  const apply = async () => {
    setBusy(true); setError(null);
    try { setResult(await api.audioImportApply(map)); setPreview(null); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  return (
    <Section
      id="audiobookshelf-import"
      title="Import from Audiobookshelf"
      subtitle={<>Bring everyone's listening places and bookmarks over. Upload Audiobookshelf's database — <code>absdatabase.sqlite</code> in its config folder (copy it while Audiobookshelf is stopped, or use a recent backup). People are matched by username and books by their folder, then title. Nothing is saved until you press Import, and a place that's newer in Arrmada is never replaced.</>}
    >
      <div>
        <input type="file" aria-label="Audiobookshelf database" accept=".sqlite,.db,application/octet-stream" disabled={busy} onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} className="max-w-full text-[12px]" />
      </div>
      {error && <div role="alert" className="text-[12.5px]" style={{ color: "var(--reject)" }}>{error}</div>}
      {busy && <div className="text-[12.5px] text-ink-dim">Working…</div>}
      {preview && (
        <div className="rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
          <div className="mb-2 text-[12.5px]">
            {preview.progress} saved place{preview.progress === 1 ? "" : "s"}, {preview.matched} on books Arrmada has · {preview.bookmarks} bookmark{preview.bookmarks === 1 ? "" : "s"}.
          </div>
          <div className="mb-1 text-[12px] font-semibold">People</div>
          {preview.users.map((u) => (
            <div key={u.abs_id} className="flex flex-wrap items-center justify-between gap-3 py-1 text-[12.5px]">
              <span className="min-w-0">{u.abs_username} <span className="text-ink-faint">· {u.progress_rows} place{u.progress_rows === 1 ? "" : "s"}</span></span>
              <select aria-label={`Arrmada account for ${u.abs_username}`} value={map[u.abs_id] ?? 0} onChange={(e) => setMap({ ...map, [u.abs_id]: Number(e.target.value) })} className="rounded-lg px-2 py-1 text-[12px]" style={inputStyle}>
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
            <button onClick={apply} disabled={busy} className="rounded-lg px-4 py-2 text-[12.5px] font-semibold disabled:opacity-60" style={{ background: "linear-gradient(150deg, var(--accent), var(--accent-deep))", color: "var(--accent-ink)" }}>Import</button>
          </div>
        </div>
      )}
      {result && (
        <div role="status" className="rounded-lg p-3 text-[12.5px]" style={{ border: "1px solid var(--good)" }}>
          Imported {result.imported} place{result.imported === 1 ? "" : "s"} and {result.bookmarks} bookmark{result.bookmarks === 1 ? "" : "s"}.
          {result.kept > 0 && ` Kept ${result.kept} place${result.kept === 1 ? "" : "s"} that were newer in Arrmada.`}
          {result.skipped > 0 && ` Skipped ${result.skipped} (person or book not matched).`}
        </div>
      )}
    </Section>
  );
}
