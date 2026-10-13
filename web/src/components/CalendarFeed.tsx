import { useEffect, useState } from "react";
import { req } from "../lib/api";
import { Button, Sheet, useConfirm, useToast } from "../ui";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";
import { Switch } from "../ui/Switch";

// The personal calendar feed: a private link Google or Apple Calendar subscribes to, so
// upcoming episodes and releases show up there. This file loads only when someone opens
// it (from the Calendar's Subscribe button or Me), never with a requester's first load.
//
// The server keeps only a fingerprint of the link, so it's shown once, when it's made:
// after that the page says a link exists and offers a new one (the old one stops working)
// or turning it off.

interface FeedState { enabled: boolean; created_at?: number; mine: boolean }
interface MadeFeed extends FeedState { url: string; webcal_url: string; outside: boolean }

const feedApi = {
  status: () => req<FeedState>("/api/v1/me/calendar-feed"),
  make: () => req<MadeFeed>("/api/v1/me/calendar-feed", { method: "POST" }),
  turnOff: () => req<FeedState>("/api/v1/me/calendar-feed", { method: "DELETE" }),
};

// withMine is a feed link showing only the person's requests, or everything.
function withMine(link: string, mine: boolean): string {
  try {
    const u = new URL(link);
    u.searchParams.set("mine", mine ? "1" : "0");
    return u.toString();
  } catch { return link; }
}

const webcal = (link: string) => link.replace(/^https?:\/\//, "webcal://");

export function CalendarFeedPanel() {
  const [state, setState] = useState<FeedState | null>(null);
  const [made, setMade] = useState<MadeFeed | null>(null);
  const [mine, setMine] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const confirm = useConfirm();

  useEffect(() => {
    let alive = true;
    feedApi.status().then((s) => { if (alive) { setState(s); setMine(s.mine); } }).catch((e: Error) => { if (alive) setError(e.message); });
    return () => { alive = false; };
  }, []);

  const make = async () => {
    if (state?.enabled) {
      const ok = await confirm({
        title: "Make a new link?",
        body: "The calendar apps using the current link stop updating. Subscribe them again with the new one.",
        confirmLabel: "Make a new link", tone: "danger",
      });
      if (!ok) return;
    }
    setBusy(true);
    try {
      const m = await feedApi.make();
      setMade(m);
      setMine(m.mine);
      setState({ enabled: true, created_at: m.created_at, mine: m.mine });
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  const turnOff = async () => {
    const ok = await confirm({ title: "Turn the calendar link off?", body: "Calendar apps using it stop updating. You can make a new link any time.", confirmLabel: "Turn off", tone: "danger" });
    if (!ok) return;
    setBusy(true);
    try {
      setState(await feedApi.turnOff());
      setMade(null);
      toast("Calendar link turned off", { tone: "good" });
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setBusy(false); }
  };
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); toast("Link copied", { tone: "good" }); }
    catch { toast("Couldn’t copy — select the link and copy it yourself", { tone: "error" }); }
  };

  if (error && !state) return <p className="m-0 text-sm" style={{ color: "var(--reject)" }}>Couldn’t load your calendar link — {error}</p>;
  if (!state) return <p className="m-0 text-sm text-ink-faint">Loading…</p>;

  const link = made ? withMine(made.url, mine) : "";
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0 text-sm text-ink-dim">
        See upcoming episodes and releases in Google Calendar, Apple Calendar or Outlook. The link is private to you — anyone who has it can see your calendar, so don’t share it.
      </p>
      {made ? (
        <>
          <Switch checked={mine} onChange={setMine} size="sm">Only my requests</Switch>
          <Field label="Apple Calendar, Outlook (subscribe)">
            <Input readOnly value={webcal(link)} onFocus={(e) => e.currentTarget.select()} className="w-full px-3 py-2 font-mono text-sm" />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="primary" onClick={() => { window.location.href = webcal(link); }}>Open in calendar app</Button>
            <Button size="sm" onClick={() => copy(webcal(link))}>Copy</Button>
          </div>
          <Field label="Google Calendar (From URL)">
            <Input readOnly value={link} onFocus={(e) => e.currentTarget.select()} className="w-full px-3 py-2 font-mono text-sm" />
          </Field>
          <div><Button size="sm" onClick={() => copy(link)}>Copy</Button></div>
          <p className="m-0 text-xs text-ink-faint">
            This is the only time the link is shown. Google Calendar fetches from the internet, so it only works with your outside (tunnel) address{made.outside ? "" : " — this link uses the address you’re on now"}.
          </p>
        </>
      ) : state.enabled ? (
        <p className="m-0 text-sm">You have a calendar link{state.created_at ? `, made ${new Date(state.created_at * 1000).toLocaleDateString()}` : ""}. For your privacy it isn’t shown again — make a new one to see it.</p>
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant={state.enabled ? "secondary" : "primary"} onClick={make} busy={busy} busyLabel="Working…" disabled={busy}>
          {state.enabled ? "Reset link" : "Make my calendar link"}
        </Button>
        {state.enabled && <Button size="sm" variant="ghost" onClick={turnOff} disabled={busy} style={{ color: "var(--reject)" }}>Turn off</Button>}
      </div>
    </div>
  );
}

// The Calendar's Subscribe sheet.
export default function CalendarFeedSheet({ onClose }: { onClose: () => void }) {
  return (
    <Sheet onClose={onClose} title="Subscribe to your calendar" size="md">
      <div className="px-4 pb-4 sm:px-0 sm:pb-0">
        <CalendarFeedPanel />
      </div>
    </Sheet>
  );
}
