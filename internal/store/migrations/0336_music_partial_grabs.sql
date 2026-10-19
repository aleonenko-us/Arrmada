-- 0336_music_partial_grabs: an album grab used to become 'imported' only once the whole
-- album was on disk, and seed cleanup only reads imported grabs, so a download that filled
-- 11 of 12 tracks seeded forever and its seed rule never ran. The import now marks the
-- grab imported as soon as it places anything (its hash goes into imports at that moment);
-- this heals the grabs left behind: still 'grabbed', but already imported by hash.
UPDATE grabs SET status = 'imported'
 WHERE media_type = 'music' AND status = 'grabbed' AND info_hash != ''
   AND lower(info_hash) IN (SELECT lower(download_hash) FROM imports);
