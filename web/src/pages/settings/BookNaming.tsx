import { useEffect, useState } from "react";
import { BookOrganizeModal } from "../../components/BookOrganizeModal";
import { AutoText } from "../../components/settings/auto";
import { Section, TokenList } from "../../components/settings/ui";
import { bookNamingApi, type BookNaming, type BookNamingPreset, type BookNamingPreview } from "../../lib/bookNamingApi";
import { useLoadedSettings, useSetting } from "../../lib/useSettings";
import { Button, useConfirm, useToast } from "../../ui";

// A blank template would quietly fall back to the default, so say so instead of saving it.
const notBlank = (v: string) => (v.trim() === "" ? "A name template can't be empty. Esc puts the saved one back." : null);

// Shown until the presets load (and if they can't): the server's token list is the same.
const FALLBACK_TOKENS = ["Author", "Author Sort", "Title", "Series", "Series Position", "Year", "Narrator", "Edition", "Format", "Part", "Part Count", "Original"];

const FIELD_NAME = { folder: "Folder", file: "File", track: "Track files" } as const;

// usePreview renders the sample books with the templates as typed — a moment after the
// last keystroke, so typing doesn't send a request per letter. The server renders them, so
// the preview is exactly what an import would do.
function usePreview(kind: "audiobook" | "ebook", folder: string, file: string, track?: string): BookNamingPreview | null {
  const [preview, setPreview] = useState<BookNamingPreview | null>(null);
  useEffect(() => {
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      bookNamingApi.preview(kind, { folder, file, track }, ctl.signal)
        .then(setPreview)
        .catch(() => { /* cancelled by the next keystroke, or offline: keep the last preview */ });
    }, 250);
    return () => { clearTimeout(timer); ctl.abort(); };
  }, [kind, folder, file, track]);
  return preview;
}

function Samples({ preview, label }: { preview: BookNamingPreview | null; label: string }) {
  if (!preview) return <div className="text-xs text-ink-faint">Rendering the examples…</div>;
  if (preview.error) {
    return (
      <div className="text-xs" style={{ color: "var(--reject-text)" }}>
        {FIELD_NAME[preview.error.field]}: {preview.error.message}
      </div>
    );
  }
  return (
    <div role="group" aria-label={label} className="flex flex-col gap-2 rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
      {preview.samples.map((s) => (
        <div key={s.label}>
          <div className="text-xs text-ink-faint">{s.label}</div>
          {s.paths.map((p) => (
            <div key={p} className="break-all font-mono text-xs" style={{ color: "var(--accent-text)" }}>{p}</div>
          ))}
        </div>
      ))}
    </div>
  );
}

const same = (a: BookNaming, b: BookNaming) => a.folder === b.folder && a.file === b.file && (a.track ?? "") === (b.track ?? "");

