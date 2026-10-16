// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { RequestSheet } from "./RequestSheet";
import { api, type AuthUser, type MediaRequest } from "../lib/api";
import { qualityApi } from "../lib/qualityApi";
import { ConfirmProvider, ToastProvider } from "../ui";

// REQ-04: the sheet offers each viewer exactly the actions they may take, as plain
// buttons — staff decide, owners withdraw, followers stop following — and never more.

const me: { user: AuthUser | null } = { user: null };
vi.mock("../lib/me", async (orig) => ({ ...(await orig<typeof import("../lib/me")>()), useMe: () => me }));

const at = "2026-10-01 09:00:00";
const rq = (over: Partial<MediaRequest> = {}): MediaRequest => ({
  id: 5, media_type: "movie", tmdb_id: 1, title: "Heat", year: 1995, status: "pending",
  requested_by: 7, requested_by_name: "alice", note: "the director's cut please", available: false,
  tracking: { stage: "pending" }, created_at: at, updated_at: at, ...over,
});
const user = (id: number, role: AuthUser["role"]): AuthUser => ({ id, username: `u${id}`, role, auto_approve: false, created_at: at });

function open(r: MediaRequest) {
  vi.spyOn(api, "getRequest").mockResolvedValue({ request: r, client_health: { ok: true } });
  return render(
    <MemoryRouter>
      <ToastProvider>
        <ConfirmProvider>
          <RequestSheet requestId={r.id} initial={r} onChanged={() => {}} onClose={() => {}} />
        </ConfirmProvider>
      </ToastProvider>
    </MemoryRouter>,
  );
}
const has = (name: string) => screen.queryByRole("button", { name }) !== null;

