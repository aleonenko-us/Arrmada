-- 0336_music_partial_grabs: an album grab used to become 'imported' only once the whole
-- album was on disk, and seed cleanup only reads imported (and dismissed) grabs, so a
-- download that filled 11 of 12 tracks seeded forever and its seed rule never ran. The
-- import now closes the grab as soon as it places anything; this closes the grabs left
-- behind: still 'grabbed', but already imported by hash.
--
-- They become 'dismissed', not 'imported'. Seed cleanup removes a dismissed torrent at its
-- seed goal but KEEPS its files, while an imported one may go with its data. From here
-- there's no telling whether such a download still holds files the import left for a
-- person (ones that matched no track), so the closing that can't lose them is the one used.
UPDATE grabs SET status = 'dismissed'
 WHERE media_type = 'music' AND status = 'grabbed' AND info_hash != ''
   AND lower(info_hash) IN (SELECT lower(download_hash) FROM imports);
