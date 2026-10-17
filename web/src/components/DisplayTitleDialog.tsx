import { useEffect, useState } from "react";
import { displayTitleApi, type DisplayTitleState, type TitleChoice, type TitleMedia } from "../lib/displayTitleApi";
import { langName, useLanguages } from "../lib/languages";
import { Button, ErrorState, Modal, Skeleton, useToast } from "../ui";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";

// DisplayTitleDialog is a movie's or show's "Display title…": which name the app shows for
// this one title, picked from the names TMDB has (English, original, translations) or
// typed. Presentation only — matching, file and folder names and the API's title keep the
// English title, and a metadata refresh never changes the pick. "Follow the setting" clears
// it, back to Settings → Library → "Show titles as". Lazy-loaded on open.

const KIND_LABEL: Record<TitleChoice["kind"], string> = { english: "English (TMDB)", original: "Original", translation: "Translation" };

export default function DisplayTitleDialog({ media, id, title, override, onClose, onSaved }: {
  media: TitleMedia;
  id: number;
  /** The canonical (English) title. */
  title: string;
  /** The current override ("" follows the setting). */
  override: string;
  onClose: () => void;
  onSaved: (s: DisplayTitleState) => void;
}) {
  const toast = useToast();
  const langs = useLanguages();
  const [choices, setChoices] = useState<TitleChoice[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [typed, setTyped] = useState(override);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    displayTitleApi.choices(media, id)
      .then((c) => { if (live) setChoices(c); })
      .catch((e: Error) => { if (live) setError(e.message); });
    return () => { live = false; };
  }, [media, id]);

  const save = async (value: string) => {
    setBusy(true);
    try {
      const s = await displayTitleApi.set(media, id, value);
      toast(value ? `Shown as “${s.display_title}”.` : "Follows the setting again.", { tone: "good" });
      onSaved(s);
      onClose();
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setBusy(false);
    }
  };

  const label = (c: TitleChoice) => {
    const lang = c.lang ? langName(c.lang, langs) : "";
    const region = c.country ? ` (${c.country})` : "";
    return c.kind === "translation" ? `${lang}${region}` : c.kind === "original" && lang ? `${KIND_LABEL.original} · ${lang}` : KIND_LABEL[c.kind];
  };

  return (
    <Modal
      onClose={onClose}
      title="Display title"
      size="md"
      footer={
        <>
          {override && <Button variant="ghost" disabled={busy} onClick={() => void save("")}>Follow the setting</Button>}
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button disabled={busy || !typed.trim() || typed.trim() === override} onClick={() => void save(typed.trim())}>Show this title</Button>
        </>
      }
    >
      <p className="text-sm text-ink-dim">
        Which name Arrmada shows for “{title}”. Matching, file and folder names and the API keep the English title, and a refresh never changes your pick.
      </p>
      {error && <div className="mt-3"><ErrorState what="the other titles" message={error} /></div>}
      {!error && !choices && <div className="mt-3"><Skeleton variant="list" count={3} /></div>}
      {choices && (
        <ul className="m-0 mt-3 flex max-h-[40vh] list-none flex-col gap-1 overflow-y-auto p-0 thin-scroll" aria-label="Known titles">
          {choices.map((c) => (
            <li key={`${c.kind}:${c.lang ?? ""}:${c.country ?? ""}:${c.title}`}>
              <button
                type="button"
                aria-pressed={typed.trim() === c.title}
                onClick={() => setTyped(c.title)}
                className="flex w-full items-baseline justify-between gap-3 rounded-lg px-2.5 py-1.5 text-left hover:bg-panel-2"
                style={typed.trim() === c.title ? { background: "var(--panel-2)", outline: "1px solid var(--accent)" } : undefined}
              >
                <span className="min-w-0 truncate text-md">{c.title}</span>
                <span className="flex-none font-mono text-2xs uppercase text-ink-faint">{label(c)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <Field className="mt-3" label="Title to show" hint="Pick one above or type your own.">
        <Input value={typed} maxLength={200} onChange={(e) => setTyped(e.target.value)} />
      </Field>
    </Modal>
  );
}