// BookNamingSettings: how imported audiobooks and ebooks are named (issue #25), with a
// live preview on sample books, ready-made schemes, and the rename that applies a scheme
// to the books already in the library. Each template saves when you leave the field.
export function BookNamingSettings() {
  const aFolder = useSetting("naming_audiobook_folder", { validate: notBlank });
  const aFile = useSetting("naming_audiobook_file", { validate: notBlank });
  const aTrack = useSetting("naming_audiobook_track", { validate: notBlank });
  const eFolder = useSetting("naming_ebook_folder", { validate: notBlank });
  const eFile = useSetting("naming_ebook_file", { validate: notBlank });
  const confirm = useConfirm();
  const toast = useToast();
  const [presets, setPresets] = useState<BookNamingPreset[]>([]);
  const [tokens, setTokens] = useState<string[]>(FALLBACK_TOKENS);
  const [applying, setApplying] = useState<string | null>(null);
  const [organizing, setOrganizing] = useState(false);

  useEffect(() => {
    let alive = true;
    bookNamingApi.presets()
      .then((r) => { if (alive) { setPresets(r.presets); setTokens(r.tokens); } })
      .catch(() => { /* no presets: the templates still work by hand */ });
    return () => { alive = false; };
  }, []);

  const audioPreview = usePreview("audiobook", aFolder.value, aFile.value, aTrack.value);
  const ebookPreview = usePreview("ebook", eFolder.value, eFile.value);

  const { s } = useLoadedSettings();
  const saved = {
    audiobook: { folder: s.naming_audiobook_folder, file: s.naming_audiobook_file, track: s.naming_audiobook_track },
    ebook: { folder: s.naming_ebook_folder, file: s.naming_ebook_file },
  };
  const applyPreset = async (p: BookNamingPreset) => {
    const ok = await confirm({
      title: `Use ${p.label}?`,
      body: "The audiobook and ebook templates are replaced. Books already in your library keep their names until you rename them.",
      confirmLabel: "Use it",
    });
    if (!ok) return;
    setApplying(p.id);
    try {
      const done = await Promise.all([
        aFolder.commit(p.audiobook.folder),
        aFile.commit(p.audiobook.file),
        aTrack.commit(p.audiobook.track ?? ""),
        eFolder.commit(p.ebook.folder),
        eFile.commit(p.ebook.file),
      ]);
      if (done.every(Boolean)) toast(`Book naming set to ${p.label}.`);
    } finally {
      setApplying(null);
    }
  };

  return (
    <Section id="book-naming" title="Book naming" subtitle="How imported audiobooks and ebooks are named. Put / between folders.">
      {presets.length > 0 && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-2">
            {presets.map((p) => {
              const inUse = same(saved.audiobook, p.audiobook) && same(saved.ebook, p.ebook);
              return (
                <Button key={p.id} size="sm" variant={inUse ? "secondary" : "primary"} onClick={() => void applyPreset(p)}
                  busy={applying === p.id} busyLabel="Saving…" disabled={inUse || applying !== null} title={p.description}>
                  {inUse ? `Using ${p.label}` : p.label}
                </Button>
              );
            })}
          </div>
          <span className="text-xs text-ink-faint">A preset fills in every template below; adjust any of them after.</span>
        </div>
      )}

      <h3 className="m-0 text-base font-semibold">Audiobooks</h3>
      <AutoText label="Audiobook folder" save={aFolder} />
      <AutoText label="Audiobook file" save={aFile}>
        <span className="text-xs text-ink-faint">For an audiobook that is one file.</span>
      </AutoText>
      <AutoText label="Audiobook track files" save={aTrack}>
        <span className="text-xs text-ink-faint">For an audiobook in several files. <code className="font-mono">{"{Original}"}</code> keeps each file’s own name; <code className="font-mono">{"{Part}"}</code> numbers them in play order.</span>
      </AutoText>
      <Samples preview={audioPreview} label="Audiobook examples" />

      <h3 className="m-0 text-base font-semibold">Ebooks</h3>
      <AutoText label="Ebook folder" save={eFolder} />
      <AutoText label="Ebook file" save={eFile} />
      <Samples preview={ebookPreview} label="Ebook examples" />

      <TokenList tokens={tokens} />
      <p className="m-0 text-xs text-ink-faint">
        A folder that comes out empty is left out, and so is a separator next to an empty token: <code className="font-mono">{"{Author}/{Series}/{Series Position} - {Title}"}</code> puts a book in no series straight under its author.
        Wrap a part in <code className="font-mono">{"< >"}</code> to write it only when its tokens have values, and use <code className="font-mono">|</code> for an alternative — <code className="font-mono">{"{Author}/<{Series}|Standalone>/<{Series Position} - >{Title}"}</code>.
        Pad numbers with zeros: <code className="font-mono">{"{Series Position:00}"}</code>. Every book gets a folder of its own, so the last folder needs <code className="font-mono">{"{Title}"}</code>. A second audiobook version (“Full cast”) is always kept apart in “Title (Full cast)” unless the folder names <code className="font-mono">{"{Edition}"}</code>.
      </p>
      <div className="flex flex-wrap items-center gap-2.5 text-sm text-ink-faint">
        <Button size="sm" variant="secondary" onClick={() => setOrganizing(true)}>Preview book rename…</Button>
        <span>Books already in your library keep their names until you rename them. Listening places are kept.</span>
        {organizing && <BookOrganizeModal onClose={() => setOrganizing(false)} />}
      </div>
    </Section>
  );
}
