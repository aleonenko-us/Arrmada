import { useEffect, useState } from "react";
import { AutoSelect, AutoText } from "../../components/settings/auto";
import { Section } from "../../components/settings/ui";
import { musicApi, type MusicMatching } from "../../lib/musicApi";
import { useAutoSave } from "../../lib/useAutoSave";
import { Button, useToast } from "../../ui";

// The Music matching card (MUS-30/31): how sure the matcher must be before it files a
// download by itself, and which edition of an album is its target. Advanced: the
// defaults suit most libraries, so the card says what each number does and offers to
// put them back. Shown only while the Music module is on (its API answers 404 when off).

type Threshold = "strong" | "expected" | "review" | "track_max" | "gap" | "partial_min";

const THRESHOLDS: { key: Threshold; label: string; help: string }[] = [
  { key: "strong", label: "Import by itself (nobody grabbed it)", help: "The furthest a download nobody grabbed may be from an album and still import without asking. 0 is a perfect fit." },
  { key: "expected", label: "Import by itself (grabbed for the album)", help: "The same for a download grabbed for that album: the grab is extra evidence, so it may be further off." },
  { key: "review", label: "Offer in Reviews up to", help: "Past this, Reviews says it couldn't identify the files and lists the closest albums." },
  { key: "track_max", label: "One file at most", help: "A single file further than this from its track isn't placed; the rest of the album still is." },
  { key: "gap", label: "Two editions this close ask you", help: "When a second, different edition fits within this of the best, Reviews names both." },
  { key: "partial_min", label: "Partial albums import from", help: "The share of an edition's tracks a partial download needs to import by itself (0.5 = half), unless it's exactly the tracks still wanted." },
];

const POLICIES = [
  { value: "original", label: "Original: the first release (Digital Media, then CD)" },
  { value: "most_complete", label: "Most complete: the most tracks" },
  { value: "preferred", label: "Preferred: by the countries and formats below" },
] as const;

function decimal(v: string): string | null {
  const t = v.trim();
  if (!/^\d*\.?\d+$/.test(t)) return "Enter a number from 0 to 1, like 0.15.";
  const n = Number(t);
  return n < 0 || n > 1 ? "Enter a number from 0 to 1." : null;
}

function ThresholdField({ k, label, help, cfg, onSaved }: { k: Threshold; label: string; help: string; cfg: MusicMatching; onSaved: (m: MusicMatching) => void }) {
  const save = useAutoSave(String(cfg[k]), (v) => musicApi.setMatching({ [k]: Number(v) } as Partial<MusicMatching>).then((res) => onSaved(res.settings)), { validate: decimal });
  return (
    <AutoText label={label} save={save} mono={false} inputMode="decimal" className="w-full max-w-[120px] rounded-lg px-3 py-2 text-base">
      <p className="m-0 text-xs text-ink-faint">{help}</p>
    </AutoText>
  );
}

// ScanWorkersField is how many album folders the library scan reads at once (MUS-37).
function ScanWorkersField({ cfg, onSaved }: { cfg: MusicMatching; onSaved: (m: MusicMatching) => void }) {
  const save = useAutoSave(String(cfg.scan_workers), (v) => musicApi.setMatching({ scan_workers: Number(v) }).then((res) => onSaved(res.settings)), {
    validate: (v) => (/^\d+$/.test(v.trim()) && Number(v) >= 1 && Number(v) <= 16 ? null : "Enter a whole number from 1 to 16."),
  });
  return (
    <AutoText label="Library scan reads at once" save={save} mono={false} inputMode="numeric" className="w-full max-w-[120px] rounded-lg px-3 py-2 text-base">
      <p className="m-0 text-xs text-ink-faint">
        How many album folders Scan library reads at the same time. 4 suits most setups; more can help a library on a network share, fewer a small box.
      </p>
    </AutoText>
  );
}

function ListField({ k, label, help, cfg, onSaved }: { k: "edition_countries" | "edition_media"; label: string; help: string; cfg: MusicMatching; onSaved: (m: MusicMatching) => void }) {
  const toList = (v: string) => v.split(",").map((s) => s.trim()).filter(Boolean);
  const save = useAutoSave(cfg[k].join(", "), (v) => musicApi.setMatching({ [k]: toList(v) } as Partial<MusicMatching>).then((res) => onSaved(res.settings)));
  return (
    <AutoText label={label} save={save} mono={false}>
      <p className="m-0 text-xs text-ink-faint">{help}</p>
    </AutoText>
  );
}

export function MusicMatchingSettings() {
  const toast = useToast();
  const [cfg, setCfg] = useState<MusicMatching | null>(null);
  const [defaults, setDefaults] = useState<MusicMatching | null>(null);
  const [off, setOff] = useState(false);
  const [resetting, setResetting] = useState(false);
  // Remounted after a reset so every field shows the stored value again.
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    let live = true;
    musicApi
      .matching()
      .then((res) => {
        if (!live) return;
        setCfg(res.settings);
        setDefaults(res.defaults);
      })
      .catch(() => live && setOff(true)); // Music is off: no card
    return () => {
      live = false;
    };
  }, []);

  const policy = useAutoSave(cfg?.edition_policy ?? "original", (v) =>
    musicApi.setMatching({ edition_policy: v }).then((res) => setCfg(res.settings)),
  );

  if (off || !cfg || !defaults) return null;

  const isDefault = THRESHOLDS.every((t) => cfg[t.key] === defaults[t.key]) && cfg.edition_policy === defaults.edition_policy;
  const reset = async () => {
    setResetting(true);
    try {
      const res = await musicApi.setMatching(defaults);
      setCfg(res.settings);
      setGeneration((g) => g + 1);
      toast("Music matching is back to the defaults.");
    } catch (e) {
      toast((e as Error).message, { tone: "error" });
    } finally {
      setResetting(false);
    }
  };

  return (
    <Section
      id="music-matching"
      title="Music matching"
      subtitle="How sure Arrmada must be before it files a music download by itself, and which edition of an album it aims for. Anything less sure waits in Reviews with the reasons."
    >
      <AutoSelect label="Edition to aim for" save={policy} options={POLICIES}>
        <p className="m-0 text-xs text-ink-faint">
          Every edition of an album is listed; this picks the one its tracks are counted against. A correct download of another edition still completes it,
          and an album page can keep a chosen edition.
        </p>
      </AutoSelect>
      {cfg.edition_policy === "preferred" && (
        <div key={`lists-${generation}`} className="flex flex-col gap-4">
          <ListField k="edition_countries" label="Countries, in order" help="Release countries as MusicBrainz codes, for example GB, US, XW (worldwide)." cfg={cfg} onSaved={setCfg} />
          <ListField k="edition_media" label="Formats, in order" help="For example Digital Media, CD." cfg={cfg} onSaved={setCfg} />
        </div>
      )}
      <div key={`thresholds-${generation}`} className="flex flex-col gap-4">
        {THRESHOLDS.map((t) => (
          <ThresholdField key={t.key} k={t.key} label={t.label} help={t.help} cfg={cfg} onSaved={setCfg} />
        ))}
        <ScanWorkersField cfg={cfg} onSaved={setCfg} />
      </div>
      <div className="flex items-center gap-3">
        <Button size="sm" onClick={() => void reset()} busy={resetting} busyLabel="Resetting…" disabled={isDefault}>
          Reset to defaults
        </Button>
        {isDefault && <span className="text-xs text-ink-faint">Using the defaults.</span>}
      </div>
    </Section>
  );
}
