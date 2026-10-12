import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { req, type ConvertDevice, type ConvertDeviceCodec, type ConvertDevices } from "../../lib/api";
import { formatAgo } from "../../lib/format";
import { LINKS } from "../../lib/links";

// "Your devices": what each Plex player did with HEVC, HEVC 10-bit and AV1 over the last 90
// days of plays Insights recorded — so the formats Convert writes are chosen on evidence, not
// a guess about every TV in the house. A ✗ means Plex transcoded that format's video for the
// device; it can also be a bandwidth or setting limit, so it's worded "transcoded", never
// "can't play". Remote plays fitted to the connection don't count either way (server side).

export type DeviceFormat = "hevc" | "hevc10" | "av1";

export const FORMAT_LABEL: Record<DeviceFormat, string> = { hevc: "HEVC", hevc10: "HEVC 10-bit", av1: "AV1" };

/** The recorded devices, fetched once (null while loading or when it can't be read). The
 *  call lives here rather than in lib/api's table, which is in every requester's first load. */
export function useConvertDevices(): ConvertDevices | null {
  const [d, setD] = useState<ConvertDevices | null>(null);
  useEffect(() => { req<ConvertDevices>("/api/v1/convert/devices").then(setD).catch(() => {}); }, []);
  return d;
}

/** Devices whose video was only ever transcoded in that format — the ones to warn about. */
export function transcodingDevices(d: ConvertDevices | null, format: DeviceFormat): ConvertDevice[] {
  return (d?.devices ?? []).filter((x) => x[format].state === "no");
}

/** "Bedroom Roku (Plex for Roku)" — the player's own name, and what it runs. */
export function deviceName(x: ConvertDevice): string {
  const name = x.player || x.product || x.platform || "A device";
  return x.product && x.product !== name ? `${name} (${x.product})` : name;
}

/** A sentence naming the devices that transcoded a format, for a warning. */
export function transcodedSentence(list: ConvertDevice[], format: DeviceFormat): string {
  if (list.length === 0) return "";
  const names = list.slice(0, 4).map(deviceName);
  const more = list.length > names.length ? ` and ${list.length - names.length} more` : "";
  return `In the last 90 days Plex transcoded ${FORMAT_LABEL[format]} video for ${names.join(", ")}${more}. ` +
    "Files converted to it would likely be transcoded for them too.";
}

function Cell({ c, format, device }: { c: ConvertDeviceCodec; format: DeviceFormat; device: string }) {
  const [mark, color, words] = c.state === "yes"
    ? ["✓", "var(--good)", `played ${FORMAT_LABEL[format]} as it was (${c.direct}×)`]
    : c.state === "no"
      ? ["✗", "var(--avoid)", `${FORMAT_LABEL[format]} was transcoded (${c.transcoded}×)`]
      : ["?", "var(--ink-faint)", `no ${FORMAT_LABEL[format]} played yet`];
  return (
    <td className="px-2 py-1 text-center font-mono" style={{ color }} title={words} aria-label={`${device}: ${words}`}>{mark}</td>
  );
}

export function DevicesPanel({ data }: { data: ConvertDevices | null }) {
  if (!data) return null;
  const rows = data.devices;
  return (
    <div className="flex flex-col gap-1.5" aria-label="Your devices" role="region">
      <div className="text-[12px] font-semibold">Your devices</div>
      <div className="text-[10.5px] text-ink-faint">
        From the plays Plex reported over the last 90 days: ✓ played as it was, ✗ Plex transcoded it, ? not seen yet.
        10-bit is known only for plays recorded since this update.
      </div>
      {!data.monitoring && (
        <div className="text-[11px]" style={{ color: "var(--avoid)" }}>
          Plex watch monitoring is off, so no new plays are recorded{rows.length ? " — this is what it saw while it was on" : ""}.{" "}
          <Link to={LINKS.plexMonitoring} className="font-semibold" style={{ color: "var(--accent)" }}>Turn it on</Link>
        </div>
      )}
      {rows.length === 0 ? (
        <div className="text-[11.5px] text-ink-dim">No plays recorded yet — the table fills in as people watch.</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[420px] text-[11.5px]">
            <thead>
              <tr className="text-left text-[10.5px] uppercase tracking-wide text-ink-faint">
                <th className="py-1 pr-2 font-semibold">Device</th>
                <th className="px-2 py-1 font-semibold">Last seen</th>
                <th className="px-2 py-1 text-center font-semibold">HEVC</th>
                <th className="px-2 py-1 text-center font-semibold">HEVC 10-bit</th>
                <th className="px-2 py-1 text-center font-semibold">AV1</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((x) => {
                const name = deviceName(x);
                return (
                  <tr key={`${x.product}|${x.platform}|${x.player}`} className="border-t" style={{ borderColor: "var(--line-soft)" }}>
                    <td className="py-1 pr-2">{name}</td>
                    <td className="whitespace-nowrap px-2 py-1 text-ink-faint">{formatAgo(x.last_seen * 1000)}</td>
                    <Cell c={x.hevc} format="hevc" device={name} />
                    <Cell c={x.hevc10} format="hevc10" device={name} />
                    <Cell c={x.av1} format="av1" device={name} />
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
