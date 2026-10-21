import { useId } from "react";
import type { InsightsStream, StreamHealth } from "../../lib/api";
import { hwBadge } from "../../lib/plexHw";
import { IconButton, Modal } from "../../ui";
import { DECISION, RowServer, UserLink, fmtClock, fmtMbps, geoLabel } from "../../pages/insights/shared";

// A live stream in full: how it's going, why the server is converting it, and what the
// file is next to what the player gets. The Dashboard's Now playing and Insights → Activity
// both open it.

const HEALTH_TONE: Record<string, { dot: string; bg: string; text: string }> = {
  good: { dot: "var(--good)", bg: "var(--good-soft)", text: "var(--good-text)" },
  warn: { dot: "var(--avoid)", bg: "var(--avoid-soft)", text: "var(--avoid-text)" },
  bad: { dot: "var(--reject)", bg: "var(--reject-soft)", text: "var(--reject-text)" },
};

/** HealthLine is the one-line verdict with its coloured dot. */
export function HealthLine({ h, className = "" }: { h?: StreamHealth; className?: string }) {
  if (!h?.text) return null;
  const tone = HEALTH_TONE[h.level] ?? HEALTH_TONE.good;
  return (
    <div className={`flex items-start gap-1.5 text-xs ${className}`} style={{ color: tone.text }}>
      <span className="mt-[5px] h-1.5 w-1.5 flex-none rounded-full" style={{ background: tone.dot }} aria-hidden />
      <span className="min-w-0">{h.text}</span>
    </div>
  );
}

/** qualityLine is the short "what the file is → what's sent" for a card. */
export function qualityLine(s: InsightsStream): string {
  const src = shortVideo(s.video.src);
  const parts = [s.video.stream ? `${src} → ${s.video.stream}` : src];
  if (s.audio.stream) parts.push(`sound → ${s.audio.stream}`);
  parts.push(`${fmtMbps(s.bandwidth_kbps)} Mb/s`);
  parts.push(s.location?.toLowerCase() === "lan" || s.geo?.local ? "home network" : "remote");
  return parts.filter(Boolean).join(" · ");
}

// shortVideo trims the server's stream name to its first part: "4K DoVi/HDR10 (HEVC
// Main 10)" → "4K DoVi/HDR10".
function shortVideo(v: string): string {
  const i = v.indexOf(" (");
  return i > 0 ? v.slice(0, i) : v;
}

export function StreamDeepDive({ s, onClose }: { s: InsightsStream; onClose: () => void }) {
  const d = DECISION[s.decision] ?? DECISION.direct_play;
  const hw = hwBadge(s);
  const titleId = useId();
  const tone = HEALTH_TONE[s.health?.level ?? "good"] ?? HEALTH_TONE.good;
  const transcoder = hw
    ? [hw.label + (s.hw_title && hw.label !== "CPU" && s.server !== "jellyfin" ? ` · ${s.hw_title}` : ""),
       s.speed ? `${s.speed.toFixed(1)}× real time` : s.throttled ? "ahead, resting" : "",
       s.buffered_secs ? `${fmtClock(s.buffered_secs * 1000)} ready ahead` : ""].filter(Boolean).join(" · ")
    : "";
  const connection = [
    s.location?.toLowerCase() === "lan" || s.geo?.local ? "Home network" : "Remote",
    s.relayed ? "through Plex Relay" : "",
    s.secure ? "secure" : "",
  ].filter(Boolean).join(" · ");
  return (
    <Modal onClose={onClose} labelledBy={titleId} size="md">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id={titleId} className="m-0 truncate text-lg font-bold">{s.title}</h2>
          <p className="m-0 mt-0.5 flex items-center gap-1.5 text-sm text-ink-dim"><span>{s.subtitle}</span><RowServer server={s.server} /></p>
        </div>
        <IconButton label="Close" onClick={onClose} className="text-ink-faint hover:text-[var(--ink)]">✕</IconButton>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="rounded-full px-2.5 py-0.5 font-mono text-2xs font-bold uppercase" style={{ background: d.color, color: "var(--accent-ink)" }}>{d.label}</span>
        {hw && <span title={s.hw_title || undefined} className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--line)", color: "var(--ink-dim)" }}>{hw.label === "CPU" ? "On the CPU" : `GPU · ${hw.label}`}</span>}
        {hw?.fellBack && <span className="rounded-full px-2.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--avoid)", background: "var(--avoid-soft)", color: "var(--avoid-text)" }}>Fell back to CPU</span>}
      </div>

      {s.health?.text && (
        <div className="mb-3 flex items-start gap-2 rounded-lg p-3 text-sm" style={{ background: tone.bg, color: tone.text }}>
          <span className="mt-1.5 h-2 w-2 flex-none rounded-full" style={{ background: tone.dot }} aria-hidden />
          <span>{s.health.text}</span>
        </div>
      )}

      {s.reasons && s.reasons.length > 0 && (
        <div className="mb-3 rounded-lg p-3 text-sm" style={{ border: "1px solid var(--line)" }}>
          <div className="mb-1 font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Why the server is converting it</div>
          <ul className="m-0 list-none space-y-1 p-0 text-ink-dim">{s.reasons.map((r, i) => <li key={i}>• {r}</li>)}</ul>
        </div>
      )}

      <div className="mb-1 font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">The file → what the player gets</div>
      <div className="mb-3 grid gap-px overflow-hidden rounded-lg text-sm" style={{ background: "var(--line)" }}>
        <DDRow label="Video" a={s.video.src} b={s.video.stream} />
        <DDRow label="Audio" a={s.audio.src} b={s.audio.stream} />
        <DDRow label="Subtitles" a={s.subtitles?.src || "Off"} b={s.subtitles?.stream} />
        <DDRow label="Container" a={s.container.src} b={s.container.stream} />
        {transcoder && <DDRow label="Transcoder" a={transcoder} />}
      </div>

      <div className="mb-1 font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">Who and where</div>
      <div className="grid gap-px overflow-hidden rounded-lg text-sm" style={{ background: "var(--line)" }}>
        <div className="flex items-center justify-between gap-3 px-3 py-2" style={{ background: "var(--panel)" }}>
          <span className="font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">User</span>
          <UserLink id={s.user_id} name={s.user} className="text-right text-ink-dim" />
        </div>
        <DDRow label="Player" a={s.player || s.platform || "—"} />
        {s.device && <DDRow label="Device" a={s.device} />}
        <DDRow label="Connection" a={connection} />
        <DDRow label="Location" a={`${geoLabel(s.geo)}${s.ip ? ` · ${s.ip}` : ""}`} />
        <DDRow label="Bandwidth" a={`${fmtMbps(s.bandwidth_kbps)} Mb/s`} />
        <DDRow label="Progress" a={`${fmtClock(s.offset_ms)} / ${fmtClock(s.duration_ms)} (${s.progress_pct}%)`} />
      </div>
    </Modal>
  );
}

function DDRow({ label, a, b }: { label: string; a: string; b?: string }) {
  return (
    <div className="flex items-start justify-between gap-3 px-3 py-2" style={{ background: "var(--panel)" }}>
      <span className="mt-0.5 flex-none font-mono text-2xs font-bold uppercase tracking-wide text-ink-faint">{label}</span>
      <span className="min-w-0 text-right text-ink-dim">
        {a}{b ? <span style={{ color: "var(--avoid-text)" }}> → {b}</span> : null}
      </span>
    </div>
  );
}
