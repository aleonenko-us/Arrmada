import { ago } from "./taskTime";

// How System → Status and the Dashboard's System card word the build and the last backup.

// versionLabel is "2026.10.08 (abc1234)": the short commit tells two builds of one day
// apart. Builds that don't know their commit (older images, outside a git checkout) show
// the version alone.
export function versionLabel(version: string, commit: string): string {
  return commit && commit !== "unknown" && !version.includes(commit) ? `${version} (${commit})` : version;
}

// A backup older than this is worth a second look (the nightly one runs every day).
export const BACKUP_STALE_MS = 48 * 3600 * 1000;

// backupAge is "2 h ago" or "never", and whether that's old enough to flag.
export function backupAge(at: string | null | undefined, now = Date.now()): { text: string; stale: boolean } {
  const ms = at ? Date.parse(at) : NaN;
  if (Number.isNaN(ms)) return { text: "never", stale: true };
  return { text: ago(ms, now), stale: now - ms > BACKUP_STALE_MS };
}
