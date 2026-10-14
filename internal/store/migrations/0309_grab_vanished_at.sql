-- 0309_grab_vanished_at: when a grab was failed over because its torrent left the client.
--
-- That fail-over no longer blocklists the release the first time: a torrent disappearing
-- says nothing about the release (removed by hand or by another tool, or the client lost
-- its state), and the old blocklist rows piled up — hundreds at once, right after one
-- update. Without the row, the stall check has to know a vanished download apart from a
-- stale copy of one: every pending row of a download is closed with it, so a pending row
-- of a release whose earlier grab carries vanished_at is that release grabbed again, and
-- its own disappearance is blocklisted (internal/automation/vanished.go). Rows closed
-- before this keep 0 and are judged as they always were.
--
-- Unix seconds; 0 = not failed over as vanished.

ALTER TABLE grabs ADD COLUMN vanished_at INTEGER NOT NULL DEFAULT 0;
