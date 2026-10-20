import { req, type BackupFile, type BackupSchedule, type BackupsState, type LogEntry, type RecentJob, type RecycleItem, type RestoreStaged, type TaskStatus, type UpdateProgress, type UpdateStatus } from "./api";

// System calls: scheduled tasks, database backups and the recycle bins. They live here rather
// than in api.ts so they only download with the staff pages that use them, not with a
// requester's first load.
export const systemApi = {
  tasks: () => req<TaskStatus[]>("/api/v1/system/tasks"),
  runTask: (name: string) =>
    req<{ status: string; job_id: number; existing: boolean }>(`/api/v1/system/tasks/${encodeURIComponent(name)}/run`, {
      method: "POST",
    }),
  // Stops a queued or running job (a library scan). 409 when it has already finished.
  cancelJob: (id: number) => req<{ status: string; job_id: number }>(`/api/v1/jobs/${id}/cancel`, { method: "POST" }),
  // Recent jobs under the tasks: searches, scans, imports and Run nows, newest first.
  recentJobs: (limit = 50) => req<{ jobs: RecentJob[] }>(`/api/v1/system/jobs?limit=${limit}`).then((r) => r.jobs),
  // Database backups — admin only (a backup holds every secret the app has).
  backups: () => req<BackupsState>("/api/v1/system/backups"),
  // A manual database backup, taken synchronously.
  backupNow: () => req<BackupFile>("/api/v1/system/backups", { method: "POST" }),
  saveBackupSchedule: (p: Partial<BackupSchedule>) =>
    req<BackupSchedule>("/api/v1/system/backups/settings", { method: "PUT", body: JSON.stringify(p) }),
  deleteBackup: (name: string) =>
    req<{ status: string }>(`/api/v1/system/backups/${encodeURIComponent(name)}`, { method: "DELETE" }),
  // Stages a restore for the next start ("RESTORE" is the typed confirmation).
  restoreBackup: (name: string) =>
    req<RestoreStaged>(`/api/v1/system/backups/${encodeURIComponent(name)}/restore`, { method: "POST", body: JSON.stringify({ confirm: "RESTORE" }) }),
  cancelRestore: () => req<{ cancelled: boolean }>("/api/v1/system/backups/restore-pending", { method: "DELETE" }),
  recycleItems: () => req<{ items: RecycleItem[] }>("/api/v1/recycle/items").then((r) => r.items),
  // bin: one bin's key; omitted empties every bin.
  emptyRecycle: (bin?: string) => req<{ freed_bytes: number }>("/api/v1/recycle/empty", { method: "POST", body: JSON.stringify(bin ? { bin } : {}) }),
  restoreRecycle: (id: string) => req<{ status: string }>("/api/v1/recycle/restore", { method: "POST", body: JSON.stringify({ id }) }),
  deleteRecycleItem: (id: string) => req<{ status: string }>("/api/v1/recycle/delete", { method: "POST", body: JSON.stringify({ id }) }),
  // The application log (admin). after_seq asks only for lines newer than a cursor; the
  // answer's last_seq is the next cursor, and reset says the server restarted (start over).
  logs: (opts: LogQuery) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(opts)) if (v !== undefined && v !== "" && v !== 0) p.set(k, String(v));
    const qs = p.toString();
    return req<LogPage>(`/api/v1/logs${qs ? `?${qs}` : ""}`);
  },
  // The debug switch: a raised level always ends by itself (and on restart).
  logLevel: () => req<LogLevelState>("/api/v1/logs/level"),
  setLogLevel: (level: string, minutes?: number) =>
    req<LogLevelState>("/api/v1/logs/level", { method: "PUT", body: JSON.stringify({ level, minutes }) }),
  // The "Update available" check on Settings → Status: switch it on or off, or check now.
  setUpdateCheck: (enabled: boolean) =>
    req<UpdateStatus>("/api/v1/system/update-check", { method: "PUT", body: JSON.stringify({ enabled }) }),
  checkForUpdate: () => req<UpdateStatus>("/api/v1/system/update-check", { method: "POST" }),
  // Update now (needs the updater container), how it is going, and the automatic-updates switch.
  applyUpdate: () => req<UpdateProgress>("/api/v1/system/update/apply", { method: "POST" }),
  updateProgress: () => req<UpdateProgress>("/api/v1/system/update/progress"),
  setAutoUpdate: (enabled: boolean) =>
    req<UpdateStatus>("/api/v1/system/update/auto", { method: "PUT", body: JSON.stringify({ enabled }) }),
};

export interface LogQuery {
  limit?: number;
  level?: string;
  q?: string;
  hide?: string;
  module?: string;
  after_seq?: number;
}

export interface LogPage {
  entries: LogEntry[];
  /** The newest line the server holds; ask for after_seq=last_seq next time. */
  last_seq?: number;
  /** The cursor was from before a restart: entries is a fresh window, not an increment. */
  reset?: boolean;
  /** This run of the server; when it changes, line numbers started over. */
  run?: number;
  /** The parts of the app that tag their lines, for the module filter. */
  modules?: string[];
  /** How many lines the server keeps in memory (older ones are only in the files on disk). */
  capacity?: number;
}

export interface LogLevelState {
  level: string;
  /** The install's own level (ARRMADA_LOG_LEVEL), which a raised level returns to. */
  env_level: string;
  /** When a raised level ends; absent while at the install's level. */
  until?: string;
  max_minutes: number;
}
