import { BrowserRouter } from "react-router-dom";
import type { LoginAnswer } from "../lib/api";
import { SetupWizard } from "./SetupWizard";

// The first-run screen of a fresh install: the setup wizard with no account behind it yet.
// Nobody is signed in, so there is no router above it (the console's router only exists for
// a signed-in role); the wizard's Plex, Jellyfin and Emby panels link and read the address
// like they do in Settings, so it gets a small one of its own. Its last step makes the
// first admin and calls onSignedIn.
export default function FirstRunSetup({ onSignedIn }: { onSignedIn: (answer: LoginAnswer) => void }) {
  return (
    <BrowserRouter>
      <SetupWizard firstRun onDone={() => { /* the account step is the only way out */ }} onSignedIn={onSignedIn} />
    </BrowserRouter>
  );
}
