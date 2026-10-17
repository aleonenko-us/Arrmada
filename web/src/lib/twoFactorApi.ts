import { req, type AuthUser } from "./api";

// Two-factor sign-in calls. They live here rather than in api.ts so they only download
// with the pieces that use them (the Me page's card, the sign-in page's code step and
// People & access), never with a requester's first load.

/** GET /me/2fa: never the secret. */
export interface TwoFactorState {
  enabled: boolean;
  enabled_at?: string;
  recovery_codes_left: number;
  /** Somebody knows a password for this account: two-factor protects that sign-in. */
  password_set: boolean;
  /** "Require two-factor for admins" is on and this is an admin. */
  required: boolean;
}

/** A new secret, shown once while setting up: its text, its link and its QR code. */
export interface TwoFactorSetup {
  secret: string;
  uri: string;
  /** The QR code's dark modules as an SVG path over a size×size grid. */
  qr: { size: number; path: string };
}

const post = <T>(path: string, body: unknown) => req<T>(path, { method: "POST", body: JSON.stringify(body) });

export const twoFactorApi = {
  state: () => req<TwoFactorState>("/api/v1/me/2fa"),
  /** Starts setting up (the password first): a new secret, not yet on. */
  setup: (password: string) => post<TwoFactorSetup>("/api/v1/me/2fa/setup", { password }),
  /** The first code from the new secret turns it on; the recovery codes are shown once. */
  confirm: (code: string) => post<{ recovery_codes: string[] }>("/api/v1/me/2fa/confirm", { code }),
  disable: (password: string, code: string) => post<{ enabled: false }>("/api/v1/me/2fa/disable", { password, code }),
  newRecoveryCodes: (password: string, code: string) => post<{ recovery_codes: string[] }>("/api/v1/me/2fa/recovery-codes", { password, code }),

  // The sign-in page's second step, with the ticket a right password answered.
  loginCode: (ticket: string, code: string, trustDevice: boolean) =>
    post<{ user: AuthUser }>("/api/v1/auth/login/code", { ticket, code, trust_device: trustDevice }),
  loginSetup: (ticket: string) => post<TwoFactorSetup>("/api/v1/auth/login/setup", { ticket }),
  loginSetupConfirm: (ticket: string, code: string) =>
    post<{ user: AuthUser; recovery_codes: string[] }>("/api/v1/auth/login/setup/confirm", { ticket, code }),

  /** Admin: turn off someone else's two-factor (lost phone and recovery codes). */
  reset: (userId: number) => post<{ reset: boolean }>(`/api/v1/users/${userId}/2fa/reset`, {}),
};
