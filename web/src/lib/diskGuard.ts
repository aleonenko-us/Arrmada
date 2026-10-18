import { formatBytes } from "./format";

// Small helpers for the Download disk guard card (Settings → Downloads).

// shortBytes is formatBytes without a decimal once the number is 10 or more: "1.0 TB",
// "312 GB". The card shows a drive's size at a glance, not to the tenth of a gigabyte.
export function shortBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const s = formatBytes(n);
  const [num, unit] = s.split(" ");
  return Number(num) >= 10 ? `${Math.round(Number(num))} ${unit}` : s;
}

// sizeAndFree is the drive line under "Currently watching": "1.0 TB, 312 GB free", or
// null when the size isn't known.
export function sizeAndFree(total: number | undefined, free: number | undefined): string | null {
  if (!total || total <= 0) return null;
  return `${shortBytes(total)}, ${shortBytes(free ?? 0)} free`;
}

// measurePathProblem checks "Measure a different drive" before it's sent: empty clears it,
// anything else has to be a full path inside the container. The server checks the rest
// (that it exists, can be measured and isn't the downloads folder's own drive).
export function measurePathProblem(v: string): string | null {
  const p = v.trim();
  if (p === "" || p.startsWith("/")) return null;
  return "Enter a full path starting with /, like /cache.";
}
