import { useState } from "react";
import type { Book, BookEditField } from "../lib/api";
import { bookEditApi, type BookEditRequest, type BookEditResult } from "../lib/bookEditApi";
import { langName, useLanguages } from "../lib/languages";
import { useUnsavedGuard } from "../lib/useUnsavedGuard";
import { Button, Modal } from "../ui";
import { Field } from "../ui/Field";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";

// EditBookDetails is the book page's "Edit details" dialog (loaded on open). Each detail
// can be typed over what the catalogue (Open Library / Hardcover) says; the catalogue's
// value stays underneath. An edited detail is marked "edited", shows the catalogue's
// value, and has "Reset to matched"; "Reset all" puts every one back. Nothing is saved
// until Save, and nothing on disk is renamed by it — the page offers the rename preview
// when the edit changed what the files would be called.

type Kind = "text" | "number" | "textarea" | "language" | "tags";

interface FieldDef {
  key: BookEditField;
  label: string;
  kind: Kind;
  placeholder?: string;
  hint?: string;
  maxLength?: number;
  /** Shown only for a book with an audiobook (or a value already typed). */
  audio?: boolean;
}

const FIELDS: FieldDef[] = [
  { key: "title", label: "Title", kind: "text", maxLength: 300 },
  { key: "subtitle", label: "Subtitle", kind: "text", maxLength: 300 },
  { key: "author", label: "Author(s)", kind: "text", maxLength: 300, hint: "Several authors: separate them with commas or “&”." },
  { key: "series_name", label: "Series", kind: "text", maxLength: 200 },
  { key: "series_position", label: "Number in series", kind: "number", placeholder: "e.g. 2 or 1.5" },
  { key: "year", label: "Published", kind: "number", placeholder: "Year" },
  { key: "language", label: "Language", kind: "language" },
  { key: "publisher", label: "Publisher", kind: "text", maxLength: 200 },
  { key: "isbn", label: "ISBN", kind: "text", placeholder: "ISBN-10 or ISBN-13", maxLength: 20 },
  { key: "narrator", label: "Narrator(s)", kind: "text", maxLength: 200, audio: true, placeholder: "From the audio files' tags" },
  { key: "subjects", label: "Tags", kind: "tags", placeholder: "Fantasy, Coming of age", hint: "Separate tags with commas." },
  { key: "description", label: "Description", kind: "textarea" },
];

type Raw = string | number | string[] | undefined;

// asText is a detail as the form shows it: numbers 0 as empty, tags comma-joined.
function asText(key: BookEditField, v: Raw): string {
  if (v === undefined || v === null) return "";
  if (Array.isArray(v)) return v.join(", ");
  if (typeof v === "number") return v === 0 && (key === "year" || key === "series_position") ? "" : String(v);
  return v;
}

// shown is the book's detail as it is now (an edit, or the catalogue's).
function shown(b: Book, key: BookEditField): string {
  switch (key) {
    case "narrator": return b.audiobook_narrator ?? "";
    case "subjects": return asText(key, b.subjects ?? []);
    case "year": return asText(key, b.year);
    case "series_position": return asText(key, b.series_position ?? 0);
    case "title": return b.title;
    case "author": return b.author;
    case "series_name": return b.series_name ?? "";
    case "description": return b.description ?? "";
    default: return (b[key] as string | undefined) ?? "";
  }
}

// toApi is a typed value as the API takes it.
function toApi(key: BookEditField, text: string): string | number | string[] {
  const t = text.trim();
  if (key === "subjects") return t.split(",").map((s) => s.trim()).filter(Boolean);
  if (key === "year" || key === "series_position") return t === "" ? 0 : t;
  return key === "description" ? text : t;
}

