import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { PageHeader } from "../components/PageHeader";
import { api, type Book, type BookAuthor, type BookDiscoverCard } from "../lib/api";
import { bookAuthorApi, type LibraryAuthor, type LibraryAuthorPatch } from "../lib/bookAuthorApi";
import { followLine } from "../lib/authorFollow";
import { qualityApi } from "../lib/qualityApi";
import { useCanHover } from "../lib/useCanHover";
import { libraryStatus } from "../lib/status";
import { editionsHeld } from "../lib/bookSearch";
import { Button, StatusChip, useToast, type Tone } from "../ui";

// AuthorDetail unifies an author's shelf: the books you already own (link through to the
// full book detail page for grab / auto-grab / etc.) plus the rest of their official
// catalogue (from Hardcover when a key is set, else Open Library; bundle-filtered) that you
// can add in a click.
//
// The page is the author row (/books/authors/:id). Its catalogue is the catalogue author
// stored on that row, never a guess from the name: an author with none stored asks
// "Which author is this?" and the pick is saved.
export function AuthorDetail() {
  const { id: raw } = useParams();
  const id = Number(raw);
  const navigate = useNavigate();

  const [author, setAuthor] = useState<LibraryAuthor | null>(null);
  const [missingAuthor, setMissingAuthor] = useState(false);
  const [library, setLibrary] = useState<Book[] | null>(null);
  const [detail, setDetail] = useState<BookAuthor | null>(null); // photo + bio, when the catalogue has them
  const [works, setWorks] = useState<BookDiscoverCard[] | null>(null);
  const [repick, setRepick] = useState(false);
  const [profiles, setProfiles] = useState<{ key: string; name: string; isDefault: boolean }[]>([]);
  const [saving, setSaving] = useState(false);
  const [addingKey, setAddingKey] = useState<string | null>(null);
  const [addingAll, setAddingAll] = useState(false);
  const toast = useToast();

  const loadLibrary = useCallback(() => api.books().then((r) => setLibrary(r.books)).catch(() => setLibrary([])), []);
  const loadAuthor = useCallback(() => bookAuthorApi.get(id).then((a) => { setAuthor(a); setMissingAuthor(false); }).catch(() => setMissingAuthor(true)), [id]);

  useEffect(() => { setAuthor(null); setRepick(false); void loadAuthor(); void loadLibrary(); }, [loadAuthor, loadLibrary]);
  useEffect(() => {
    qualityApi.qualityProfiles("book").then((r) => {
      setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name, isDefault: !!p.is_default })));
    }).catch(() => {});
  }, []);

  // The catalogue answers for the stored author only.
  const key = author?.catalogue_key ?? "";
  useEffect(() => {
    setWorks(null);
    setDetail(null);
    if (!key) return;
    api.bookAuthorWorks(key).then(setWorks).catch(() => setWorks([]));
    if (key.startsWith("hc:a:")) api.bookAuthorDetail(key).then(setDetail).catch(() => setDetail(null));
  }, [key]);

  const owned = useMemo(() => (library ?? []).filter((b) => b.author_id === id), [library, id]);
  const ownedKeys = useMemo(() => new Set(owned.map((b) => b.ol_key)), [owned]);
  const missing = useMemo(() => (works ?? []).filter((w) => !w.in_library && !ownedKeys.has(w.key)), [works, ownedKeys]);
  const catalogueName = key ? (key.startsWith("hc:a:") ? "Hardcover" : "Open Library") : null;
  const defaultProfile = profiles.find((p) => p.isDefault)?.key ?? profiles[0]?.key ?? "";
  const profile = author?.quality_profile || defaultProfile;

  const save = async (patch: LibraryAuthorPatch, done?: string) => {
    if (!author) return;
    setSaving(true);
    try {
      const next = await bookAuthorApi.update(author.id, patch);
      if (next.id !== author.id) {
        // The pick was an author already in the library under another spelling: the two
        // are one now, and this page moves to the one that stayed.
        toast(`Joined with ${next.name}`);
        navigate(`/books/authors/${next.id}`, { replace: true });
        return;
      }
      setAuthor(next);
      setRepick(false);
      if (done) toast(done);
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setSaving(false); }
  };

  const add = async (w: BookDiscoverCard) => {
    if (!author) return;
    setAddingKey(w.key);
    try {
      await api.addBook({ ol_key: w.key, quality_profile: profile, monitored: true, title: w.title, author: author.name, year: w.year, cover_url: w.cover_url });
      await Promise.all([loadLibrary(), loadAuthor()]);
      toast(`Added “${w.title}”`);
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setAddingKey(null); }
  };

  const addAll = async () => {
    if (!author || !key) return;
    setAddingAll(true);
    try {
      const r = await api.addAuthor({ author_key: key, name: author.name, quality_profile: profile });
      await Promise.all([loadLibrary(), loadAuthor()]);
      toast(`Added ${r.added} book${r.added === 1 ? "" : "s"}${r.skipped ? ` · ${r.skipped} already in library` : ""}`);
    } catch (e) { toast((e as Error).message, { tone: "error" }); } finally { setAddingAll(false); }
  };

  if (missingAuthor) {
    return (
      <>
        <PageHeader title="Author" tail="Books" />
        <div className="mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6">
          <BackLink />
          <Empty>This author isn’t in your library any more.</Empty>
        </div>
      </>
    );
  }

  const name = author?.name ?? "";
  const image = detail?.image_url || author?.image_url;
  return (
    <>
      <PageHeader title={name || "Author"} tail="Author" />
      <div className="mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6">
        <BackLink />

        <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
          <div className="flex min-w-0 items-center gap-4">
            {image && (
              <img src={image} alt={name} className="h-[72px] w-[72px] flex-none rounded-full object-cover sm:h-[88px] sm:w-[88px]" style={{ border: "1px solid var(--line)" }} />
            )}
            <div className="min-w-0">
              <h1 className="m-0 break-words text-[22px] font-bold">{name || "…"}</h1>
              <div className="mt-1 text-[12px] text-ink-faint">
                {owned.length} in library{detail?.work_count ? ` · ${detail.work_count.toLocaleString()} works in the catalogue` : ""}{detail?.birth_date ? ` · ${detail.birth_date}` : ""}
                {author && author.names && author.names.length > 1 && <> · also filed as {author.names.filter((n) => n !== name).join(", ")}</>}
              </div>
              {(detail?.bio || author?.bio) && <p className="m-0 mt-2 max-w-[70ch] text-[12.5px] leading-relaxed text-ink-dim" style={{ display: "-webkit-box", WebkitLineClamp: 4, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{detail?.bio || author?.bio}</p>}
              {key && !repick && (
                <div className="mt-1.5 text-[11.5px] text-ink-faint">
                  {catalogueName} author{" "}
                  <button type="button" onClick={() => setRepick(true)} className="underline hover:text-[var(--ink)]">Not this person?</button>
                </div>
              )}
              {key && author?.monitored && author.monitor_new && (
                <div className="mt-1 text-[11.5px] text-ink-faint">{followLine(author)}</div>
              )}
            </div>
          </div>
          {author && (
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
              <Switch label="Monitor" on={author.monitored} disabled={saving}
                onToggle={() => save({ monitored: !author.monitored }, author.monitored ? "No longer following this author" : "Following this author")} />
              <Switch label="Add new books automatically" on={author.monitor_new} disabled={saving}
                onToggle={() => save({ monitor_new: !author.monitor_new })} />
              <label className="flex items-center gap-2">
                <span className="font-mono text-[10px] uppercase text-ink-faint">Format</span>
                <select aria-label="Format" value={profile} disabled={saving} onChange={(e) => save({ quality_profile: e.target.value })} className="rounded-lg px-2.5 py-1.5 text-[12px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>
                  {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
                </select>
              </label>
              {missing.length > 0 && (
                <Button variant="primary" onClick={addAll} busy={addingAll} busyLabel="Adding…" className="disabled:opacity-50">
                  + Add all {missing.length} missing
                </Button>
              )}
            </div>
          )}
        </div>

        {author && (!key || repick) && (
          <WhichAuthor name={name} current={key} busy={saving}
            onPick={(a) => save({ catalogue_key: a.key }, `Saved: ${a.name}`)}
            onCancel={key ? () => setRepick(false) : undefined} />
        )}

        {/* In your library */}
        <SectionTitle count={library === null ? undefined : owned.length}>In your library</SectionTitle>
        {library === null ? (
          <Grid>{Array.from({ length: 4 }).map((_, i) => <div key={i} className="rounded-xl" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }} />)}</Grid>
        ) : owned.length === 0 ? (
          <Empty>No books by {name || "this author"} in your library yet{key ? " — add some from their catalogue below" : ""}.</Empty>
        ) : (
          <div className="mb-8"><Grid>{owned.map((b) => <OwnedCard key={b.id} b={b} />)}</Grid></div>
        )}

        {/* Rest of the catalogue: only once we know who the author is. */}
        {key && (
          <>
            <SectionTitle count={works === null ? undefined : missing.length}>More by {name}</SectionTitle>
            {works === null ? (
              <Grid>{Array.from({ length: 8 }).map((_, i) => <div key={i} className="rounded-xl" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }} />)}</Grid>
            ) : missing.length === 0 ? (
              <Empty>{owned.length > 0 ? "You have this author's whole catalogue. 🎉" : `Couldn't find more books for this author on ${catalogueName}.`}</Empty>
            ) : (
              <Grid>{missing.map((w) => <CatalogueCard key={w.key} w={w} onAdd={() => add(w)} busy={addingKey === w.key} disabled={addingKey !== null} />)}</Grid>
            )}
          </>
        )}
      </div>

    </>
  );
}

