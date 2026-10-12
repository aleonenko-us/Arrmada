import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { useMe } from "../lib/me";
import { Card, Field } from "../pages/audiobooks/shared";

// EReaderPanel is "Read on your e-reader": the OPDS catalogue's address, the sign-in, and
// a line of setup for each reader app that speaks OPDS. Readers sign in with the person's
// username and their audiobook & e-reader password (the same one listening apps use), so
// the panel says whether that's set and where to set it.
//
// It's a lazy chunk on My shelf and Audiobooks → Apps & devices; nothing here is on a
// requester's first load.

/** What the panel knows about the person's audiobook & e-reader password; undefined = look it up. */
export type ReaderPassword = { has: boolean; allowed: boolean } | null;

/** The catalogue's address on the origin this page was opened from. */
export function opdsAddress(origin: string): string {
  return `${origin.replace(/\/+$/, "")}/opds`;
}

export const READER_APPS: { name: string; where: string; how: string }[] = [
  { name: "KOReader", where: "Kobo (with KOReader), Kindle, PocketBook, Android", how: "Search → OPDS catalog → +, then the address, username and password." },
  { name: "Moon+ Reader", where: "Android", how: "Net Library → add a catalog, then the address; sign in when it asks." },
  { name: "Librera", where: "Android", how: "Library → Network (OPDS) → +, then the address; sign in when it asks." },
  { name: "Thorium Reader", where: "Windows, Mac, Linux", how: "Catalogs → Add OPDS feed, then the address; sign in when it asks." },
];

/** password: the person's password state (undefined: the panel looks it up). passwordCard: the
 *  form to set it, where the page has one (Audiobooks); elsewhere the panel links there. */
export function EReaderPanel({ password, passwordCard }: { password?: ReaderPassword; passwordCard?: ReactNode }) {
  const { user, external } = useMe();
  // The Audiobooks page already has the answer; My shelf asks for it.
  const lookup = password === undefined;
  const [fetched, setFetched] = useState<ReaderPassword | undefined>(undefined);
  useEffect(() => {
    if (!lookup) return;
    let live = true;
    api.myAudio()
      .then((d) => { if (live) setFetched({ has: d.has_password, allowed: d.allowed }); })
      .catch(() => { if (live) setFetched(null); });
    return () => { live = false; };
  }, [lookup]);
  const known = lookup ? fetched : password;

  const address = opdsAddress(window.location.origin);
  const https = window.location.protocol === "https:";
  return (
    <Card title="Read on your e-reader" note="Browse the library in an e-reader app and download books straight to it.">
      {known && !known.allowed ? (
        <p className="m-0 text-[12.5px] text-ink-dim">Your account isn't set up for reading apps. Ask an admin.</p>
      ) : (
        <>
          <Field label="Catalogue address (OPDS)" value={address} note={!https && !external ? "At home" : undefined} />
          {user?.username && <Field label="Username" value={user.username} />}
          <p className="m-0 mb-3 text-[12px] text-ink-dim">
            Password: your <b>audiobook &amp; e-reader password</b> — not your Arrmada password.{" "}
            {passwordCard ? null : known?.has === false
              ? <>You haven't set one yet: <Link to="/audiobooks?tab=apps" className="font-semibold" style={{ color: "var(--accent)" }}>set it in Audiobooks → Apps &amp; devices</Link>.</>
              : <>Change it in <Link to="/audiobooks?tab=apps" className="font-semibold" style={{ color: "var(--accent)" }}>Audiobooks → Apps &amp; devices</Link>; it's the one listening apps use too.</>}
          </p>
          {passwordCard && <div className="mb-3">{passwordCard}</div>}
          <ul className="m-0 mb-3 flex list-none flex-col gap-1.5 p-0">
            {READER_APPS.map((a) => (
              <li key={a.name} className="rounded-lg px-3 py-2 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line-soft)" }}>
                <span className="font-semibold">{a.name}</span> <span className="text-ink-faint">· {a.where}</span>
                <span className="block text-ink-dim">{a.how}</span>
              </li>
            ))}
          </ul>
          <p className="m-0 text-[11.5px] text-ink-faint">
            {https
              ? "This is a secure (https) address, so it works away from home too."
              : "This address works on the home network. Away from home, use your https address instead — the password travels with every request, so never use plain http over the internet."}
            {" "}Stock Kobo software and Apple Books can't open OPDS catalogues; menu names vary between app versions.
          </p>
        </>
      )}
    </Card>
  );
}
