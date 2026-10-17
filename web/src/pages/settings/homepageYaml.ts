// The services.yaml entry for gethomepage.dev's "customapi" widget, with this install's
// address and widget key filled in. docs/homepage.md has the same snippet and every field
// the widget answers with; keep the two in step.
//
// Homepage shows at most four blocks per widget, so the entry is one service with two
// widgets: four headline counts as blocks, then a short list for the rest.

export const HOMEPAGE_ICON = "https://raw.githubusercontent.com/tristenlammi/Arrmada/main/web/public/icon-192.png";

// The key goes in through a Homepage variable when it isn't to hand (it's only shown once).
export const HOMEPAGE_KEY_VAR = "{{HOMEPAGE_VAR_ARRMADA_WIDGET_KEY}}";

export const WIDGET_PATH = "/api/v1/widget/homepage";

// homepageYaml is one service in a "Media" group. origin is Arrmada's address as Homepage's
// server reaches it (no trailing slash); key the widget key, or the variable when empty.
export function homepageYaml(origin: string, key: string): string {
  const base = origin.replace(/\/+$/, "");
  const head = (refresh: number) => [
    "          - type: customapi",
    `            url: ${base}${WIDGET_PATH}`,
    `            refreshInterval: ${refresh}`,
    "            headers:",
    `              X-Api-Key: ${key || HOMEPAGE_KEY_VAR}`,
  ];
  const map = (field: string, label: string, extra: string[] = []) => [
    `              - field: ${field}`,
    `                label: ${label}`,
    ...extra.map((l) => `                ${l}`),
  ];
  const num = ["format: number"];
  return [
    "- Media:",
    "    - Arrmada:",
    `        icon: ${HOMEPAGE_ICON}`,
    `        href: ${base}`,
    "        description: Movies, TV, books and requests",
    "        widgets:",
    ...head(30000),
    "            mappings:",
    ...map("movies", "Movies", num),
    ...map("shows", "Shows", num),
    ...map("wanted.total", "Wanted", num),
    ...map("needs_you", "Needs you", num),
    ...head(60000),
    "            display: list",
    "            mappings:",
    ...map("queue.downloading", "Downloading", num),
    ...map("queue.stalled", "Stalled", num),
    ...map("requests_pending", "Requests waiting", num),
    ...map("health.problems", "Health problems", num),
    ...map("disk.lowest_free_bytes", "Free space", ["format: bytes"]),
    ...map("version", "Version"),
    ...map("update_available", "Update", ["remap:", "  - value: true", "    to: Available", "  - any: true", "    to: Up to date"]),
  ].join("\n") + "\n";
}
