-- 0275_quality_group_tiers: each profile's release-group tiers (QUAL-26).
--
-- JSON: {"tier1": [...], "tier2": [...]}. '' (every existing profile) means the built-in
-- suggestions in quality/presets.go. Tiers only order releases under ranking v2 (tier 1
-- above tier 2 above untiered, after source) and never reject anything. See
-- quality/grouptiers.go.
ALTER TABLE quality_profiles ADD COLUMN group_tiers TEXT NOT NULL DEFAULT '';