beforeEach(() => {
  vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({
    profiles: [
      { key: "hd", name: "HD-1080p", media_type: "movie", built_in: true, is_default: true, summary: "" },
      { key: "uhd", name: "Ultra-HD 2160p", media_type: "movie", built_in: true, is_default: false, summary: "" },
    ],
    formats: [],
  });
  vi.spyOn(api, "seriesSeasons").mockResolvedValue([1, 2, 3, 4].map((n) => ({
    number: n, name: `Season ${n}`, episode_count: 8, have: n === 1 ? 8 : 0, aired: 8,
    state: n === 1 ? "in_library" as const : "requested" as const, requestable: false,
  })));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("RequestSheet", () => {
  it("lets staff approve with a chosen profile, and shows who asked and their note", async () => {
    me.user = user(1, "admin");
    const approve = vi.spyOn(api, "approveRequest").mockResolvedValue(rq({ status: "approved" }));
    open(rq());
    expect(await screen.findByText("alice")).toBeTruthy();
    expect(screen.getByText("the director's cut please")).toBeTruthy();
    expect(has("Decline…") && has("Delete")).toBe(true);
    expect(has("Withdraw") || has("Stop following")).toBe(false);
    const select = (await screen.findByRole("combobox")) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Ultra-HD 2160p" });
    expect(select.value).toBe("hd"); // the default, preselected
    fireEvent.change(select, { target: { value: "uhd" } });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await vi.waitFor(() => expect(approve).toHaveBeenCalledWith(5, { quality_profile: "uhd" }));
  });

  // REQ-08: declining asks for a reason first (a quick pick or typed), and sends it.
  it("asks for a reason before declining, and sends it", async () => {
    me.user = user(1, "manager");
    const decline = vi.spyOn(api, "declineRequest").mockResolvedValue({ status: "declined" });
    open(rq());
    fireEvent.click(await screen.findByRole("button", { name: "Decline…" }));
    expect(decline).not.toHaveBeenCalled();
    expect(screen.getByText(/Decline “Heat” for alice\?/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Couldn’t find a good copy" }));
    expect((screen.getByRole("textbox", { name: /Reason/ }) as HTMLTextAreaElement).value).toBe("Couldn’t find a good copy");
    fireEvent.click(screen.getByRole("button", { name: "Decline and tell them" }));
    await vi.waitFor(() => expect(decline).toHaveBeenCalledWith(5, "Couldn’t find a good copy"));
  });

  it("shows staff a re-request with the earlier reason, and who decided", async () => {
    me.user = user(1, "admin");
    open(rq({ rerequest: 1, decline_reason: "Not for now", note: "It's on sale" }));
    expect(await screen.findByText("Re-request")).toBeTruthy();
    expect(screen.getByText("Declined before: Not for now")).toBeTruthy();
    cleanup();
    open(rq({ status: "approved", tracking: { stage: "searching" }, decided_by_name: "sam", decided_at: Math.floor(Date.now() / 1000) - 7200 }));
    expect(await screen.findByText(/Approved by sam · /)).toBeTruthy();
  });

  it("tells a requester why their request was declined", async () => {
    me.user = user(7, "requester");
    open(rq({ relation: "owner", status: "declined", tracking: { stage: "declined" }, decline_reason: "Already on Netflix" }));
    expect(await screen.findByText("Already on Netflix")).toBeTruthy();
    expect(screen.queryByText("Re-request")).toBeNull();
  });

  it("offers the owner Withdraw on a pending request and nothing a staff member gets", async () => {
    me.user = user(7, "requester");
    open(rq({ relation: "owner" }));
    expect(await screen.findByRole("button", { name: "Withdraw" })).toBeTruthy();
    expect(has("Approve") || has("Decline") || has("Delete") || has("Stop following")).toBe(false);
  });

  it("offers a follower Stop following, and never another person's name", async () => {
    me.user = user(8, "requester");
    open(rq({ relation: "subscriber", requested_by: 0, requested_by_name: "", note: "" }));
    expect(await screen.findByRole("button", { name: "Stop following" })).toBeTruthy();
    expect(has("Withdraw") || has("Approve")).toBe(false);
    expect(screen.queryByText("alice")).toBeNull();
  });

  it("gives an owner nothing to withdraw once it's approved", async () => {
    me.user = user(7, "requester");
    open(rq({ relation: "owner", status: "approved", tracking: { stage: "searching" } }));
    await screen.findByText("Your request");
    expect(has("Withdraw")).toBe(false);
  });

  it("says what was asked for: a book's format, a show's seasons", async () => {
    me.user = user(1, "admin");
    open(rq({ media_type: "book", title: "Dune", author: "Frank Herbert", formats: "audiobook" }));
    expect(await screen.findByText("Listen")).toBeTruthy();
    cleanup();
    open(rq({ media_type: "series", title: "Silo", seasons: [1, 2, 3, 5] }));
    expect(await screen.findByText(/S1–3, S5/)).toBeTruthy();
    cleanup();
    open(rq({ media_type: "series", title: "Silo" }));
    expect(await screen.findByText(/All seasons/)).toBeTruthy();
  });

  // REQ-13: staff untick seasons to approve only part of a series request.
  it("lets staff trim a series request to the seasons left ticked", async () => {
    me.user = user(1, "admin");
    const approve = vi.spyOn(api, "approveRequest").mockResolvedValue(rq({ status: "approved" }));
    open(rq({ media_type: "series", title: "Silo", seasons: [2, 3, 4] }));
    const s3 = await screen.findByRole("checkbox", { name: "Season 3" });
    expect((s3 as HTMLInputElement).checked).toBe(true);
    fireEvent.click(s3);
    fireEvent.click(screen.getByRole("button", { name: "Approve S2, S4" }));
    await vi.waitFor(() => expect(approve).toHaveBeenCalledWith(5, { quality_profile: undefined, seasons: [2, 4] }));
  });

  // What approving would grab: staff see it, with Approve & grab for the profile it was
  // checked under; picking another profile marks it as for the old one and hides the grab.
  it("shows staff the release preview and approves with exactly that release", async () => {
    me.user = user(1, "admin");
    const { requestPreviewApi } = await import("../lib/requestPreviewApi");
    const grab = vi.spyOn(requestPreviewApi, "approveAndGrab").mockResolvedValue(rq({ status: "approved", approve_grab: { mode: "preview" } }));
    const refresh = vi.spyOn(requestPreviewApi, "refresh");
    const checkedAt = Math.floor(Date.now() / 1000) - 600;
    open(rq({ preview: {
      state: "done", checked_at: checkedAt, profile: "hd", stale: false, grabbable: true,
      result: { outcome: "found", reason: "grabbed", profile: "hd", returned: 9, matching: 4, eligible: 2, searched: 1, queries: 1, why: ["Highest resolution on offer"],
        releases: [{ title: "Heat.1995.1080p.BluRay.x264-GRP", indexer: "TL", summary: "1080p · BluRay", resolution: "1080p", source: "BluRay", size_gb: 12.4, seeders: 40, transport: "torrent" }] },
    } }));
    expect(await screen.findByText("Would grab")).toBeTruthy();
    expect(screen.getByText("Heat.1995.1080p.BluRay.x264-GRP")).toBeTruthy();
    expect(screen.getByText("40 seeders")).toBeTruthy();
    expect(screen.getByText(/Highest resolution on offer/)).toBeTruthy();
    expect(refresh).not.toHaveBeenCalled(); // fresh: opening it doesn't search again
    await screen.findByRole("option", { name: "Ultra-HD 2160p" });
    fireEvent.click(await screen.findByRole("button", { name: "Approve & grab this" }));
    await vi.waitFor(() => expect(grab).toHaveBeenCalledWith(5, { quality_profile: "hd", folder_id: undefined, preview_checked_at: checkedAt }));
  });

  it("marks a preview for another profile and offers to check again", async () => {
    me.user = user(1, "admin");
    open(rq({ preview: {
      state: "done", checked_at: Math.floor(Date.now() / 1000) - 60, profile: "hd", stale: false, grabbable: true,
      result: { outcome: "found", reason: "grabbed", profile: "hd", returned: 1, matching: 1, eligible: 1, searched: 1, queries: 1,
        releases: [{ title: "Heat.1995.1080p.BluRay.x264-GRP", indexer: "TL", summary: "", size_gb: 12, seeders: 4 }] },
    } }));
    const select = (await screen.findByRole("combobox")) as HTMLSelectElement;
    await screen.findByRole("button", { name: "Approve & grab this" });
    fireEvent.change(select, { target: { value: "uhd" } });
    expect(await screen.findByText("for HD-1080p")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Check for Ultra-HD 2160p" })).toBeTruthy();
    expect(has("Approve & grab this")).toBe(false);
    expect(has("Approve")).toBe(true);
  });

  it("checks a missing or old preview when staff open the request", async () => {
    me.user = user(1, "admin");
    const { requestPreviewApi } = await import("../lib/requestPreviewApi");
    const refresh = vi.spyOn(requestPreviewApi, "refresh").mockResolvedValue({});
    open(rq({ preview: { state: "queued", checked_at: 0, profile: "", stale: true, grabbable: false } }));
    await vi.waitFor(() => expect(refresh).toHaveBeenCalledWith(5, undefined));
    expect(await screen.findByText("Not checked yet")).toBeTruthy();
  });

  it("never shows a requester a preview", async () => {
    me.user = user(7, "requester");
    open(rq({ relation: "owner", preview: { state: "done", checked_at: 1, profile: "hd", stale: false, grabbable: true,
      result: { outcome: "found", reason: "grabbed", profile: "hd", returned: 1, matching: 1, eligible: 1, searched: 1, queries: 1,
        releases: [{ title: "Heat.1995.1080p.BluRay.x264-GRP", indexer: "TL", summary: "", size_gb: 12, seeders: 4 }] } } }));
    await screen.findByRole("button", { name: "Withdraw" });
    expect(screen.queryByText("Would grab")).toBeNull();
    expect(screen.queryByTestId("release-preview")).toBeNull();
    expect(has("Approve & grab this")).toBe(false);
  });

  it("approves a whole-show request as asked unless a season is unticked, and never with none", async () => {
    me.user = user(1, "admin");
    const approve = vi.spyOn(api, "approveRequest").mockResolvedValue(rq({ status: "approved" }));
    open(rq({ media_type: "series", title: "Silo" }));
    // The show's seasons, all ticked; the one on disk says so.
    expect(await screen.findByRole("checkbox", { name: /Season 4/ })).toBeTruthy();
    expect(screen.getByText("In library ✓")).toBeTruthy();
    for (const n of [1, 2, 3, 4]) fireEvent.click(screen.getByRole("checkbox", { name: new RegExp(`Season ${n}`) }));
    expect((screen.getByRole("button", { name: "Approve" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("checkbox", { name: /Season 4/ }));
    fireEvent.click(screen.getByRole("button", { name: "Approve S4" }));
    await vi.waitFor(() => expect(approve).toHaveBeenCalledWith(5, { quality_profile: undefined, seasons: [4] }));
  });
});
