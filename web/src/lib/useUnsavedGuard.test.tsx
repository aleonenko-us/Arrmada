// @vitest-environment jsdom
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { Link, Outlet, RouterProvider, createMemoryRouter, useNavigate } from "react-router-dom";
import { ConfirmProvider } from "../ui/Confirm";
import { UnsavedGuardHost, unsavedMessage, useUnsavedGuard } from "./useUnsavedGuard";

// The data router builds a Request for every navigation with jsdom's AbortSignal, which
// Node's Request refuses. Nothing here has a loader, so the signal can go.
beforeAll(() => {
  const NativeRequest = globalThis.Request;
  globalThis.Request = class extends NativeRequest {
    constructor(input: RequestInfo | URL, init?: RequestInit) { super(input, init && { ...init, signal: undefined }); }
  } as typeof Request;
});

afterEach(() => {
  cleanup();
  document.body.style.overflow = "";
});

// A form that's dirty once something is typed, with a Save that navigates away the way
// the Quality editor does: mark clean, then navigate in the same tick.
function Form() {
  const [text, setText] = useState("");
  const markClean = useUnsavedGuard(text !== "", "Discard your changes to this profile?");
  const navigate = useNavigate();
  return (
    <>
      <input aria-label="Name" value={text} onChange={(e) => setText(e.target.value)} />
      <button onClick={() => { markClean(); navigate("/list"); }}>Save</button>
      <Link to="/form?tab=two">Other tab</Link>
    </>
  );
}

function Layout() {
  return (
    <>
      <UnsavedGuardHost />
      <Link to="/list">Sidebar link</Link>
      <Outlet />
    </>
  );
}

function setup() {
  const router = createMemoryRouter([
    { path: "/", element: <Layout />, children: [
      { path: "form", element: <Form /> },
      { path: "list", element: <p>The list</p> },
    ] },
  ], { initialEntries: ["/list", "/form"], initialIndex: 1 });
  render(<ConfirmProvider><RouterProvider router={router} /></ConfirmProvider>);
  return router;
}

const type = (v: string) => fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: v } });

describe("useUnsavedGuard", () => {
  it("lets a clean form go without asking", async () => {
    const router = setup();
    await act(async () => { fireEvent.click(screen.getByText("Sidebar link")); });
    expect(router.state.location.pathname).toBe("/list");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("asks before leaving a dirty form: Keep editing stays, Discard leaves", async () => {
    const router = setup();
    type("Remux only");
    expect(unsavedMessage()).toBe("Discard your changes to this profile?");

    await act(async () => { fireEvent.click(screen.getByText("Sidebar link")); });
    const dialog = await screen.findByRole("dialog", { name: "Discard your changes to this profile?" });
    expect(dialog).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Keep editing" })); });
    expect(router.state.location.pathname).toBe("/form");
    expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("Remux only");

    // Back asks the same way.
    await act(async () => { router.navigate(-1); });
    await screen.findByRole("dialog");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Discard" })); });
    expect(router.state.location.pathname).toBe("/list");
    expect(unsavedMessage()).toBeNull();
  });

  it("doesn't ask for a ?tab= change on the same page", async () => {
    const router = setup();
    type("x");
    await act(async () => { fireEvent.click(screen.getByText("Other tab")); });
    expect(router.state.location.search).toBe("?tab=two");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("doesn't ask when the form's own Save navigates away", async () => {
    const router = setup();
    type("x");
    await act(async () => { fireEvent.click(screen.getByText("Save")); });
    expect(router.state.location.pathname).toBe("/list");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(unsavedMessage()).toBeNull();
  });
});
