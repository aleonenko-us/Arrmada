import { useEffect, useId, useState } from "react";
import { api, type FileDetails } from "../lib/api";
import { formatBytes } from "../lib/format";
import { IconButton, Modal } from "../ui";

// Everything known about one library file, in one place: what's on disk, what's inside
// it, and — the part that had no answer anywhere in the app — which torrent it came from.
export function FileDetailsModal({
  path,
  title,
  subtitle,
  onCleanTracks,
  onClose,
}: {
  path: string;
  title: string;
  subtitle?: string;
  // onCleanTracks queues this file for Convert, which brings it to the target spec —
  // a re-encode if the codec is wrong, a track rewrite if only the languages are.
  // Omitted where there's nothing sensible to queue.
  onCleanTracks?: () => Promise<void>;
  onClose: () => void;
}) {
  const [info, setInfo] = useState<FileDetails | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [cleaning, setCleaning] = useState(false);
  const [cleanMsg, setCleanMsg] = useState<string | null>(null);

  useEffect(() => {
    setInfo(null);
    setErr(null);
    api.fileInfo(path).then(setInfo).catch((e: Error) => setErr(e.message));
  }, [path]);

  const m = info?.media;
  const src = info?.source;
  const titleId = useId();

  return (
    <Modal onClose={onClose} labelledBy={titleId} size="lg">
        <div className="mb-4 flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="m-0 text-lg font-bold">{title}</h2>
            {subtitle && <div className="mt-0.5 text-sm text-ink-dim">{subtitle}</div>}
          </div>
          <IconButton
            label="Close"
            onClick={onClose}
            className="shrink-0 rounded-lg px-2.5 py-1 text-md"
            style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}
          >
            ✕
          </IconButton>
        </div>

        {err && (
          <p className="m-0 text-base" style={{ color: "var(--reject-text)" }}>
            {err}
          </p>
        )}
        {!info && !err && <p className="m-0 text-base text-ink-dim">Reading the file…</p>}

        {info && (
          <div className="flex flex-col gap-4">
            <Section label="File">
              <Row k="Name" v={info.name} mono />
              <Row k="Folder" v={info.dir} mono />
              {info.exists ? (
                <>
                  <Row k="Size" v={info.size_bytes > 0 ? formatBytes(info.size_bytes, 2) : "0 B"} />
                  <Row k="Modified" v={when(info.modified_ms)} />
                </>
              ) : (
                <Row k="Status" v={info.missing_reason ?? "not on disk"} tone="bad" />
              )}
            </Section>

            {m && (
              <Section label="Media">
                <Row
                  k="Video"
                  v={[
                    m.video_codec?.toUpperCase(),
                    m.width && m.height ? `${m.width}×${m.height}` : m.resolution,
                    m.hdr && m.hdr !== "SDR" ? m.hdr : null,
                    m.ten_bit ? "10-bit" : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                />
                <Row
                  k="Bitrate"
                  v={[
                    m.bitrate_kbps ? `${(m.bitrate_kbps / 1000).toFixed(1)} Mbps` : null,
                    m.frame_rate ? `${m.frame_rate.toFixed(3)} fps` : null,
                    m.vfr ? "variable" : null,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                />
                {m.duration_sec > 0 && <Row k="Duration" v={runtime(m.duration_sec)} />}
                <Row k="Container" v={m.container?.toUpperCase() || "—"} />
                {m.audio && m.audio.length > 0 && (
                  <Row
                    k="Audio"
                    v={m.audio
                      .map((a) =>
                        [a.codec?.toUpperCase(), channels(a.channels), a.lang || null]
                          .filter(Boolean)
                          .join(" "),
                      )
                      .join(" · ")}
                  />
                )}
                {m.subs && m.subs.length > 0 && (
                  <Row
                    k="Subtitles"
                    v={m.subs
                      .map((s) => `${(s.lang || "und").toUpperCase()} ${s.codec}${s.text ? "" : " (image)"}`)
                      .join(" · ")}
                  />
                )}
                {m.has_cc && <Row k="Captions" v="embedded CEA-608/708" />}
                {onCleanTracks && (m.subs?.length ?? 0) + (m.audio?.length ?? 0) > 2 && (
                  <div className="mt-2 border-t pt-2" style={{ borderColor: "var(--line)" }}>
                    <button
                      onClick={async () => {
                        setCleaning(true);
                        setCleanMsg(null);
                        try {
                          await onCleanTracks();
                          setCleanMsg("Queued — Convert will bring it to your target spec.");
                        } catch (e) {
                          setCleanMsg((e as Error).message);
                        } finally {
                          setCleaning(false);
                        }
                      }}
                      disabled={cleaning}
                      className="rounded-lg px-3 py-1.5 text-sm font-semibold disabled:opacity-50"
                      style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}
                    >
                      {cleaning ? "Queueing…" : "Bring to target"}
                    </button>
                    <span className="ml-2 text-xs text-ink-faint">
                      Applies your Convert target: codec, plus the audio and subtitle languages you keep.
                    </span>
                    {cleanMsg && <p className="m-0 mt-1.5 text-sm text-ink-dim">{cleanMsg}</p>}
                  </div>
                )}
              </Section>
            )}
            {info.media_note && <Note>{info.media_note}</Note>}

            <Section label="Where it came from">
              {src ? (
                <>
                  <Row k="Release" v={src.release || "—"} mono />
                  {src.from_pack && (
                    <Note>
                      This file arrived inside that release along with others — it wasn&apos;t grabbed on
                      its own.
                    </Note>
                  )}
                  {src.indexer && <Row k="Indexer" v={src.indexer} />}
                  {src.quality_profile && <Row k="Profile" v={src.quality_profile} />}
                  {src.manual && <Row k="Chosen" v="manually, from an interactive search" />}
                  {src.grabbed_ms ? <Row k="Grabbed" v={when(src.grabbed_ms)} /> : null}
                  {src.imported_ms ? <Row k="Imported" v={when(src.imported_ms)} /> : null}
                  {src.info_hash && <Row k="Info hash" v={src.info_hash} mono />}
                  <Row
                    k="Torrent"
                    v={
                      src.in_client
                        ? `still in the client — ${src.state}${src.ratio ? `, ratio ${src.ratio.toFixed(2)}` : ""}`
                        : "no longer in the download client"
                    }
                  />
                  {src.seed_enabled && (
                    <Row
                      k="Seed goal"
                      v={[
                        src.seed_ratio ? `ratio ${src.seed_ratio}` : null,
                        src.seed_hours ? `${src.seed_hours}h` : null,
                      ]
                        .filter(Boolean)
                        .join(" · ") || "enabled"}
                    />
                  )}
                  {src.source_path && <Row k="Downloaded to" v={src.source_path} mono />}
                </>
              ) : (
                <Note>{info.source_note || "No download record for this file."}</Note>
              )}
            </Section>
          </div>
        )}
    </Modal>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 font-mono text-2xs font-bold uppercase tracking-[0.12em] text-ink-faint">
        {label}
      </div>
      <div className="rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
        {children}
      </div>
    </div>
  );
}

function Row({ k, v, mono, tone }: { k: string; v: string; mono?: boolean; tone?: "bad" }) {
  if (!v) return null;
  return (
    <div className="flex gap-3 py-[3px] text-base">
      <span className="w-[104px] shrink-0 text-ink-faint">{k}</span>
      <span
        className={`min-w-0 flex-1 break-all ${mono ? "font-mono text-sm" : ""}`}
        style={tone === "bad" ? { color: "var(--reject-text)" } : undefined}
      >
        {v}
      </span>
    </div>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="m-0 py-1 text-sm text-ink-dim">{children}</p>;
}


function when(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

function runtime(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  return h > 0 ? `${h}h ${m}m` : `${m}m`;
}

function channels(n: number): string {
  if (n === 8) return "7.1";
  if (n === 6) return "5.1";
  if (n === 2) return "stereo";
  if (n === 1) return "mono";
  return `${n}ch`;
}
