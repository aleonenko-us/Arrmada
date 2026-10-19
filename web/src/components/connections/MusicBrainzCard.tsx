import { useEffect, useState } from "react";
import { formatBytes } from "../../lib/format";
import { musicBrainzApi, type MusicBrainzSettings } from "../../lib/musicBrainzApi";
import { setQueryData, useQuery } from "../../lib/query";
import { Button, useToast } from "../../ui";
import { Field } from "../../ui/Field";
import { Input } from "../../ui/Input";

// MusicBrainz (admin only): which server Music asks — the public one, or a mirror the
// owner runs, which may be on the LAN — how fast a mirror may be asked, and a contact for
// the User-Agent. Every answer is kept for good, so the cache line says how much is held.
const QUERY_KEY = "musicbrainz-settings";

export function MusicBrainzCard() {
  const q = useQuery(QUERY_KEY, musicBrainzApi.get, { staleMs: 0 });
  const toast = useToast();
  const [base, setBase] = useState("");
  const [rate, setRate] = useState("1");
  const [contact, setContact] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; detail: string } | null>(null);

  const load = (s: MusicBrainzSettings) => {
    setBase(s.base_url);
    setRate(String(s.mirror_rate));
    setContact(s.contact);
  };
  const data = q.data;
  useEffect(() => { if (data) load(data); }, [data]);

  if (!data) {
    return q.error ? <p role="alert" className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>{q.error.message}</p> : null;
  }
  const mirror = base.trim() !== "";
  const dirty = base !== data.base_url || rate !== String(data.mirror_rate) || contact !== data.contact;

  const save = async () => {
    setError(null);
    const n = Number(rate);
    if (!Number.isInteger(n) || n < 1 || n > data.max_mirror_rate) {
      setError(`A mirror's rate is a whole number from 1 to ${data.max_mirror_rate}.`);
      return;
    }
    setSaving(true);
    try {
      const s = await musicBrainzApi.save({ base_url: base.trim(), mirror_rate: n, contact: contact.trim() });
      setQueryData(QUERY_KEY, s); // the answer is the saved state
      load(s);
      toast("MusicBrainz settings saved");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const test = async () => {
    setTesting(true);
    setResult(null);
    try {
      setResult(await musicBrainzApi.test(base.trim()));
    } catch (e) {
      setResult({ ok: false, detail: e instanceof Error ? e.message : String(e) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div id="musicbrainz" className="flex scroll-mt-20 flex-col gap-3 rounded-xl p-4" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-md font-semibold">MusicBrainz</span>
        <span className="rounded-full px-2 py-0.5 text-xs font-semibold" style={{ background: "var(--panel-2)", color: "var(--ink-faint)" }}>
          {data.base_url ? "Mirror" : "Public server"}
        </span>
      </div>
      <p className="m-0 text-xs text-ink-faint">
        Where Music looks up artists, albums and track listings. Arrmada asks MusicBrainz directly, one request a
        second, and keeps every answer, so pages open from the cache and keep working when MusicBrainz is down.
        Point it at your own MusicBrainz mirror to drop the limit.
      </p>
      {data.paused && (
        <p role="status" className="m-0 text-sm" style={{ color: "var(--avoid-text)" }}>
          MusicBrainz is refusing requests just now; Music is using cached answers.
        </p>
      )}
      <Field label="Server" hint={`Leave empty for ${data.default_base_url}. A mirror on your network is fine.`}>
        <Input value={base} onChange={(e) => setBase(e.target.value)} placeholder={data.default_base_url} spellCheck={false} autoComplete="off" />
      </Field>
      {mirror && (
        <Field label="Mirror requests a second" hint={`1 to ${data.max_mirror_rate}. The public server always gets one a second.`}>
          <Input type="number" min={1} max={data.max_mirror_rate} value={rate} onChange={(e) => setRate(e.target.value)} className="w-24 px-3 py-2 font-mono text-base" />
        </Field>
      )}
      <Field label="Contact email (optional)" hint="Sent with each request, so MusicBrainz can reach you rather than block the address.">
        <Input type="email" value={contact} onChange={(e) => setContact(e.target.value)} placeholder="you@example.com" autoComplete="off" />
      </Field>
      {error && <p role="alert" className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>}
      {result && (
        <p role="status" className="m-0 text-sm" style={{ color: result.ok ? "var(--good-text)" : "var(--reject-text)" }}>
          {result.detail}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="primary" onClick={() => void save()} disabled={!dirty} busy={saving} busyLabel="Saving…">Save</Button>
        <Button size="sm" variant="secondary" onClick={() => void test()} busy={testing} busyLabel="Testing…">Test</Button>
        <span className="text-xs text-ink-faint">
          Cache: {data.cache.entries.toLocaleString()} answers, {formatBytes(data.cache.bytes)}
          {data.cache.stale > 0 ? ` · ${data.cache.stale.toLocaleString()} due a recheck` : ""}
        </span>
      </div>
    </div>
  );
}
