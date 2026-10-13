import { req, type SetupState } from "./api";

// First-run calls: the Getting started checklist (the Dashboard card and the wizard's last
// step). They live here rather than in api.ts so they only download with the admin pages
// that use them, not with a requester's first load.

export interface ChecklistItem {
  key: string;
  label: string;
  done: boolean;
  /** Where to do it: link_key names a LINKS entry (resolved first), link is the path. */
  link: string;
  link_key?: string;
}

export interface SetupChecklist {
  items: ChecklistItem[];
  /** Every item is ticked. */
  done: boolean;
  /** The admin dismissed the Dashboard card. */
  dismissed: boolean;
}

export const setupApi = {
  checklist: () => req<SetupChecklist>("/api/v1/setup/checklist"),
  dismissChecklist: () => req<SetupChecklist>("/api/v1/setup/checklist/dismiss", { method: "POST" }),
  /** The setup state plus whether a Prowlarr answers (a check of up to 3 s), for the Indexers step. */
  stateWithProwlarr: () => req<SetupState>("/api/v1/setup?prowlarr=1"),
};
