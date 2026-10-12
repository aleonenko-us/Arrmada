import { req } from "./api";

// Send to Kindle. Its own module, not lib/api.ts: only the lazy Kindle components (My
// shelf's book sheet, the Me page's Kindle card) import it, so a requester's first load
// doesn't carry it.

export interface KindleView {
  /** The person's Kindle address; "" when they haven't set one. */
  kindle_email: string;
  /** The address books are sent from, which Amazon must be told to accept. */
  from_address?: string;
  /** Outgoing email is set up on the server, so sending can work at all. */
  smtp_configured: boolean;
  /** The saved address isn't at a Kindle domain (saved anyway). */
  warning?: string;
}

export const kindleApi = {
  get: () => req<KindleView>("/api/v1/me/kindle"),
  set: (kindle_email: string) => req<KindleView>("/api/v1/me/kindle", { method: "PUT", body: JSON.stringify({ kindle_email }) }),
  /** Starts the send (202); the result arrives in the inbox. */
  send: (bookId: number) => req<{ status: string }>(`/api/v1/me/books/${bookId}/send-to-kindle`, { method: "POST" }),
};

// Amazon's page with the approved-senders list and each Kindle's address.
export const AMAZON_DEVICES_URL = "https://www.amazon.com/mycd";
