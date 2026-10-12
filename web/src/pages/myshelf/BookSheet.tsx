import { useState } from "react";
import { api, type MyBook } from "../../lib/api";
import { posterThumb } from "../../lib/img";
import { playAudiobook } from "../../lib/playerStub";
import { KindleButton } from "../../components/Kindle";
import { Sheet } from "../../ui";
import { fmtSize, seriesLabel } from "./shelf";

// BookSheet is one book from My shelf: the cover, who wrote it, where it sits in its
// series, the blurb, what's here (ebook, audiobooks) and what you can do with it —
// download, send to a Kindle, listen — plus the rest of its series on the shelf. Its own
// chunk, loaded on the first tap; deep-linked as /shelf?book=<id>, so Back closes it.

export function BookSheet({ book, shelf, listen, onClose, onOpen }: {
  book: MyBook;
  /** The whole shelf, for "More in this series". */
  shelf: readonly MyBook[];
  /** The caller may play audiobooks here (the audiobook server is on and allows them). */
  listen: boolean;
  onClose: () => void;
  onOpen: (id: number) => void;
}) {
  const [more, setMore] = useState(false);
  const series = seriesLabel(book);
  const siblings = book.series_name
    ? shelf.filter((b) => b.book_id !== book.book_id && b.series_name?.toLowerCase() === book.series_name?.toLowerCase())
      .sort((a, b) => (a.series_position || 1e9) - (b.series_position || 1e9) || a.title.localeCompare(b.title))
    : [];
  const meta = { title: book.title, author: book.author, cover: book.cover_url };
  const facts = [book.year ? String(book.year) : "", book.ebook ? `Ebook · ${(book.ebook.format || "").toUpperCase()}${book.ebook.size_bytes ? ` ${fmtSize(book.ebook.size_bytes)}` : ""}` : "", book.audiobook ? "Audiobook" : ""].filter(Boolean);

  return (
    <Sheet onClose={onClose} closeOnBack={false} ariaLabel={book.title} size="lg">
      <div className="flex flex-col gap-5 p-5 sm:p-6">
        <div className="flex gap-4">
          <div className="w-[104px] flex-none overflow-hidden rounded-lg sm:w-[132px]" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }}>
            {book.cover_url && <img src={posterThumb(book.cover_url)} alt="" className="h-full w-full object-cover" />}
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="m-0 text-[17px] font-bold leading-snug">{book.title}</h2>
            {book.author && <div className="mt-0.5 text-[12.5px] text-ink-dim">{book.author}</div>}
            {series && <div className="mt-0.5 text-[12px] text-ink-faint">{series}</div>}
            {facts.length > 0 && <div className="mt-1 text-[11.5px] text-ink-faint">{facts.join(" · ")}</div>}
            {book.subjects && book.subjects.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-1">
                {book.subjects.map((s) => (
                  <span key={s} className="rounded-full px-2 py-0.5 text-[10.5px] text-ink-dim" style={{ border: "1px solid var(--line)" }}>{s}</span>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {book.ebook && (
            <a href={api.ebookDownloadURL(book.book_id)} download className="rounded-lg px-4 py-2 text-[13px] font-semibold" style={{ background: "var(--accent)", color: "var(--accent-ink)" }}>
              Download {book.ebook.format ? book.ebook.format.toUpperCase() : "ebook"}
            </a>
          )}
          {book.ebook && <KindleButton book={book} />}
        </div>

        {(book.audiobooks ?? []).length > 0 && (
          <section aria-label="Audiobooks" className="flex flex-col gap-1.5">
            <h3 className="m-0 text-[13.5px] font-bold">Audiobook{(book.audiobooks ?? []).length > 1 ? "s" : ""}</h3>
            {(book.audiobooks ?? []).map((a) => (
              <div key={a.version_id} className="flex flex-wrap items-center gap-2">
                {listen && a.item_key && (
                  <button
                    onClick={() => { playAudiobook(a.item_key!, { meta }); onClose(); }}
                    className="rounded-lg px-3 py-1.5 text-[12.5px] font-semibold"
                    style={{ background: "var(--accent-soft)", border: "1px solid var(--accent-line)", color: "var(--accent)" }}
                  >
                    ▶ Listen{a.label ? ` · ${a.label}` : ""}
                  </button>
                )}
                <a href={api.audiobookDownloadURL(book.book_id, a.version_id)} download className="rounded-lg px-3 py-1.5 text-[12.5px] font-semibold" style={{ border: "1px solid var(--accent-line)", color: "var(--accent)" }}>
                  Download{a.label && !(listen && a.item_key) ? ` · ${a.label}` : ""}{a.size_bytes ? ` · ${fmtSize(a.size_bytes)}` : ""}{a.files > 1 ? " (zip)" : ""}
                </a>
              </div>
            ))}
          </section>
        )}

        {book.description && (
          <div className="text-[12.5px] leading-relaxed text-ink-dim">
            <p className={`m-0 whitespace-pre-line ${more ? "" : "line-clamp-5"}`}>{book.description}</p>
            {book.description.length > 300 && (
              <button onClick={() => setMore(!more)} className="mt-1 text-[12px] font-semibold" style={{ color: "var(--accent)" }}>{more ? "Less" : "More"}</button>
            )}
          </div>
        )}

        {siblings.length > 0 && (
          <section aria-label={`More in ${book.series_name}`}>
            <h3 className="m-0 mb-2 text-[13.5px] font-bold">More in this series</h3>
            <div className="thin-scroll flex gap-3 overflow-x-auto pb-1">
              {siblings.map((b) => (
                <button key={b.book_id} onClick={() => onOpen(b.book_id)} className="w-[88px] flex-none text-left" aria-label={`${b.title}${b.series_position ? `, #${b.series_position}` : ""}`}>
                  <div className="overflow-hidden rounded-lg" style={{ aspectRatio: "2/3", background: "var(--panel-2)" }}>
                    {b.cover_url && <img src={posterThumb(b.cover_url)} alt="" className="h-full w-full object-cover" loading="lazy" />}
                  </div>
                  <div className="mt-1 truncate text-[11px] font-semibold">{b.series_position ? `#${b.series_position} ` : ""}{b.title}</div>
                </button>
              ))}
            </div>
          </section>
        )}
      </div>
    </Sheet>
  );
}
