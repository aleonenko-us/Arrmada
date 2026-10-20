-- 0347_quality_multi_counts_language (issue #58): a Must audio language now has to be named
-- in a release (ITA, iTALiAN, VFF…) or be the title's original language. A MULTi or Dual
-- Audio release that names no language used to count as maybe carrying it, and was grabbed;
-- now it is refused unless the profile turns this on ("MULTi and Dual Audio releases count
-- as having a required language"), for trackers that label releases carrying the local dub
-- MULTi.
--
-- Every existing profile starts with it off: the stricter reading is the fix.
ALTER TABLE quality_profiles ADD COLUMN multi_counts_language INTEGER NOT NULL DEFAULT 0;
