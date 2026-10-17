import { req } from "./api";

// Display titles (staff): a movie's or show's "Display title…" override and the names it
// may pick from. Its own module, not part of lib/api.ts: only the picker on the movie and
// series pages loads it, so none of it lands in a requester's first load.

export type TitleMedia = "movies" | "series";

/** One name a title may be shown by. */
export interface TitleChoice {
  title: string;
  /** english (TMDB's title), original (in its own language and script) or translation. */
  kind: "english" | "original" | "translation";
  /** ISO 639-1 language of an original title or a translation, and a translation's region. */
  lang?: string;
  country?: string;
}

export interface DisplayTitleState {
  id: number;
  title: string;
  display_title: string;
  title_override?: string;
}

export const displayTitleApi = {
  /** The names TMDB has for the title (through the server's titles cache), or the stored ones. */
  choices: (media: TitleMedia, id: number) =>
    req<{ choices: TitleChoice[] }>(`/api/v1/${media}/${id}/title-choices`).then((r) => r.choices),
  /** Show the title as `title`; "" follows Settings → Library → "Show titles as" again. */
  set: (media: TitleMedia, id: number, title: string) =>
    req<DisplayTitleState>(`/api/v1/${media}/${id}/display-title`, { method: "PUT", body: JSON.stringify({ title }) }),
};
