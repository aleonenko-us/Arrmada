-- 0272_quality_unwanted: each profile's choices for the built-in Unwanted pack.
--
-- JSON: {"items": {"<item>": "reject"|"avoid"|"allow"}, "low_quality_groups": [...]}.
-- '' (every existing profile) means the defaults: a full disc image, 3D and extras are
-- refused; an upscale, a foreign-language-only release, a low-quality group and Dolby
-- Vision with no HDR fallback rank in the avoided tier. A missing item is its default, and
-- a missing group list is the default list (YIFY, YTS, MeGusta). See quality/unwanted.go.
ALTER TABLE quality_profiles ADD COLUMN unwanted TEXT NOT NULL DEFAULT '';
