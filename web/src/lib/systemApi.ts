import { req, type BackupFile, type BackupSchedule, type BackupsState, type RecycleItem, type RestoreStaged, type TaskStatus } from "./api";

// System calls: scheduled tasks, database backups and the recycle bins. They live here rather
// than in api.ts so they only download with the staff pages that use them, not with a
// requester's first load.
export const systemApi = {
  tasks: () => req<TaskStatus[]>("/api/v1/system/tasks"),
  runTask: (name: string) =>
    req<{ status: string; job_id: number; existing: boolean }>(`/api/v1/system/tasks/${encodeURIComponent(name)}/run`, {
      method: "POST",
    }),
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
};
