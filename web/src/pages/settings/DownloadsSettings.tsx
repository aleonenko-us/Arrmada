import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { AutoNumber, AutoText, AutoToggle, SavedTick, wholeNumber } from "../../components/settings/auto";
import { Note, Section, input, inputStyle } from "../../components/settings/ui";
import { Field } from "../../ui/Field";
import { api, type DiskGuardStatus, type RecycleBinStats, type RecycleItem, type RecycleStats } from "../../lib/api";
import { systemApi } from "../../lib/systemApi";
import { LINKS } from "../../lib/links";
import { useAutoSave } from "../../lib/useAutoSave";
import { useLoadedSettings, useSetting, useSettingGroup } from "../../lib/useSettings";

// Settings → Downloads (admin only): the download clients and indexers live on Settings →
// Connections; here are the guard rails around them — the disk guard, the stall timeout and the
// recycle bin that deletes and replacements go through. Each field saves itself.
export function DownloadsSettings() {
  return (
    <div className="flex flex-col gap-6">
      <Section id="download-links" title="Download clients and indexers" subtitle="Both live on Settings → Connections, with their live status: where downloads are sent, and where releases are searched for.">
        <div className="flex flex-wrap gap-x-5 gap-y-2 text-sm font-semibold">
          <Link to={LINKS.downloadClients} style={{ color: "var(--accent-text)" }}>Download clients →</Link>
          <Link to={LINKS.indexers} style={{ color: "var(--accent-text)" }}>Indexers →</Link>
        </div>
      </Section>
      <Section id="search-categories" title="Search categories" subtitle="Which categories an indexer is searched in when it names none of its own (Prowlarr's synced indexers name none).">
        <DefaultCategories />
      </Section>
      <DiskGuardSection />
      <StallSection />
      <UpgradeBudgetSection />
      <RecycleBin />
    </div>
  );
}

// DefaultCategories switches the usual search categories for indexers that name none of
// their own (Prowlarr's synced indexers), so a book search stops coming back with films.
// Off asks every category, as before.
function DefaultCategories() {
  const save = useSetting("indexer_default_categories");
  return (
    <AutoToggle
      label="Search the usual categories"
      hint="Indexers without categories of their own are asked only for the kind of release being searched for — movies, TV, books, audiobooks or music — when they list that category. Turn off if an indexer files releases in odd categories."
      save={save}
    />
  );
}

