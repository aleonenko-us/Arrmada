import { Suspense, type ReactNode } from "react";
import { Navigate, useLocation, useParams, type RouteObject } from "react-router-dom";
import type { UserRole } from "./api";
import type { RouteHandle } from "./title";
import { UserLayout } from "../components/UserLayout";
import { RouteError } from "../components/RouteError";
import { ModuleGate } from "../components/ModuleGate";
import { SetupGate } from "../components/SetupGate";
import { lazyPage } from "./lazyPage";
import { LINKS } from "./links";
import { legacyActivityPath, type LegacyActivityPage } from "./activity";

// The staff console's frame (sidebar, Needs-you polling, restart banner) is its own
// chunk too: a requester's phone loads only the small requester shell with Discover.
const AppLayout = lazyPage(() => import("../components/AppLayout"), "AppLayout");
const staffShellLoading = <div className="grid h-full place-items-center text-md text-ink-dim">Loading…</div>;
const SetupAgain = lazyPage(() => import("../pages/SetupWizard"), "SetupAgain");

// Every page is its own chunk (FE-06): a requester downloads Discover and their few
// pages, never Quality, Convert, Settings or the rest of the console.
const MyBooks = lazyPage(() => import("../pages/MyBooks"), "MyBooks");
const Dashboard = lazyPage(() => import("../pages/Dashboard"), "Dashboard");
const Quality = lazyPage(() => import("../pages/Quality"), "Quality");
const QualityEditorRoute = lazyPage(() => import("../pages/Quality"), "QualityEditorRoute");
const Settings = lazyPage(() => import("../pages/Settings"), "Settings");
const Activity = lazyPage(() => import("../pages/Activity"), "Activity");
const Movies = lazyPage(() => import("../pages/Movies"), "Movies");
const MovieDetail = lazyPage(() => import("../pages/MovieDetail"), "MovieDetail");
const MoviesWanted = lazyPage(() => import("../pages/MoviesWanted"), "MoviesWanted");
const Series = lazyPage(() => import("../pages/Series"), "Series");
const SeriesDetail = lazyPage(() => import("../pages/SeriesDetail"), "SeriesDetail");
const Discover = lazyPage(() => import("../pages/Discover"), "Discover");
const Requests = lazyPage(() => import("../pages/Requests"), "Requests");
const Books = lazyPage(() => import("../pages/Books"), "Books");
const Music = lazyPage(() => import("../pages/Music"), "Music");
const ArtistDetail = lazyPage(() => import("../pages/ArtistDetail"), "ArtistDetail");
const AlbumDetail = lazyPage(() => import("../pages/AlbumDetail"), "AlbumDetail");
const BookDetail = lazyPage(() => import("../pages/BookDetail"), "BookDetail");
const AuthorDetail = lazyPage(() => import("../pages/AuthorDetail"), "AuthorDetail");
const AuthorByName = lazyPage(() => import("../pages/AuthorDetail"), "AuthorByName");
const Subtitles = lazyPage(() => import("../pages/Subtitles"), "Subtitles");
const Convert = lazyPage(() => import("../pages/Convert"), "Convert");
const Insights = lazyPage(() => import("../pages/Insights"), "Insights");
const Audiobooks = lazyPage(() => import("../pages/Audiobooks"), "Audiobooks");
const Calendar = lazyPage(() => import("../pages/Calendar"), "Calendar");
const Logs = lazyPage(() => import("../pages/Logs"), "Logs");
const Me = lazyPage(() => import("../pages/Me"), "Me");
const NotFound = lazyPage(() => import("../pages/NotFound"), "NotFound");

// Three shells, one table each. Staff get the whole console; requesters and outside
// visitors get the small requester shell. The server enforces the same split, so these
// tables only decide what's worth showing.
export type Shell = "external" | "requester" | "staff";

export function shellFor(role: UserRole, external: boolean): Shell {
  if (role === "admin" || role === "manager") return "staff";
  // "external" is the server's verdict that this session is from outside the LAN and
  // not staff; staff get the whole app wherever they sign in from.
  return external ? "external" : "requester";
}

