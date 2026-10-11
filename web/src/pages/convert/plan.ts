import type { ConvertPlan, ConvertTrackDecision } from "../../lib/api";

// Words for a conversion plan, shared by the Library's plan card and the Convert-now
// confirmation, so both say the same thing about the same file.

export function fmtSize(b?: number): string {
  if (b === undefined || b === null) return "—";
  if (b <= 0) return "0 B";
  const tb = b / 1024 ** 4;
  if (tb >= 1) return `${tb.toFixed(2)} TB`;
  const gb = b / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(1)} GB`;
  const mb = b / 1024 ** 2;
  return mb >= 1 ? `${mb.toFixed(0)} MB` : `${(b / 1024).toFixed(0)} KB`;
}

const CHANNELS: Record<number, string> = { 1: "mono", 2: "stereo", 6: "5.1", 8: "7.1" };
const CODEC: Record<string, string> = {
  hdmv_pgs_subtitle: "PGS", dvd_subtitle: "VobSub", dvb_subtitle: "DVB", subrip: "SRT", mov_text: "MP4 text", webvtt: "WebVTT",
  truehd: "TrueHD", eac3: "E-AC-3", ac3: "AC-3", dts: "DTS", aac: "AAC", flac: "FLAC", opus: "Opus",
};

// trackLabel names a track the way a player's menu would: "ENG · TrueHD 7.1 · Commentary".
export function trackLabel(d: ConvertTrackDecision): string {
  const codec = CODEC[d.codec.toLowerCase()] ?? d.codec.toUpperCase();
  const parts = [d.lang && d.lang !== "und" ? d.lang.toUpperCase() : "untagged", codec];
  if (d.channels) parts[1] += ` ${CHANNELS[d.channels] ?? `${d.channels}ch`}`;
  if (d.forced) parts.push("forced");
  if (d.image) parts.push("image");
  if (d.title) parts.push(d.title);
  return parts.join(" · ");
}

// videoLine is the picture's before → after: "H.264 1080p 8-bit → HEVC 10-bit · CRF 18".
export function videoLine(p: ConvertPlan): string {
  const v = p.video;
  const hdr = (h?: string) => (h && h !== "SDR" ? ` ${h}` : "");
  const from = `${v.from_codec || "?"} ${v.from_res || ""}${hdr(v.from_hdr)} ${v.from_bits}-bit`.replace(/\s+/g, " ").trim();
  if (!v.to_codec) return `${from} — copied untouched`;
  return `${from} → ${v.to_codec}${hdr(v.to_hdr)} ${v.to_bits ?? v.from_bits}-bit${v.crf ? ` · CRF ${v.crf}` : ""}`;
}

// sizeLine is the file's size before and after: "70.0 GB → ~40.1 GB (estimated)".
export function sizeLine(p: ConvertPlan): string {
  if (!p.needs.save || p.needs.save <= 0) return fmtSize(p.size_bytes);
  return `${fmtSize(p.size_bytes)} → ~${fmtSize(p.video.est_bytes)} (${p.video.measured ? "measured by a test encode" : "estimated"})`;
}

function dropped(list: ConvertTrackDecision[]): ConvertTrackDecision[] {
  return list.filter((d) => !d.keep);
}

// planChanges is one line per change a conversion makes to the file, for the confirmation:
// the picture, each removed track with its reason, the container, then every warning.
export function planChanges(p: ConvertPlan): string[] {
  const out: string[] = [];
  if (p.video.to_codec) out.push(`Picture: ${videoLine(p)}`);
  out.push(`Size: ${sizeLine(p)}`);
  const audio = dropped(p.audio);
  if (audio.length) {
    out.push(`Audio: ${audio.length} of ${p.audio.length} track${p.audio.length === 1 ? "" : "s"} removed — ${audio.map((d) => `${trackLabel(d)} (${d.reason ?? "removed"})`).join("; ")}`);
  }
  const subs = dropped(p.subs);
  if (subs.length) {
    out.push(`Subtitles: ${subs.length} of ${p.subs.length} track${p.subs.length === 1 ? "" : "s"} removed — ${subs.map((d) => `${trackLabel(d)} (${d.reason ?? "removed"})`).join("; ")}`);
  }
  if (p.container.from && p.container.to && p.container.from !== p.container.to) out.push(`Container: ${p.container.from} → ${p.container.to}`);
  return out;
}
