import { useEffect, useState } from "react";
import type { MyBook } from "../lib/api";
import { AMAZON_DEVICES_URL, kindleApi, type KindleView } from "../lib/kindleApi";
import { Button, Sheet, useToast } from "../ui";

// Send to Kindle, as a requester meets it: a button on a book, a first-time sheet that
// explains Amazon's approved-senders list and asks for the Kindle's address, and a card on
// the Me page to change that address. Lazy-loaded with the calls behind it (lib/kindleApi),
// so none of this is in anyone's first load. An email only ever goes out from a tap on
// Send; the result arrives in the bell.

// Amazon's own limit for a document sent by email (books.KindleMaxBytes on the server).
const KINDLE_MAX_BYTES = 50 * 1024 * 1024;

// One read of the person's Kindle setup per page visit, shared by every button.
let viewPromise: Promise<KindleView> | null = null;
function loadView(): Promise<KindleView> {
  if (!viewPromise) viewPromise = kindleApi.get().catch((e) => { viewPromise = null; throw e; });
  return viewPromise;
}
function rememberView(v: KindleView) {
  viewPromise = Promise.resolve(v);
}

// kindleBlocked says why a book can't go to a Kindle by email ("" = it can): Amazon takes
// EPUB and PDF up to 50 MB. The server decides the format (ebook.kindle) and checks again.
export function kindleBlocked(book: MyBook): string {
  const e = book.ebook;
  if (!e) return "No ebook yet";
  if (!e.kindle) return `This ebook is ${(e.format || "in another format").toUpperCase()} — Kindle email takes EPUB or PDF`;
  if (e.size_bytes > KINDLE_MAX_BYTES) return "This ebook is over 50 MB, more than Kindle email takes";
  return "";
}

export function KindleButton({ book }: { book: MyBook }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [setup, setSetup] = useState<KindleView | null>(null);
  const blocked = kindleBlocked(book);

  const send = async () => {
    try {
      await kindleApi.send(book.book_id);
      toast(`Sending “${book.title}”… you’ll get a message when it’s done`, { tone: "good" });
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    }
  };

  const click = async () => {
    setBusy(true);
    try {
      const v = await loadView();
      if (!v.smtp_configured) {
        toast("Sending to Kindle isn’t set up on this server yet — ask whoever runs it.", { tone: "error" });
      } else if (!v.kindle_email) {
        setSetup(v); // first time: explain, then ask for the address
      } else {
        await send();
      }
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button onClick={click} busy={busy} busyLabel="Sending…" disabled={!!blocked} title={blocked || "Email this ebook to your Kindle"}>
        Send to Kindle
      </Button>
      {blocked && book.ebook && <span className="basis-full text-xs text-ink-faint">{blocked}</span>}
      {setup && (
        <Sheet onClose={() => setSetup(null)} title="Send to Kindle" size="sm">
          <div className="flex flex-col gap-4 px-5 pb-5">
            <KindleSteps from={setup.from_address} />
            <KindleAddressForm
              view={setup}
              submitLabel={`Save and send “${book.title}”`}
              onSaved={(v) => { rememberView(v); setSetup(null); void send(); }}
            />
          </div>
        </Sheet>
      )}
    </>
  );
}

// KindleSteps is the one thing Amazon needs before the first send, said plainly.
function KindleSteps({ from }: { from?: string }) {
  return (
    <ol className="m-0 flex flex-col gap-2 pl-5 text-base leading-relaxed text-ink-dim">
      <li>
        On Amazon’s{" "}
        <a href={AMAZON_DEVICES_URL} target="_blank" rel="noreferrer" className="font-semibold" style={{ color: "var(--accent-text)" }}>Manage Your Content and Devices</a>
        {" "}page, open <b>Preferences → Personal Document Settings</b>.
      </li>
      <li>
        Under <b>Approved Personal Document E-mail List</b>, add{" "}
        {from ? <b className="break-all" style={{ color: "var(--ink)" }}>{from}</b> : "the address Arrmada sends from"}.
        Amazon throws away books from any address that isn’t on it.
      </li>
      <li>Your Kindle’s own address (it ends in @kindle.com) is on the same page. Enter it below.</li>
    </ol>
  );
}

// KindleAddressForm saves the person's Kindle address. An address outside Amazon's Kindle
// domains is saved but questioned once before it's used.
function KindleAddressForm({ view, submitLabel, onSaved }: { view: KindleView; submitLabel: string; onSaved: (v: KindleView) => void }) {
  const [addr, setAddr] = useState(view.kindle_email);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [warned, setWarned] = useState<KindleView | null>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const v = await kindleApi.set(addr.trim());
      if (v.warning && v.kindle_email) setWarned(v);
      else onSaved(v);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); void save(); }}>
      <label className="flex flex-col gap-1.5">
        <span className="text-sm font-semibold text-ink-dim">Your Kindle’s email address</span>
        <input
          type="email" inputMode="email" autoComplete="off" spellCheck={false} required
          value={addr} onChange={(e) => { setAddr(e.target.value); setWarned(null); }}
          placeholder="name_42@kindle.com"
          className="w-full rounded-lg px-3 py-2 text-md"
          style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}
        />
      </label>
      {error && <p className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>}
      {warned ? (
        <>
          <p className="m-0 text-sm" style={{ color: "var(--avoid-text)" }}>{warned.warning}</p>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" onClick={() => onSaved(warned)}>Use it anyway</Button>
          </div>
        </>
      ) : (
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" type="submit" busy={busy} busyLabel="Saving…" disabled={!addr.trim()}>{submitLabel}</Button>
        </div>
      )}
    </form>
  );
}

// KindleCard is the Me page's "Send to Kindle" section: the address, the steps, and a way
// to change or remove it.
export function KindleCard() {
  const toast = useToast();
  const [view, setView] = useState<KindleView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  useEffect(() => {
    loadView().then(setView).catch((e) => setError((e as Error).message));
  }, []);

  if (error) return <p className="m-0 px-3.5 py-3 text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>;
  if (!view) return <p className="m-0 px-3.5 py-3 text-sm text-ink-faint">Loading…</p>;

  const saved = (v: KindleView) => { rememberView(v); setView(v); setEditing(false); toast("Kindle address saved", { tone: "good" }); };
  const remove = async () => {
    try { const v = await kindleApi.set(""); rememberView(v); setView(v); } catch (e) { toast((e as Error).message, { tone: "error" }); }
  };

  return (
    <div className="flex flex-col gap-3 px-3.5 py-3">
      {!view.smtp_configured && (
        <p className="m-0 text-sm text-ink-dim">Sending to Kindle isn’t set up on this server yet. You can still save your address for when it is.</p>
      )}
      {view.kindle_email && !editing ? (
        <>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="min-w-0 break-all text-md font-semibold">{view.kindle_email}</span>
            <span className="flex gap-2">
              <Button size="sm" onClick={() => setEditing(true)}>Change</Button>
              <Button size="sm" variant="ghost" onClick={remove}>Remove</Button>
            </span>
          </div>
          {view.warning && <p className="m-0 text-sm" style={{ color: "var(--avoid-text)" }}>{view.warning}</p>}
          {view.from_address && <p className="m-0 text-sm text-ink-faint">Books come from {view.from_address} — keep it on Amazon’s approved list.</p>}
        </>
      ) : (
        <>
          <KindleSteps from={view.from_address} />
          <KindleAddressForm view={view} submitLabel="Save" onSaved={saved} />
        </>
      )}
    </div>
  );
}