// page is one route: its tab title, and the error card shown inside the layout if it throws.
function page(path: string, title: string, element: ReactNode): RouteObject {
  const handle: RouteHandle = { title };
  return { path, element, handle, errorElement: <RouteError /> };
}

function redirect(path: string, to: string): RouteObject {
  return { path, element: <Navigate to={to} replace />, errorElement: <RouteError /> };
}

// discover is Discover plus the addresses of what opens over it, in every shell: a title
// (/discover/movie/603, /discover/series/1399), a person (/discover/person/287), a movie
// collection (/discover/collection/8091), and the browse grid behind a row's "See all"
// (/discover/browse?list=popular&media=movie), which has the same overlays under it
// (/discover/browse/movie/603). They are children of /discover so the page stays mounted
// under the sheet (Discover reads the address and opens it), keeping its rows, its grid
// and the scroll position. TMDB's own "tv" spelling redirects to "series".
function discover(element: ReactNode): RouteObject {
  const title: RouteHandle = { title: "Discover" };
  const child = (path: string, children?: RouteObject[]): RouteObject => ({ path, element: null, handle: title, errorElement: <RouteError />, children });
  const overlays = () => ["movie/:tmdbId", "series/:tmdbId", "person/:personId", "collection/:collectionId"].map((p) => child(p));
  return {
    path: "/discover",
    element,
    handle: title,
    errorElement: <RouteError />,
    children: [
      ...overlays(),
      child("browse", overlays()),
      { path: "tv/:tmdbId", element: <TvRedirect />, handle: title, errorElement: <RouteError /> },
    ],
  };
}

// The pages the Activity page replaced (and the bare /activity) open the matching tab,
// keeping their query: /downloads?tab=searching is /activity/wanted.
function legacyActivity(path: string, from: LegacyActivityPage): RouteObject {
  const handle: RouteHandle = { title: "Activity" };
  return { path, element: <ActivityRedirect from={from} />, handle, errorElement: <RouteError /> };
}

function ActivityRedirect({ from }: { from: LegacyActivityPage }) {
  const { search, hash } = useLocation();
  return <Navigate to={legacyActivityPath(from, search, hash)} replace />;
}

function TvRedirect() {
  const { tmdbId } = useParams();
  const { search } = useLocation();
  return <Navigate to={`/discover/series/${tmdbId ?? ""}${search}`} replace />;
}

function requesterRoutes(shell: "external" | "requester"): RouteObject[] {
  const home = "/discover";
  return [
    discover(<Discover chrome={false} />),
    // Their own requests and the ones they follow (/api/v1/requests is allowed from outside).
    page("/requests", "Requests", <Requests chrome={false} />),
    // Outside sessions get no Calendar: the API isn't allowlisted for them.
    ...(shell === "requester" ? [page("/calendar", "Calendar", <Calendar chrome={false} />)] : []),
    // "My shelf": the books they can download. /books was its old address, kept so
    // installed shortcuts and bookmarks still land; Discover's Books tab is the catalogue.
    page("/shelf", "My shelf", <ModuleGate module="books" home={home}><MyBooks /></ModuleGate>),
    redirect("/books", "/shelf"),
    page("/audiobooks", "Audiobooks", <Audiobooks chrome={false} />),
    page("/me", "Me", <Me />),
    redirect("*", home),
  ];
}

