// A film's or show's shown title: the library's display title (Settings → Library → "Show
// titles as", or the title's own override) when the API sends one, else its title. The
// API keeps `title` canonical (TMDB's English title); only what people read changes.
export const shownTitle = (x: { title: string; display_title?: string }): string => x.display_title || x.title;

// A title's shown overview: in the language titles are shown in (the language mode of
// "Show titles as") when the API sends one, else TMDB's English overview.
export const shownOverview = (x?: { overview?: string; display_overview?: string } | null): string | undefined =>
  x?.display_overview || x?.overview;