// WhichAuthor is the "Which author is this?" picker: the catalogue's authors by this
// name, each with a photo, how many works they have and one of them, so a person can
// tell namesakes apart. There is no silent fallback — nothing is shown as this author's
// catalogue until someone picks.
function WhichAuthor({ name, current, busy, onPick, onCancel }: { name: string; current: string; busy: boolean; onPick: (a: BookAuthor) => void; onCancel?: () => void }) {
  const [q, setQ] = useState(name);
  const [results, setResults] = useState<BookAuthor[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const search = useCallback((text: string) => {
    setResults(null);
    setError(null);
    api.searchBookAuthors(text).then(setResults).catch((e) => { setResults([]); setError((e as Error).message); });
  }, []);
  useEffect(() => { if (name) search(name); }, [name, search]);

  return (
    <section aria-labelledby="which-author" className="mb-8 rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="mb-1 flex items-start justify-between gap-3">
        <h2 id="which-author" className="m-0 text-[15px] font-bold">Which author is this?</h2>
        {onCancel && <button type="button" onClick={onCancel} className="text-[12px] text-ink-dim hover:text-[var(--ink)]">Cancel</button>}
      </div>
      <p className="m-0 mb-3 text-[12px] text-ink-dim">Pick the person in the catalogue, so this page shows their books and nobody else’s. Arrmada remembers the choice.</p>
      <form className="mb-3 flex gap-2" onSubmit={(e) => { e.preventDefault(); if (q.trim()) search(q.trim()); }}>
        <input value={q} onChange={(e) => setQ(e.target.value)} aria-label="Author name" className="min-w-0 flex-1 rounded-lg px-3 py-2 text-[13px]" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }} />
        <button type="submit" className="rounded-lg px-3 py-2 text-[12.5px] font-semibold" style={{ background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" }}>Search</button>
      </form>
      {results === null ? (
        <div className="p-4 text-center text-[12.5px] text-ink-dim">Searching the catalogue…</div>
      ) : results.length === 0 ? (
        <div className="p-4 text-center text-[12.5px] text-ink-dim">{error ? `The catalogue didn’t answer: ${error}` : "No authors by that name in the catalogue. Try another spelling."}</div>
      ) : (
        <ul className="m-0 grid list-none gap-2 p-0" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 260px), 1fr))" }}>
          {results.map((a) => (
            <li key={a.key}>
              <button type="button" disabled={busy || a.key === current} onClick={() => onPick(a)}
                className="flex w-full items-center gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-[var(--panel-2)] disabled:opacity-60"
                style={{ border: "1px solid var(--line)", minHeight: 44 }}>
                {a.image_url ? (
                  <img src={a.image_url} alt="" className="h-11 w-11 flex-none rounded-full object-cover" loading="lazy" />
                ) : (
                  <span aria-hidden className="flex h-11 w-11 flex-none items-center justify-center rounded-full text-[16px] font-bold text-white" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>{a.name.charAt(0).toUpperCase()}</span>
                )}
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-semibold">{a.name}{a.key === current ? " · current" : ""}</span>
                  <span className="block truncate text-[11.5px] text-ink-faint">
                    {a.work_count > 0 ? `${a.work_count.toLocaleString()} works` : "Author"}{a.top_work ? ` · ${a.top_work}` : ""}{a.birth_date ? ` · b. ${a.birth_date}` : ""}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// AuthorByName serves links from before authors had ids (/books/author/<name>): the name
// resolves to its author's page, or — when it could be more than one author in the
// library — a person picks which.
export function AuthorByName() {
  const { name: raw } = useParams();
  const name = decodeURIComponent(raw ?? "");
  const navigate = useNavigate();
  const [candidates, setCandidates] = useState<LibraryAuthor[] | null>(null);

  useEffect(() => {
    setCandidates(null);
    bookAuthorApi.named(name).then((list) => {
      if (list.length === 1) navigate(`/books/authors/${list[0].id}`, { replace: true });
      else setCandidates(list);
    }).catch(() => setCandidates([]));
  }, [name, navigate]);

  return (
    <>
      <PageHeader title={name} tail="Author" />
      <div className="mx-auto w-full max-w-[1440px] px-4 py-6 sm:px-6">
        <BackLink />
        {candidates === null ? (
          <div className="p-8 text-center text-[12.5px] text-ink-dim">Finding {name}…</div>
        ) : candidates.length === 0 ? (
          <Empty>No author in your library goes by “{name}”. Their books may be filed under another spelling — try <Link to="/books" className="underline">All books</Link>.</Empty>
        ) : (
          <section aria-labelledby="which-library-author">
            <h2 id="which-library-author" className="m-0 mb-1 text-[15px] font-bold">Which author is this?</h2>
            <p className="m-0 mb-3 text-[12px] text-ink-dim">“{name}” could be any of these authors in your library.</p>
            <ul className="m-0 grid list-none gap-2 p-0" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, 260px), 1fr))" }}>
              {candidates.map((a) => (
                <li key={a.id}>
                  <Link to={`/books/authors/${a.id}`} className="flex items-center gap-3 rounded-lg p-2.5 hover:bg-[var(--panel-2)]" style={{ border: "1px solid var(--line)", minHeight: 44 }}>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-[13px] font-semibold">{a.name}</span>
                      <span className="block truncate text-[11.5px] text-ink-faint">{a.books} book{a.books === 1 ? "" : "s"}{a.names && a.names.length > 1 ? ` · also ${a.names.filter((n) => n !== a.name).join(", ")}` : ""}</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </>
  );
}

function BackLink() {
  return (
    <Link to="/books" className="mb-4 inline-flex items-center gap-1 text-[12px] text-ink-dim hover:text-[var(--ink)]">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M15 19l-7-7 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
      All books
    </Link>
  );
}

// Switch is the Books pages' on/off control (the book page's Monitor switch).
function Switch({ label, on, onToggle, disabled }: { label: string; on: boolean; onToggle: () => void; disabled?: boolean }) {
  return (
    <button type="button" role="switch" aria-checked={on} disabled={disabled} onClick={onToggle} className="inline-flex min-h-[32px] items-center gap-2 text-[12.5px] font-semibold disabled:opacity-50">
      <span className="relative inline-block h-[22px] w-[38px] flex-none rounded-full transition-colors" style={{ background: on ? "var(--accent)" : "var(--line)" }}>
        <span className="absolute top-[3px] h-[16px] w-[16px] rounded-full bg-white transition-all" style={{ left: on ? "19px" : "3px" }} />
      </span>
      <span style={{ color: on ? "var(--ink)" : "var(--ink-dim)" }}>{label}</span>
    </button>
  );
}

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(140px, 1fr))" }}>{children}</div>;
}

// The chip names the editions on disk ("Ebook · Audiobook ✓") rather than "E+A".
function statusOf(b: Book): { label: string; tone: Tone } {
  const st = libraryStatus({ hasFile: b.has_file, monitored: b.monitored });
  const held = st.label === "Downloaded" ? editionsHeld(b) : "";
  return { label: held ? `${held} ✓` : st.label, tone: st.tone };
}

// BookCover renders a cover, falling back to a clean title placeholder when the
// image is missing or fails to load (a broken URL otherwise leaves a dim, blank
// tile that reads as "faded").
function BookCover({ url, title }: { url?: string; title: string }) {
  const [failed, setFailed] = useState(false);
  if (url && !failed) {
    return <img src={url} alt={title} className="h-full w-full object-cover" loading="lazy" onError={() => setFailed(true)} />;
  }
  return <div className="flex h-full w-full items-center justify-center p-2 text-center text-[12px] font-bold text-white" style={{ background: "linear-gradient(150deg, hsl(28 30% 26%), hsl(24 28% 16%))" }}>{title}</div>;
}

function OwnedCard({ b }: { b: Book }) {
  const st = statusOf(b);
  const canHover = useCanHover();
  return (
    <Link to={`/books/${b.id}`} className="group relative block overflow-hidden rounded-xl" style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <BookCover url={b.cover_url} title={b.title} />
      <StatusChip tone={st.tone} surface="poster" size="xs" className="absolute left-1.5 top-1.5 max-w-[calc(100%-0.75rem)] truncate">{st.label}</StatusChip>
      {/* Decorative caption only, so it never eats the tap meant for the card link. Touch
          can't hover to reveal it, and many Open Library covers carry no printed title, so
          there it always shows. */}
      <div className={`pointer-events-none absolute inset-x-0 bottom-0 p-2 ${canHover ? "opacity-0 transition-opacity group-hover:opacity-100" : ""}`} style={{ background: "linear-gradient(to top, rgba(0,0,0,.9), transparent)" }}>
        <div className="truncate text-[11.5px] font-semibold text-white">{b.title}</div>
        {b.year > 0 && <div className="text-[10px]" style={{ color: "rgba(255,255,255,.7)" }}>{b.year}</div>}
      </div>
    </Link>
  );
}

function CatalogueCard({ w, onAdd, busy, disabled }: { w: BookDiscoverCard; onAdd: () => void; busy: boolean; disabled: boolean }) {
  return (
    <div className="group relative overflow-hidden rounded-xl" style={{ aspectRatio: "2/3", border: "1px solid var(--line)", background: "var(--panel-2)" }}>
      <BookCover url={w.cover_url} title={w.title} />
      <div className="absolute inset-0 flex flex-col justify-end p-2" style={{ background: "linear-gradient(to top, rgba(0,0,0,.92), transparent 55%)" }}>
        <div className="truncate text-[11.5px] font-semibold text-white" title={w.title}>{w.title}</div>
        {w.year > 0 && <div className="mb-1.5 text-[10px]" style={{ color: "rgba(255,255,255,.7)" }}>{w.year}</div>}
        <Button variant="primary" size="sm" onClick={onAdd} disabled={disabled} busy={busy} busyLabel="Adding…" className="py-1.5 disabled:opacity-60" aria-label={`Add “${w.title}”`}>
          + Add
        </Button>
      </div>
    </div>
  );
}

function SectionTitle({ children, count }: { children: React.ReactNode; count?: number }) {
  return <h2 className="m-0 mb-3 text-[15px] font-bold">{children}{count !== undefined && <span className="ml-1.5 font-normal text-ink-faint">· {count}</span>}</h2>;
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="mb-8 rounded-xl p-8 text-center text-[12.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>{children}</div>;
}
