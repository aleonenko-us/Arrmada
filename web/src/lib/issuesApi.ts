import type { Tone } from "../ui";
import { req } from "./api";

// Reported problems (Requests → Issues). Its own module, not part of lib/api.ts: only the
// lazy report sheet and the Requests page's Issues tab import it, so none of it lands in a
// requester's first load.

export type IssueCategory = "video" | "audio" | "subtitles" | "wrong_file" | "other";
export type IssueStatus = "open" | "fixing" | "resolved" | "dismissed";

// The words the report sheet and the lists use for each category.
export const ISSUE_CATEGORIES: { key: IssueCategory; label: string; hint: string }[] = [
  { key: "video", label: "Picture", hint: "Blurry, stuttering, black screen, wrong size" },
  { key: "audio", label: "Sound", hint: "Missing, out of sync, wrong language" },
  { key: "subtitles", label: "Subtitles", hint: "Missing, out of sync, wrong language" },
  { key: "wrong_file", label: "Wrong file", hint: "It’s a different movie or episode" },
  { key: "other", label: "Something else", hint: "Tell us in the note" },
];

export const issueCategoryLabel = (c: string) => ISSUE_CATEGORIES.find((k) => k.key === c)?.label ?? "Something else";

// The longest note a reporter can leave (the server refuses longer).
export const ISSUE_NOTE_MAX = 500;

export interface IssueReporter {
  user_id: number;
  name: string;
  note?: string;
  at: number;
}

export interface Issue {
  id: number;
  media_type: "movie" | "series";
  tmdb_id: number;
  title: string;
  year?: number;
  poster_url?: string;
  season?: number;
  episode?: number;
  category: IssueCategory;
  status: IssueStatus;
  /** What the reporters were told it was closed with. */
  resolution?: string;
  /** The fix staff started (replace, subtitles) and what became of it. */
  action?: string;
  action_note?: string;
  action_at?: number;
  created_at: number;
  updated_at: number;
  resolved_at?: number;
  reporter_count: number;
  /** Staff only: the first reporter, their note, everyone who reported it, the file. */
  library_id?: number;
  note?: string;
  reported_by_name?: string;
  reporters?: IssueReporter[];
  resolved_by_name?: string;
  action_file?: string;
  /** Requester view: their own note. */
  my_note?: string;
}

export interface IssueCounts {
  open: number;
  closed: number;
}

export interface IssueSeasonFiles {
  season: number;
  episodes: { episode: number; title?: string }[];
}

const post = (body: unknown): RequestInit => ({ method: "POST", body: JSON.stringify(body) });

/** The live topics an issues view refreshes on: staff's, and the requester's own. */
export const ISSUE_TOPICS = ["issue.updated", "user.*.issue.updated"] as const;

export const issuesApi = {
  list: (q: { status?: "open" | "closed"; limit?: number; id?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.status) p.set("status", q.status);
    if (q.limit) p.set("limit", String(q.limit));
    if (q.id) p.set("id", String(q.id));
    const qs = p.toString();
    return req<{ issues: Issue[]; total: number; counts: IssueCounts }>(`/api/v1/issues${qs ? `?${qs}` : ""}`);
  },
  report: (body: { media_type: "movie" | "series"; tmdb_id: number; season?: number; episode?: number; category: IssueCategory; note?: string }) =>
    req<{ issue: Issue; joined: boolean }>("/api/v1/issues", post(body)),
  episodes: (tmdbId: number) => req<{ seasons: IssueSeasonFiles[] }>(`/api/v1/issues/episodes/${tmdbId}`),
  resolve: (id: number, note: string) => req<Issue>(`/api/v1/issues/${id}/resolve`, post({ note })),
  dismiss: (id: number, note: string) => req<Issue>(`/api/v1/issues/${id}/dismiss`, post({ note })),
  /** Start a one-click fix: replace the copy, or make the subtitles again. */
  fix: (id: number, action: "replace" | "subtitles") => req<Issue>(`/api/v1/issues/${id}/fix`, post({ action })),
};

/** "S01E04" for an episode, "" for a movie. */
export function issueEpisode(i: { season?: number; episode?: number }): string {
  if (!i.episode) return "";
  return `S${String(i.season ?? 0).padStart(2, "0")}E${String(i.episode).padStart(2, "0")}`;
}

/** The chip tone for each status. */
export const ISSUE_TONE: Record<IssueStatus, Tone> = { open: "avoid", fixing: "accent", resolved: "good", dismissed: "faint" };

/** An issue's status as people read it. */
export function issueStatusLabel(s: IssueStatus): string {
  switch (s) {
    case "open": return "Reported";
    case "fixing": return "Fixing";
    case "resolved": return "Fixed";
    case "dismissed": return "Closed";
  }
}
