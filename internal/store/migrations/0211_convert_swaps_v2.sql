-- 0211_convert_swaps_v2: the swap journal records each step before it is taken.
--
-- The journal row used to be written only after the finished encode had been copied next
-- to the original as .arrpart, so a crash mid-copy left a multi-GB orphan nobody knew
-- about, and recovery couldn't tell an original that was never moved aside from one that
-- was. state says how far a swap got:
--
--   staging  the encode is being copied next to the original; the original is untouched
--   staged   the copy is complete; the original is still untouched
--   holding  the original is being moved into the originals hold (hold_path)
--   held     the original is in the hold; the copy is renamed into place next
--   retired  the original was handed to the recycle bin (swaps from before the hold)
--
-- Rows already here when this runs were journalled just before their original went to the
-- recycle bin, so they are 'retired'. op is 'convert' or 'revert' (putting an original back
-- from the hold), and history_id names the ledger row the swap belongs to.
ALTER TABLE convert_swaps ADD COLUMN state TEXT NOT NULL DEFAULT 'retired';
ALTER TABLE convert_swaps ADD COLUMN hold_path TEXT NOT NULL DEFAULT '';
ALTER TABLE convert_swaps ADD COLUMN op TEXT NOT NULL DEFAULT 'convert';
ALTER TABLE convert_swaps ADD COLUMN history_id INTEGER NOT NULL DEFAULT 0;
