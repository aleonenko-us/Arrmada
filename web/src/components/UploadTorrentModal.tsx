import { useId, useRef, useState } from "react";
import { type TorrentPreview } from "../lib/api";
import { formatBytes } from "../lib/format";
import { Button, IconButton, Modal } from "../ui";

// Base64-encode an ArrayBuffer in chunks (avoids the arg-count limit of String.fromCharCode
// on large torrents).
function toBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let bin = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

// UploadTorrentModal lets you drop a .torrent file for a title, preview what it resolves
// to (name / size / files), then grab it straight into the download client — for when you
// found a release yourself (e.g. on a private tracker) and search didn't surface it.
export function UploadTorrentModal({
  what,
  onPreview,
  onGrab,
  onClose,
  targets,
}: {
  what: string; // the movie/series title, for the heading
  onPreview: (torrentB64: string) => Promise<TorrentPreview>;
  // targetId is the chosen audiobook version (books with versions only); 0 = standard.
  onGrab: (torrentB64: string, filename: string, title: string, targetId: number) => Promise<void>;
  onClose: () => void;
  /** Extra audiobook versions to offer as "file as" choices (books only). */
  targets?: { id: number; label: string }[];
}) {
  const [target, setTarget] = useState(0);
  const [torrent, setTorrent] = useState<string>("");
  const [filename, setFilename] = useState("");
  const [preview, setPreview] = useState<TorrentPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [grabbed, setGrabbed] = useState(false);
  const [drag, setDrag] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const titleId = useId();

  const load = async (f: File) => {
    setError(null); setPreview(null); setGrabbed(false);
    if (!f.name.toLowerCase().endsWith(".torrent")) { setError("That's not a .torrent file."); return; }
    setBusy(true); setFilename(f.name);
    try {
      const b64 = toBase64(await f.arrayBuffer());
      setTorrent(b64);
      setPreview(await onPreview(b64));
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };

  const grab = async () => {
    if (!preview || !torrent) return;
    setBusy(true); setError(null);
    try { await onGrab(torrent, filename, preview.name, target); setGrabbed(true); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="lg">
        <div className="mb-1 flex items-center justify-between">
          <h2 id={titleId} className="m-0 text-[15px] font-bold">Upload torrent — {what}</h2>
          <IconButton label="Close" onClick={onClose} className="h-7 w-7 rounded-md text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
        </div>
        <p className="mb-3 text-[12px] text-ink-dim">Found a release yourself? Download the .torrent from your tracker (while logged in) and drop it here. Arrmada previews it so you can check it's right, then grabs it into your library.</p>

        <input ref={inputRef} type="file" accept=".torrent" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) load(f); }} />
        <div
          role="button"
          tabIndex={0}
          onClick={() => inputRef.current?.click()}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); inputRef.current?.click(); } }}
          onDragOver={(e) => { e.preventDefault(); setDrag(true); }}
          onDragLeave={() => setDrag(false)}
          onDrop={(e) => { e.preventDefault(); setDrag(false); const f = e.dataTransfer.files?.[0]; if (f) load(f); }}
          className="cursor-pointer rounded-xl p-6 text-center transition-colors"
          style={{ border: `1.5px dashed ${drag ? "var(--accent)" : "var(--line)"}`, background: drag ? "var(--accent-soft)" : "var(--panel-2)" }}
        >
          <div className="text-[13px] font-semibold">{filename || "Choose a .torrent file"}</div>
          <div className="mt-0.5 text-[11px] text-ink-faint">{busy && !preview ? "Reading…" : "Click to browse, or drag a .torrent here"}</div>
        </div>

        {error && <div className="mt-2 text-[12px]" style={{ color: "var(--reject)" }}>{error}</div>}

        {preview && (
          <div className="mt-3 rounded-lg p-3" style={{ border: "1px solid var(--line)", background: "var(--panel-2)" }}>
            <div className="text-[13px] font-semibold break-words">{preview.name}</div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-3 font-mono text-[10.5px] text-ink-faint">
              <span>{formatBytes(preview.size_bytes)}</span>
              {preview.files && preview.files.length > 0 && <span>{preview.files.length} file{preview.files.length === 1 ? "" : "s"}</span>}
            </div>
            {preview.files && preview.files.length > 0 && (
              <div className="thin-scroll mt-2 max-h-[160px] overflow-y-auto font-mono text-[10.5px] text-ink-dim">
                {preview.files.slice(0, 60).map((f, i) => (
                  <div key={i} className="flex justify-between gap-3 py-0.5"><span className="min-w-0 truncate" title={f.path}>{f.path}</span><span className="flex-none text-ink-faint">{formatBytes(f.size_bytes)}</span></div>
                ))}
                {preview.files.length > 60 && <div className="text-ink-faint">…and {preview.files.length - 60} more</div>}
              </div>
            )}
            <div className="mt-3 flex items-center gap-3">
              {grabbed ? (
                <span className="text-[12.5px] font-semibold" style={{ color: "var(--good)" }}>✓ Grabbed — follow it in Activity → Queue.</span>
              ) : (
                <>
                  {targets && targets.length > 0 && (
                    <select value={target} onChange={(e) => setTarget(Number(e.target.value))} title="Which audiobook the download is filed as" className="rounded-lg px-2 py-2 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
                      <option value={0}>File audio as Standard</option>
                      {targets.map((t) => <option key={t.id} value={t.id}>File audio as {t.label}</option>)}
                    </select>
                  )}
                  <Button variant="primary" onClick={grab} busy={busy} busyLabel="Grabbing…" className="disabled:opacity-50">Grab this</Button>
                </>
              )}
            </div>
          </div>
        )}
    </Modal>
  );
}
