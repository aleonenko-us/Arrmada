-- 0238_subtitle_sync_check: what a sync check did to a sidecar, and when.
--
-- sync_offset_ms and sync_confidence (0222) say how a subtitle lined up with the speech
-- when it was checked. A download that ran late is shifted into sync before it is saved,
-- so its Health needs to say that too ("shifted -3.0 s"): sync_shift_ms is the shift
-- Arrmada applied (NULL = none), sync_scale the frame-rate stretch applied with it
-- (NULL = none), and sync_checked_at when the check ran (unix seconds; NULL = never
-- checked, which the Library shows as "not checked").
ALTER TABLE subtitle_files ADD COLUMN sync_shift_ms INTEGER;
ALTER TABLE subtitle_files ADD COLUMN sync_scale REAL;
ALTER TABLE subtitle_files ADD COLUMN sync_checked_at INTEGER;
