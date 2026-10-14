import type { TorrentPreview } from "./api";

// An uploaded .nzb's preview, read in the browser: the server's preview reads .torrent
// files only, and an NZB is plain XML that says what it holds — its files (by the quoted
// name in each file's subject) and their segments' sizes. Nothing is fetched or sent.

/** Whether an uploaded file is an NZB rather than a .torrent: by its name, else its start. */
export function looksLikeNZB(filename: string, head: string): boolean {
  if (filename.toLowerCase().endsWith(".nzb")) return true;
  // A torrent is a bencoded dictionary and always starts with "d"; an NZB is XML.
  return !head.startsWith("d") && /<nzb[\s>]/i.test(head.slice(0, 4096));
}

const unescape = (s: string) =>
  s.replace(/&quot;/g, '"').replace(/&apos;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");

// fileName is a file's name from its subject: the quoted part ("Film.2024.part01.rar"),
// else the whole subject.
function fileName(subject: string): string {
  const quoted = subject.match(/"([^"]+)"/);
  return (quoted ? quoted[1] : subject).trim();
}

/**
 * nzbPreview reads an NZB's name, total size and files. The name is the NZB's own
 * <meta type="name">, else the file's name without .nzb — what SABnzbd calls the job.
 * Throws when the text isn't an NZB with at least one file.
 */
export function nzbPreview(xml: string, filename: string): TorrentPreview {
  if (!/<nzb[\s>]/i.test(xml)) throw new Error("That's not an NZB file.");
  const files: { path: string; size_bytes: number }[] = [];
  const fileRe = /<file\b([^>]*)>([\s\S]*?)<\/file>/gi;
  for (let m = fileRe.exec(xml); m; m = fileRe.exec(xml)) {
    const subject = unescape(m[1].match(/subject\s*=\s*"([^"]*)"/i)?.[1] ?? m[1].match(/subject\s*=\s*'([^']*)'/i)?.[1] ?? "");
    let size = 0;
    const segRe = /<segment\b[^>]*\bbytes\s*=\s*["'](\d+)["']/gi;
    for (let s = segRe.exec(m[2]); s; s = segRe.exec(m[2])) size += Number(s[1]);
    files.push({ path: fileName(subject) || `file ${files.length + 1}`, size_bytes: size });
  }
  if (files.length === 0) throw new Error("That NZB lists no files.");
  const meta = xml.match(/<meta\b[^>]*\btype\s*=\s*["']name["'][^>]*>([^<]*)<\/meta>/i)?.[1];
  const name = (meta && unescape(meta).trim()) || filename.replace(/\.nzb$/i, "").trim() || "NZB upload";
  return { name, size_bytes: files.reduce((n, f) => n + f.size_bytes, 0), files };
}