function staffRoutes(admin: boolean): RouteObject[] {
  const home = "/";
  const books = (el: ReactNode) => <ModuleGate module="books" home={home}>{el}</ModuleGate>;
  const music = (el: ReactNode) => <ModuleGate module="music" home={home}>{el}</ModuleGate>;
  return [
    { index: true, element: <Dashboard />, handle: { title: "Dashboard" } satisfies RouteHandle, errorElement: <RouteError /> },
    page("/activity/:tab", "Activity", <Activity />),
    legacyActivity("/activity", "activity"),
    legacyActivity("/downloads", "downloads"),
    legacyActivity("/history", "history"),
    legacyActivity("/review", "review"),
    legacyActivity("/blocklist", "blocklist"),
    page("/movies", "Movies", <Movies />),
    page("/movies/wanted", "Wanted movies", <MoviesWanted />),
    page("/movies/:id", "Movie", <MovieDetail />),
    page("/series", "Series", <Series />),
    page("/series/:id", "Series", <SeriesDetail />),
    discover(<Discover />),
    page("/requests", "Requests", <Requests />),
    page("/calendar", "Calendar", <Calendar />),
    page("/music", "Music", music(<Music />)),
    page("/music/album/:id", "Album", music(<AlbumDetail />)),
    page("/music/:id", "Artist", music(<ArtistDetail />)),
    page("/books", "Books", books(<Books />)),
    page("/books/authors/:id", "Author", books(<AuthorDetail />)),
    // Links from before authors had ids: resolved to the id page, or a choice when the
    // name could be more than one author.
    page("/books/author/:name", "Author", books(<AuthorByName />)),
    page("/books/:id", "Book", books(<BookDetail />)),
    // My shelf is a requester page. A Send to Kindle result ("kindle:<send>", which names
    // neither the book nor the person) links there, so staff who send from a book page
    // land on Books instead.
    redirect("/shelf", "/books"),
    page("/subtitles", "Subtitles", <Subtitles />),
    page("/convert", "Convert", <Convert />),
    // A splat (which also matches plain /insights), so a person's page lives at
    // /insights/people/:id inside the Insights chunk.
    page("/insights/*", "Insights", <Insights />),
    page("/audiobooks", "Audiobooks", <Audiobooks />),
    page("/me", "Me", <Me chrome />),
    // The old Indexers and Download clients pages are cards on Settings → Connections now.
    redirect("/indexers", LINKS.indexers),
    redirect("/downloadclients", LINKS.downloadClients),
    // Alerts moved out of Insights into Settings; both old addresses land there.
    redirect("/notifications", LINKS.alerts),
    redirect("/alerts", LINKS.alerts),
    // A splat (which also matches plain /settings), so Settings can give its sections
    // their own addresses.
    page("/settings/*", "Settings", <Settings />),
    page("/quality", "Quality profiles", <Quality />),
    // The profile editor has its own addresses (/quality/movie/new, /quality/movie/<key>,
    // /quality/movie/<key>/copy), so a refresh, Back and the unsaved-edits guard all work on it.
    page("/quality/:media/*", "Quality profile", <QualityEditorRoute />),
    // The log is admin-only on the server; for a manager the address falls through to Not found.
    ...(admin ? [page("/logs", "Logs", <Logs />)] : []),
    redirect("/library", "/settings/library"),
    page("*", "Page not found", <NotFound />),
  ];
}

// buildRoutes returns the route table for one signed-in session. The router is rebuilt
// only when role or external changes, which happens at sign-in and sign-out (both full
// loads anyway). Module toggles don't rebuild it; ModuleGate handles those live.
export function buildRoutes({ role, external }: { role: UserRole; external: boolean }): RouteObject[] {
  const shell = shellFor(role, external);
  if (shell === "staff") {
    // On a fresh install SetupGate shows an admin the first-run wizard in place of the console.
    const frame = <SetupGate><Suspense fallback={staffShellLoading}><AppLayout /></Suspense></SetupGate>;
    const staffConsole: RouteObject = { element: frame, errorElement: <RouteError />, children: staffRoutes(role === "admin") };
    if (role !== "admin") return [staffConsole];
    // "Run setup again" (Settings → System): the wizard on its own, outside the console's
    // frame, with what's set filled in; finishing it goes back to Settings → System.
    const setupAgain: RouteObject = {
      path: "/setup",
      element: <Suspense fallback={staffShellLoading}><SetupAgain /></Suspense>,
      handle: { title: "Setup" } satisfies RouteHandle,
      errorElement: <RouteError />,
    };
    return [setupAgain, staffConsole];
  }
  return [{ element: <UserLayout />, errorElement: <RouteError />, children: requesterRoutes(shell) }];
}
