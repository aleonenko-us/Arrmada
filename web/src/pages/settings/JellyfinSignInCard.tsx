import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { AutoToggle, SavedTick } from "../../components/settings/auto";
import { Note, Section } from "../../components/settings/ui";
import { jellyfinApi } from "../../lib/jellyfinApi";
import { LINKS } from "../../lib/links";
import { useMe } from "../../lib/me";
import { useSetting } from "../../lib/useSettings";
import { AutoApproveChecks, typesFromCSV, typesToCSV } from "./People";
import { requesterPages } from "./roles";

// Jellyfin sign-in (admin): whether people may sign in with their Jellyfin username and
// password, whether Quick Connect is offered too, whether it may open a staff account, and
// what a new Jellyfin sign-in may request without asking. Each control saves itself.
export function JellyfinSignIn() {
  const { booksEnabled } = useMe();
  const allow = useSetting("jellyfin_login_enabled");
  const quick = useSetting("jellyfin_quickconnect_enabled");
  const staff = useSetting("jellyfin_signin_staff");
  const autoTypes = useSetting("jellyfin_login_auto_approve_types", { keepOnError: false });
  // Sign-in needs a Jellyfin server to send people's details to.
  const [connected, setConnected] = useState<boolean | null>(null);
  useEffect(() => { jellyfinApi.get().then((c) => setConnected(!!c.url)).catch(() => setConnected(null)); }, []);

  return (
    <Section id="jellyfin-sign-in" title="Jellyfin sign-in" subtitle={<>Let people sign in with their Jellyfin username and password. A new one gets a Requester account ({requesterPages(booksEnabled)}). Jellyfin administrators link Jellyfin from their own account instead. Passwords go to Jellyfin and are never kept.</>}>
      {connected === false && (
        <Note tone="warn">Connect your Jellyfin server first: <Link to={LINKS.jellyfinConnection} style={{ color: "var(--accent-text)" }}>Jellyfin connection →</Link></Note>
      )}
      <AutoToggle label="Allow Sign in with Jellyfin" hint="Adds a 'Sign in with Jellyfin' button to the login page." save={allow} />
      <AutoToggle
        label="Offer Quick Connect"
        hint="People can sign in by typing a code into a Jellyfin app they're already signed in to — handy on a TV, or with no password. Quick Connect must be on in Jellyfin too (Dashboard → General)."
        save={quick}
      />
      <div className="flex flex-col gap-1.5">
        <div className="flex flex-wrap items-center gap-2 text-base font-semibold">Auto-approve new Jellyfin sign-ins' requests <SavedTick save={autoTypes} /></div>
        <AutoApproveChecks
          value={typesFromCSV(autoTypes.value ?? "movie")}
          onChange={(v) => void autoTypes.commit(typesToCSV(v))}
          books={booksEnabled}
          label="New Jellyfin sign-ins auto-approve"
        />
        <p className="m-0 text-xs text-ink-faint">Ticked types download straight away for someone who signs in with Jellyfin for the first time; the rest wait for you. Existing accounts keep their own settings.</p>
      </div>
      <AutoToggle
        label="Let staff sign in with Jellyfin"
        hint="Sign in with Jellyfin may open an admin or manager account linked to that Jellyfin user. Off: staff sign in with their password."
        save={staff}
      />
      {staff.value && (
        <Note tone="warn">Anyone who knows a linked Jellyfin user's password gets that account — for an admin, everything in Arrmada.</Note>
      )}
    </Section>
  );
}
