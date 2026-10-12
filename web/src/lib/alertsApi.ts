import { req, type AlertCatalog, type AlertDelivery, type AlertPreset, type NotificationConn, type NotificationInput } from "./api";

// Admin alert channels (Settings → Alerts). They live here rather than in api.ts so they only
// download with that page, not with a requester's first load.
export const alertsApi = {
  notifications: () =>
    req<{ notifications: NotificationConn[] }>("/api/v1/notifications").then((r) => r.notifications),
  alertCatalog: () => req<AlertCatalog>("/api/v1/notifications/catalog"),
  alertPresets: () => req<{ presets: AlertPreset[] }>("/api/v1/notifications/presets").then((r) => r.presets),
  alertDeliveries: (id: number, limit = 20) =>
    req<{ deliveries: AlertDelivery[] }>(`/api/v1/notifications/${id}/deliveries?limit=${limit}`).then((r) => r.deliveries),
  createNotification: (body: NotificationInput) =>
    req<NotificationConn>("/api/v1/notifications", { method: "POST", body: JSON.stringify(body) }),
  updateNotification: (id: number, body: NotificationInput) =>
    req<{ status: string }>(`/api/v1/notifications/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  deleteNotification: (id: number) =>
    req<void>(`/api/v1/notifications/${id}`, { method: "DELETE" }),
  testNotification: (body: NotificationInput) =>
    req<{ ok: boolean; error?: string }>("/api/v1/notifications/test", { method: "POST", body: JSON.stringify(body) }),
  /** Test a saved connection through its stored URL. */
  testSavedNotification: (id: number) =>
    req<{ ok: boolean; error?: string }>(`/api/v1/notifications/${id}/test`, { method: "POST" }),
};