export function EditBookDetails({ book, onClose, onSaved }: { book: Book; onClose: () => void; onSaved: (r: BookEditResult) => void }) {
  const edited = book.edited ?? {};
  const languages = useLanguages();
  const [values, setValues] = useState<Record<BookEditField, string>>(() =>
    Object.fromEntries(FIELDS.map((f) => [f.key, shown(book, f.key)])) as Record<BookEditField, string>);
  const [resets, setResets] = useState<Set<BookEditField>>(new Set());
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const isEdited = (k: BookEditField) => edited[k] !== undefined && !resets.has(k);
  const matchedText = (k: BookEditField) => asText(k, edited[k]);
  const changed = (k: BookEditField) => resets.has(k) || values[k] !== shown(book, k);
  const dirty = FIELDS.some((f) => changed(f.key));
  useUnsavedGuard(dirty);

  const setValue = (k: BookEditField, v: string) => {
    setValues((cur) => ({ ...cur, [k]: v }));
    setResets((cur) => { if (!cur.has(k)) return cur; const next = new Set(cur); next.delete(k); return next; });
  };
  const reset = (k: BookEditField) => {
    setValues((cur) => ({ ...cur, [k]: matchedText(k) }));
    setResets((cur) => new Set(cur).add(k));
  };
  const editedKeys = FIELDS.map((f) => f.key).filter((k) => edited[k] !== undefined);
  const resetAll = () => editedKeys.forEach(reset);

  const save = async () => {
    const body: BookEditRequest = {};
    const set: NonNullable<BookEditRequest["set"]> = {};
    const reset: BookEditField[] = [];
    for (const f of FIELDS) {
      if (resets.has(f.key) && edited[f.key] !== undefined) reset.push(f.key);
      else if (values[f.key] !== shown(book, f.key)) set[f.key] = toApi(f.key, values[f.key]);
    }
    if (Object.keys(set).length > 0) body.set = set;
    if (reset.length > 0) body.reset = reset;
    if (!body.set && !body.reset) { onClose(); return; }
    if (set.title !== undefined && String(set.title).trim() === "") { setErr("The title can't be empty."); return; }
    setBusy(true); setErr(null);
    try {
      const r = await bookEditApi.editDetails(book.id, body);
      onSaved(r);
      onClose();
    } catch (e) { setErr((e as Error).message); setBusy(false); }
  };

  const hintFor = (f: FieldDef) => {
    if (!isEdited(f.key)) return f.hint;
    const m = matchedText(f.key);
    const shownM = f.kind === "language" ? langName(m, languages) : m;
    const what = f.key === "narrator" ? (m ? shownM : "the audio files' tags") : (m ? shownM : "nothing");
    return (
      <span className="flex flex-wrap items-baseline gap-x-2">
        <span className="min-w-0 break-words">Matched: <span className="text-ink-dim">{f.kind === "textarea" && what.length > 140 ? `${what.slice(0, 140)}…` : what}</span></span>
        <button type="button" onClick={() => reset(f.key)} className="font-semibold underline-offset-2 hover:underline" style={{ color: "var(--accent-text)" }}
          aria-label={`Reset ${f.label.toLowerCase()} to matched`}>
          Reset to matched
        </button>
      </span>
    );
  };

  // The marker is part of the label, so a screen reader hears "Title edited" too.
  const label = (f: FieldDef) => (
    <>
      {f.label}
      {isEdited(f.key) && <>{" "}<span className="ml-1 rounded px-1 py-px font-mono text-2xs normal-case tracking-normal" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }} data-testid={`edited-${f.key}`}>edited</span></>}
    </>
  );

  const control = (f: FieldDef) => {
    const common = { value: values[f.key], onChange: (e: { target: { value: string } }) => setValue(f.key, e.target.value), disabled: busy };
    switch (f.kind) {
      case "textarea": return <Textarea {...common} rows={5} />;
      case "language":
        return (
          <Select {...common}>
            <option value="">Not set</option>
            {values[f.key] && !languages?.some((l) => l.code === values[f.key]) && <option value={values[f.key]}>{langName(values[f.key], languages)}</option>}
            {(languages ?? []).map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
          </Select>
        );
      case "number": return <Input {...common} inputMode="decimal" placeholder={f.placeholder} />;
      default: return <Input {...common} placeholder={f.placeholder} maxLength={f.maxLength} />;
    }
  };

  const visible = FIELDS.filter((f) => !f.audio || book.audiobook || (book.audio_versions?.length ?? 0) > 0 || values[f.key] || edited[f.key] !== undefined);
  const pair = new Set<BookEditField>(["series_position", "year", "language", "publisher", "isbn"]);

  return (
    <Modal
      onClose={onClose}
      title="Edit details"
      size="lg"
      dismissible={!busy}
      footer={<>
        {editedKeys.length > 0 && (
          <Button variant="ghost" onClick={resetAll} disabled={busy || editedKeys.every((k) => resets.has(k))} className="mr-auto" style={{ border: "1px solid var(--line)" }}>
            Reset all
          </Button>
        )}
        <Button variant="ghost" onClick={onClose} disabled={busy} style={{ border: "1px solid var(--line)" }}>Cancel</Button>
        <Button variant="primary" onClick={save} busy={busy} busyLabel="Saving…" disabled={!dirty}>Save</Button>
      </>}
    >
      <p className="mb-3 mt-1 text-sm text-ink-dim">
        Type over anything the catalogue got wrong. What it matched stays underneath, so you can put any detail back, and refreshing the book never undoes an edit.
      </p>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {visible.map((f) => (
          <Field key={f.key} label={label(f)} hint={hintFor(f)} className={pair.has(f.key) ? "" : "sm:col-span-2"}>
            {control(f)}
          </Field>
        ))}
      </div>
      {err && <p role="alert" className="mb-0 mt-3 text-sm" style={{ color: "var(--reject-text)" }}>{err}</p>}
    </Modal>
  );
}
