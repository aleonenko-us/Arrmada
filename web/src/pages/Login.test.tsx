// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { Login } from "./Login";
import { api } from "../lib/api";

type Status = Awaited<ReturnType<typeof api.status>>;
const status = (over: Partial<Status>): Status =>
  ({ needs_setup: false, setup_blocked: false, plex_login: false, jellyfin_login: false, ...over }) as Status;

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Login page", () => {
  it("offers only the password form when no media server sign-in is configured", async () => {
    vi.spyOn(api, "status").mockResolvedValue(status({}));
    render(<Login />);
    await screen.findByLabelText(/password/i);
    expect(screen.queryByText(/Sign in with Plex/)).toBeNull();
    expect(screen.queryByText(/Sign in with Jellyfin/)).toBeNull();
  });

  it("offers Sign in with Plex when the server says it is usable", async () => {
    vi.spyOn(api, "status").mockResolvedValue(status({ plex_login: true }));
    render(<Login />);
    await waitFor(() => expect(screen.getByText(/Sign in with Plex/)).toBeTruthy());
    expect(screen.queryByText(/Sign in with Jellyfin/)).toBeNull();
  });
});
