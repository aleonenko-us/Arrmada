import type { Tone } from "../ui";

// One vocabulary for where a download has got to (the server's internal/pipeline), shared
// by the staff Activity queue and the requester's request cards, so both call the same
// download by the same word. Staff see every stage with the reason behind it; a requester
// only ever gets the plain ones (the server maps the rest before they leave it).

export type PipelineStage =
  | "queued" | "fetching_metadata" | "downloading" | "stalled" | "paused" | "paused_by_guard" | "failed"
  | "downloaded" | "importing" | "import_retrying" | "needs_review" | "wrong_category"
  | "imported" | "seeding" | "not_managed" | "removed";

export const STAGE: Record<PipelineStage, { label: string; tone: Tone }> = {
  queued: { label: "Queued", tone: "faint" },
  fetching_metadata: { label: "Fetching metadata", tone: "faint" },
  downloading: { label: "Downloading", tone: "accent" },
  stalled: { label: "Stalled", tone: "avoid" },
  paused: { label: "Paused", tone: "faint" },
  paused_by_guard: { label: "Paused · disk full", tone: "avoid" },
  failed: { label: "Failed", tone: "avoid" },
  downloaded: { label: "Downloaded", tone: "accent" },
  importing: { label: "Importing", tone: "accent" },
  import_retrying: { label: "Import failed", tone: "reject" },
  needs_review: { label: "Held", tone: "avoid" },
  wrong_category: { label: "Wrong category", tone: "avoid" },
  imported: { label: "Imported", tone: "good" },
  seeding: { label: "Seeding", tone: "good" },
  not_managed: { label: "Not managed", tone: "faint" },
  removed: { label: "Removed", tone: "faint" },
};

export type QueueGroup = "downloading" | "finishing" | "seeding";

// stageGroup is the Queue section a transfer sits in: still arriving, finished but not in
// the library yet (Importing, Held, Import failed, Wrong category), or done and sharing.
// Without a stage (an older server) it falls back to progress, as the page used to.
export function stageGroup(stage: string | undefined, progress: number): QueueGroup {
  switch (stage) {
    case "queued": case "fetching_metadata": case "downloading": case "stalled": case "paused": case "paused_by_guard": case "failed":
      return "downloading";
    case "downloaded": case "importing": case "import_retrying": case "needs_review": case "wrong_category":
      return "finishing";
    case "imported": case "seeding": case "not_managed":
      return "seeding";
  }
  return progress < 1 ? "downloading" : "seeding";
}

export function stageLabel(stage: string | undefined): string | undefined {
  return stage && Object.prototype.hasOwnProperty.call(STAGE, stage) ? STAGE[stage as PipelineStage].label : undefined;
}
