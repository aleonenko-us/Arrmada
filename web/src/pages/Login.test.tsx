// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { Login } from "./Login";
import { api } from "../lib/api";
import { ConfirmProvider, ToastProvider } from "../ui";
import { setupApi } from "../lib/setupApi";

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

  it("opens the setup wizard, not an admin form, on a fresh install", async () => {
    vi.spyOn(api, "status").mockResolvedValue(status({ needs_setup: true }));
    const session = vi.spyOn(setupApi, "startSession").mockResolvedValue({ expires_at: "" });
    vi.spyOn(api, "setupState").mockReturnValue(new Promise(() => {}));
    const admin = vi.spyOn(api, "setupAdmin");
    // main.tsx mounts these above the app.
    render(<ToastProvider><ConfirmProvider><Login /></ConfirmProvider></ToastProvider>);
    await waitFor(() => expect(session).toHaveBeenCalledOnce());
    expect(screen.queryByText("Create admin & continue")).toBeNull();
    expect(admin).not.toHaveBeenCalled();
  });

  it("still says the first account needs the home network when setup is blocked", async () => {
    vi.spyOn(api, "status").mockResolvedValue(status({ needs_setup: true, setup_blocked: true }));
    const session = vi.spyOn(setupApi, "startSession");
    render(<Login />);
    expect(await screen.findByText(/can only be made from your home network/)).toBeTruthy();
    expect(session).not.toHaveBeenCalled();
  });
});
