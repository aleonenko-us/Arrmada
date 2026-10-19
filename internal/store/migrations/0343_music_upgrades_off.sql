-- 0343_music_upgrades_off: music upgrades start off (MUS-16).
--
-- Every music profile saved before the upgrade sweep existed was saved while its
-- "Automatically upgrade" switch was hidden, and the page said albums you already have
-- aren't replaced. Whatever upgrades_enabled holds on them (the presets set it on) was
-- never a choice anyone made, so the sweep would start replacing albums nobody asked to
-- have replaced. They are turned off; the owner turns them on, now that the switch is back.
UPDATE quality_profiles SET upgrades_enabled = 0 WHERE media_type = 'music';
