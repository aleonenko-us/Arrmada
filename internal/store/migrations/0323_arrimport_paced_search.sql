-- 0323_arrimport_paced_search: titles brought over from Radarr or Sonarr wait their turn.
--
-- A Radarr/Sonarr import can add hundreds of monitored titles without a file at once. The
-- missing sweeps search every such title each run, so without this the next sweep would
-- search (and grab) all of them together. search_paced = 1 marks a title added in bulk:
-- each sweep searches only a few marked titles and clears the mark on the ones it searched,
-- so the import's wanted titles are worked through over several runs. Every existing title
-- is 0 — nothing already in the library changes how it's searched.
ALTER TABLE movies ADD COLUMN search_paced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN search_paced INTEGER NOT NULL DEFAULT 0;
