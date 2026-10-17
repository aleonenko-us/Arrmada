import { req } from "./api";

// Send to Kindle email (Settings → Book server → Send to Kindle email). Its own module, not lib/api.ts:
// only the admin's settings card imports it, so none of it reaches a requester's first load.
//
// The server, the login and the password never come back: the view says whether each is
// saved and shows a masked hint. A blank field on save keeps what's saved.

export type MailSecurity = "starttls" | "tls" | "none";

export interface SMTPView {
  configured: boolean;
  host_set: boolean;
  host_hint?: string;
  port: number;
  security: MailSecurity;
  from: string;
  username_set: boolean;
  username_hint?: string;
  password_set: boolean;
}

export interface SMTPUpdate {
  host?: string;
  port?: number;
  security?: MailSecurity;
  username?: string;
  password?: string;
  from?: string;
  remove_login?: boolean;
}

export const mailApi = {
  get: () => req<SMTPView>("/api/v1/settings/smtp"),
  save: (u: SMTPUpdate) => req<SMTPView>("/api/v1/settings/smtp", { method: "PUT", body: JSON.stringify(u) }),
  clear: () => req<SMTPView>("/api/v1/settings/smtp", { method: "DELETE" }),
  test: (to: string) => req<{ ok: boolean; detail: string }>("/api/v1/settings/smtp/test", { method: "POST", body: JSON.stringify({ to }) }),
};

// The usual port for each security choice, which the card fills in when it changes.
export const DEFAULT_PORT: Record<MailSecurity, number> = { starttls: 587, tls: 465, none: 25 };