function fmtBytes(b: number): string {
  if (!b || b <= 0) return "0 MB";
  const tb = b / 1024 ** 4;
  if (tb >= 1) return `${tb.toFixed(2)} TB`;
  const gb = b / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(1)} GB`;
  return `${(b / 1024 ** 2).toFixed(0)} MB`;
}
function fmtDay(unix: number): string {
  return new Date(unix * 1000).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}
function ageOf(unix: number): string {
  const days = Math.floor((Date.now() / 1000 - unix) / 86400);
  return days <= 0 ? "today" : `${days} day${days === 1 ? "" : "s"} ago`;
}

// RecycleBin shows what the bin is holding and lets an admin set the guard rails (max size /
// retention, each saved when its field is left) and empty it on demand. Deleted & replaced
// files (movie/episode deletes, Convert originals) land here instead of being erased.
function RecycleBin() {
  // Blank isn't "unlimited" here (the server would read it as 0): 0 has to be typed.
  const maxGB = useSetting("recycle_max_gb", { validate: wholeNumber(0, 100000, "The size cap") });
  const keepDays = useSetting("recycle_retention_days", { validate: wholeNumber(0, 36500, "Keep for") });
  const [stats, setStats] = useState<RecycleStats | null>(null);
  const [items, setItems] = useState<RecycleItem[] | null>(null);
  const [showItems, setShowItems] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rowBusy, setRowBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);

  const load = () => api.recycleStats().then(setStats).catch(() => setStats(null));
  const loadItems = () => systemApi.recycleItems().then(setItems).catch(() => setItems([]));
  useEffect(() => { load(); }, []);
  useEffect(() => { if (showItems) loadItems(); }, [showItems]);

  // Both "for good" actions ask through the shared dialog; a refusal shows inside it.
  // An empty with a bin empties just that one.
  const [confirm, setConfirm] = useState<{ kind: "empty"; bin?: RecycleBinStats } | { kind: "item"; it: RecycleItem } | null>(null);
  const [confirmErr, setConfirmErr] = useState<string | null>(null);
  const closeConfirm = () => { setConfirm(null); setConfirmErr(null); };

  const empty = async (bin?: RecycleBinStats) => {
    setBusy(true); setMsg(null); setConfirmErr(null);
    try {
      const r = await systemApi.emptyRecycle(bin?.key);
      setMsg(`Freed ${fmtBytes(r.freed_bytes)}.`);
      closeConfirm();
      load(); if (showItems) loadItems();
    } catch (e) { setConfirmErr((e as Error).message); }
    finally { setBusy(false); }
  };

  const restore = async (it: RecycleItem) => {
    setRowBusy(it.id); setMsg(null);
    try { await systemApi.restoreRecycle(it.id); setMsg(`Restored ${it.name}.`); load(); loadItems(); }
    catch (e) { setMsg((e as Error).message); }
    finally { setRowBusy(null); }
  };
  const deleteItem = async (it: RecycleItem) => {
    setRowBusy(it.id); setMsg(null); setConfirmErr(null);
    try { await systemApi.deleteRecycleItem(it.id); closeConfirm(); load(); loadItems(); }
    catch (e) { setConfirmErr((e as Error).message); }
    finally { setRowBusy(null); }
  };

  const bins = stats?.bins ?? [];
  const emptying = confirm?.kind === "empty" ? confirm : null;

  return (
    <Section id="recycle-bin" title="Recycle bin" subtitle="Deleted & replaced files (movie/episode deletes and Convert originals) are moved into a hidden .arrmada-recycle folder inside the library they came from — a quick move on the same drive, hidden from Plex — instead of being erased, so a mistake can be undone until the guard rails below purge it. The size cap counts every bin together, and the oldest files go first once they're over it. Convert only starts a file whose original fits under the cap. To restore a converted film, delete the converted file first: the bin won't restore over it.">
      {stats && !stats.enabled ? (
        <p className="text-sm text-ink-dim">Recycling is turned off (<code>ARRMADA_RECYCLE_DIR=off</code>) — deleted files are erased immediately, and Convert deletes each original once its conversion is verified, with no undo.</p>
      ) : (
        <>
          <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1 text-base">
            <span>Holding <b>{stats ? fmtBytes(stats.bytes) : "…"}</b>{stats ? ` · ${stats.files} file${stats.files === 1 ? "" : "s"}` : ""}</span>
            {stats?.oldest_unix ? <span className="text-ink-faint">oldest {ageOf(stats.oldest_unix)}</span> : null}
          </div>
          {bins.length > 0 && (
            <div className="rounded-lg" style={{ border: "1px solid var(--line)" }}>
              {bins.map((b, i) => (
                <div key={b.key} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2" style={i > 0 ? { borderTop: "1px solid var(--line-soft)" } : undefined}>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2 text-base font-medium">
                      {b.label}
                      {b.legacy && <span className="rounded px-1.5 py-0.5 text-xs font-semibold" style={{ border: "1px solid var(--avoid)", color: "var(--avoid-text)" }}>Legacy bin</span>}
                    </div>
                    <div className="truncate font-mono text-xs text-ink-faint" title={b.dir}>{b.dir}</div>
                    {b.legacy && <div className="text-xs text-ink-faint">Where deletes used to go. Only files outside every library folder land here now — empty it once you've checked it.</div>}
                    {b.other_drive && <div className="text-xs" style={{ color: "var(--avoid-text)" }}>On a different drive from your library — every delete into it is a full copy.</div>}
                  </div>
                  <span className="flex-none font-mono text-xs text-ink-faint">{fmtBytes(b.bytes)} · {b.files} file{b.files === 1 ? "" : "s"}{b.free_known ? ` · ${fmtBytes(b.free_bytes)} free` : ""}</span>
                  {bins.length > 1 && (
                    <button onClick={() => setConfirm({ kind: "empty", bin: b })} disabled={busy || b.files === 0} className="flex-none rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-40" style={{ border: "1px solid var(--line)", color: "var(--reject-text)" }}>Empty this bin</button>
                  )}
                </div>
              ))}
            </div>
          )}
          {stats && stats.over_cap_bytes > 0 && (
            <p className="m-0 text-sm" style={{ color: "var(--avoid-text)" }}>
              Over the cap by {fmtBytes(stats.over_cap_bytes)} — {stats.protected_bytes > 0 && stats.over_cap_bytes <= stats.protected_bytes
                ? "items from the last 3 days are kept until they're older."
                : "the next hourly clean-up will trim it."}
            </p>
          )}
          {stats && stats.max_gb > 0 && stats.largest_item_bytes > stats.max_gb * 1024 ** 3 && (
            <p className="m-0 text-sm" style={{ color: "var(--avoid-text)" }}>Your cap ({stats.max_gb} GB) is smaller than the largest file here ({fmtBytes(stats.largest_item_bytes)}).</p>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <AutoNumber label="Max size (GB)" save={maxGB} min={0} max={100000} placeholder="0">
              <span className="text-xs text-ink-faint">0 = unlimited. Over this, the oldest files are purged first — never anything deleted in the last 3 days, or the newest file.</span>
            </AutoNumber>
            <AutoNumber label="Keep for (days)" save={keepDays} min={0} max={36500} placeholder="0">
              <span className="text-xs text-ink-faint">0 = keep forever. Older files are auto-deleted.</span>
            </AutoNumber>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <button onClick={() => setShowItems((v) => !v)} disabled={(stats?.files ?? 0) === 0} className="rounded-lg px-4 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>{showItems ? "Hide contents" : `Manage contents${stats ? ` (${stats.files})` : ""}`}</button>
            <button onClick={() => setConfirm({ kind: "empty" })} disabled={busy || (stats?.files ?? 0) === 0} className="rounded-lg px-4 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--reject)", color: "var(--reject-text)" }}>{busy ? "Emptying…" : "Empty now"}</button>
            {msg && <span className="text-sm text-ink-dim">{msg}</span>}
          </div>

          {showItems && (
            <div className="rounded-lg" style={{ border: "1px solid var(--line)" }}>
              {items === null ? (
                <div className="p-4 text-center text-sm text-ink-dim">Loading…</div>
              ) : items.length === 0 ? (
                <div className="p-4 text-center text-sm text-ink-dim">The recycle bin is empty.</div>
              ) : (
                <div className="thin-scroll max-h-[340px] overflow-y-auto">
                  {items.map((it) => (
                    <div key={it.id} className="flex items-center gap-3 px-3 py-2" style={{ borderTop: "1px solid var(--line-soft)" }}>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-base font-medium" title={it.name}>{it.name}</div>
                        <div className="truncate font-mono text-xs text-ink-faint" title={it.orig_path || "origin not recorded"}>{it.orig_path || "origin not recorded"}</div>
                        {bins.length > 1 && <div className="text-xs text-ink-faint">In: {it.bin_label}{it.legacy ? " (legacy bin)" : ""}</div>}
                        {it.expires_at > 0 && <div className="text-xs text-ink-faint">Deleted for good on {fmtDay(it.expires_at)}</div>}
                      </div>
                      <span className="flex-none font-mono text-xs text-ink-faint">{fmtBytes(it.size_bytes)}</span>
                      <span className="hidden flex-none font-mono text-xs text-ink-faint sm:block">{ageOf(it.deleted_unix)}</span>
                      <button onClick={() => restore(it)} disabled={!it.restorable || rowBusy !== null} title={it.restorable ? "Move back to its original location" : "Original location wasn't recorded for this item"} className="flex-none rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-40" style={{ border: "1px solid var(--accent-line)", color: "var(--accent-text)" }}>{rowBusy === it.id ? "…" : "Restore"}</button>
                      <button onClick={() => setConfirm({ kind: "item", it })} disabled={rowBusy !== null} title="Delete permanently" className="flex-none rounded-md px-2.5 py-1 text-xs font-semibold disabled:opacity-40" style={{ border: "1px solid var(--line)", color: "var(--reject-text)" }}>Delete</button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          <p className="text-xs text-ink-faint">Guard rails run automatically about once an hour. Restore moves a file back to where it was deleted from (when that location is free).</p>
        </>
      )}
      {emptying && (
        <ConfirmDialog
          title={emptying.bin ? <>Empty the “{emptying.bin.label}” bin?</> : bins.length > 1 ? "Empty every recycle bin?" : "Empty the recycle bin?"}
          body={emptying.bin
            ? <>Permanently deletes <b>{fmtBytes(emptying.bin.bytes)}</b> ({emptying.bin.files} file{emptying.bin.files === 1 ? "" : "s"}) from {emptying.bin.dir}. The other bins are left alone. This can't be undone.</>
            : <>Permanently deletes {stats ? <b>{fmtBytes(stats.bytes)}</b> : "everything"}{stats ? ` (${stats.files} file${stats.files === 1 ? "" : "s"})` : ""}. This can't be undone.</>}
          confirmLabel={emptying.bin ? "Delete this bin's files for good" : "Delete everything for good"}
          busyLabel="Emptying…"
          busy={busy}
          error={confirmErr}
          onConfirm={() => empty(emptying.bin)}
          onCancel={closeConfirm}
        />
      )}
      {confirm?.kind === "item" && (
        <ConfirmDialog
          title={<>Delete “{confirm.it.name}” for good?</>}
          body={<>Permanently deletes {fmtBytes(confirm.it.size_bytes)}. It can't be restored afterwards.</>}
          confirmLabel="Delete for good"
          busyLabel="Deleting…"
          busy={rowBusy !== null}
          error={confirmErr}
          onConfirm={() => deleteItem(confirm.it)}
          onCancel={closeConfirm}
        />
      )}
    </Section>
  );
}

// A full downloads volume errors every torrent at once and, on a shared cache pool,
// takes everything else on that pool down with it. This is on by default for that
// reason — it isn't a preference anyone opts into deliberately after the fact.
// The pause and resume points save together, whichever field is left: the server checks
// them as a pair, and checking one against a half-edited other would refuse a good change.
type GuardPair = { downloads_disk_guard_pause_pct: string; downloads_disk_guard_resume_pct: string };
const GUARD_KEYS = ["downloads_disk_guard_pause_pct", "downloads_disk_guard_resume_pct"] as const;
export function guardProblem(v: GuardPair): string | null {
  const p = v.downloads_disk_guard_pause_pct.trim(), r = v.downloads_disk_guard_resume_pct.trim();
  if (!/^\d+$/.test(p) || Number(p) < 1 || Number(p) > 99) return "Pause needs a percentage from 1 to 99.";
  if (!/^\d+$/.test(r)) return "Resume needs a percentage below the pause point.";
  // Mirrors the server's rule. Equal or inverted thresholds would pause and resume on
  // alternate passes forever, so the server rejects them — say so before sending.
  if (Number(r) >= Number(p)) return `Resume (${r}%) must be below pause (${p}%) — otherwise downloads would pause and resume on alternate checks.`;
  return null;
}

function DiskGuardSection() {
  const on = useSetting("downloads_disk_guard");
  const pair = useSettingGroup(GUARD_KEYS, { validate: guardProblem });
  const digits = (v: string) => v.replace(/[^0-9]/g, "").slice(0, 3);
  const edit = (p: Partial<GuardPair>) => pair.set({ ...pair.value, ...p });
  const keys = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") { e.preventDefault(); void pair.commit(); }
    if (e.key === "Escape" && pair.dirty) { e.preventDefault(); pair.revert(); }
  };
  const fieldStyle = pair.status === "error" ? { ...inputStyle, border: "1px solid var(--reject)" } : inputStyle;

  // The guard measures the downloads folder (lib_downloads_dir, picked in Settings → Library;
  // ARRMADA_DOWNLOADS_DIR is only the fallback) and nothing else. Whether that path is
  // the torrent drive is not knowable from in here, so show the resolved path and the
  // reading taken from it and let the user confirm it against their own setup.
  const [status, setStatus] = useState<DiskGuardStatus | null>(null);
  useEffect(() => {
    api.diskGuard().then(setStatus).catch(() => setStatus(null));
  }, []);

  return (
    <Section
      id="disk-guard"
      title="Download disk guard"
      subtitle="Pause downloads before the downloads volume fills up. A full disk errors every torrent at once, and on a shared cache pool it takes everything else on that pool with it. Seeding torrents are never paused — they aren't writing anything, and pausing them would put your seed goals at risk."
    >
      <Note tone="warn">
        <b>This only works if your torrents live on their own drive.</b> The guard measures
        one folder: your Downloads folder, set in{" "}
        <Link to={LINKS.libraryFolders} style={{ color: "var(--accent-text)" }}>Settings → Library</Link>
        {status?.path ? <> (currently <code>{status.path}</code>)</> : null}. If that folder is on
        your main array rather than the torrent or cache drive, the percentage measures the
        array, and it will either never trigger or pause your queue for a reason that has
        nothing to do with downloads — choose the right folder there. A changed folder is
        watched from the next check, a minute at most.
      </Note>

      {status && (
        <div className="rounded-lg p-3" style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}>
          <div className="text-xs uppercase tracking-[0.1em] text-ink-faint">Currently watching</div>
          <div className="mt-0.5 break-all font-mono text-sm">
            {status.path || "(not set)"}
          </div>
          {status.measurable ? (
            <div className="mt-1 text-sm text-ink-dim">
              {status.used_pct.toFixed(1)}% full
              {status.holding > 0 && (
                <span style={{ color: "var(--avoid-text)" }}>
                  {" "}· holding {status.holding} torrent{status.holding === 1 ? "" : "s"} paused
                </span>
              )}
            </div>
          ) : (
            <div className="mt-1 text-sm" style={{ color: "var(--reject-text)" }}>
              Arrmada can't read this folder's disk usage, so the guard will do nothing. Check the
              path exists and is mounted into the container.
            </div>
          )}
          {(status.shared_with?.length ?? 0) > 0 && (
            <div className="mt-1.5 text-sm" style={{ color: "var(--reject-text)" }}>
              Shares a drive with: {status.shared_with.map((f) => f.label).join(", ")}.
              The guard will be measuring your whole array, not a torrent drive — a threshold like
              85% almost certainly isn't what you want here.
            </div>
          )}
        </div>
      )}

      <AutoToggle
        label="Pause downloads when the disk gets full"
        hint="Checked every minute. Only torrents Arrmada paused are resumed — anything you paused by hand stays paused."
        save={on}
      />
      {on.value && (
        <>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Pause at (% full)" hint="Active downloads are paused once the volume is this full.">
              <input
                inputMode="numeric"
                value={pair.value.downloads_disk_guard_pause_pct}
                onChange={(e) => edit({ downloads_disk_guard_pause_pct: digits(e.target.value) })}
                onBlur={() => void pair.commit()}
                onKeyDown={keys}
                aria-invalid={pair.status === "error" || undefined}
                placeholder="85"
                className={input}
                style={fieldStyle}
              />
            </Field>
            <Field label="Resume at (% full)" hint="They restart once it drops back to this. Must be below the pause point.">
              <input
                inputMode="numeric"
                value={pair.value.downloads_disk_guard_resume_pct}
                onChange={(e) => edit({ downloads_disk_guard_resume_pct: digits(e.target.value) })}
                onBlur={() => void pair.commit()}
                onKeyDown={keys}
                aria-invalid={pair.status === "error" || undefined}
                placeholder="80"
                className={input}
                style={fieldStyle}
              />
            </Field>
          </div>
          <SavedTick save={pair} />
        </>
      )}
    </Section>
  );
}

// The global stall timeout, shown in hours and stored in minutes. Profiles left on "Use
// default" follow it. Safe to have on: a stalled download is only removed once another
// release has been grabbed in its place.
const hoursText = (minutes: number) => String(Math.round((minutes / 60) * 10) / 10);
export const stallHoursProblem = (v: string) => (/^\d+(\.\d*)?$/.test(v.trim()) && Number(v) <= 168 ? null : "Hours needs a number from 0 to 168.");
function StallSection() {
  const { s, commit } = useLoadedSettings();
  const hours = useAutoSave(hoursText(s.downloads_stall_minutes ?? 360), (v) => commit({ downloads_stall_minutes: Math.round(Number(v) * 60) }), {
    validate: stallHoursProblem,
    // 6 and 6.0 are the same timeout.
    equal: (a, b) => a.trim() !== "" && b.trim() !== "" && Number(a) === Number(b),
  });
  return (
    <Section
      id="stalled-downloads"
      title="Stalled downloads"
      subtitle="When a download makes no progress for this long, Arrmada looks for another release. The stalled one is only removed once a replacement has been grabbed — with nothing else available it's left to keep trying. Time spent paused, queued by the client, rechecking or held by the disk guard doesn't count."
    >
      <AutoText label="Give up on a download with no progress after (hours)" save={hours} mono={false} inputMode="decimal" filter={(v) => v.replace(/[^0-9.]/g, "").slice(0, 5)}>
        <span className="text-xs text-ink-faint">0 = never. Quality profiles can set their own time, or turn it off.</span>
      </AutoText>
    </Section>
  );
}

// How many upgrades one upgrade sweep (movies and TV each, every six hours) may grab. A
// profile edit can make hundreds of files eligible at once; this spreads them out.
function UpgradeBudgetSection() {
  const { s, commit } = useLoadedSettings();
  const budget = useAutoSave(String(s.upgrade_max_grabs_per_sweep ?? 10), (v) => commit({ upgrade_max_grabs_per_sweep: Number(v) }), {
    validate: wholeNumber(0, 1000, "Upgrades per sweep"),
  });
  return (
    <Section
      id="upgrade-limit"
      title="Upgrades per sweep"
      subtitle="Each upgrade sweep — movies and TV separately — grabs at most this many better releases. The rest wait for the next sweep, so changing a quality profile can't start hundreds of downloads at once. Upgrading a single title by hand is never held back."
    >
      <AutoNumber label="Upgrades per sweep" save={budget} min={0} max={1000}>
        <span className="text-xs text-ink-faint">0 = no limit. Each episode counts as one.</span>
      </AutoNumber>
    </Section>
  );
}
