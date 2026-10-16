import { req, type JobRef, type MediaRequest } from "./api";

// Release previews for requests waiting for approval: staff-only calls, kept apart from
// api.ts so they download with the staff preview panel, never with a requester's load.

export const requestPreviewApi = {
  /** Checks what approving the request would grab, now: a job to follow ("Checking indexers…"). */
  refresh: (id: number, qualityProfile?: string) =>
    req<JobRef>(`/api/v1/requests/${id}/preview`, { method: "POST", body: JSON.stringify({ quality_profile: qualityProfile ?? "" }) }),
  /**
   * "Approve & grab this": approves the request and grabs the release the preview found,
   * when it can still be had; the answer's approve_grab says whether it searched instead.
   * previewCheckedAt is the preview the approver saw — one checked again since is a 409.
   */
  approveAndGrab: (id: number, body: { quality_profile?: string; seasons?: number[]; folder_id?: number; preview_checked_at: number }) =>
    req<MediaRequest>(`/api/v1/requests/${id}/approve`, {
      method: "POST",
      body: JSON.stringify({ ...body, quality_profile: body.quality_profile ?? "", grab_preview: true }),
    }),
};
