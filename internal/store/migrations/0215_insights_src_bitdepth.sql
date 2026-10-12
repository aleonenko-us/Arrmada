-- 0215_insights_src_bitdepth: the source video's bit depth and codec profile for each
-- recorded play, from the session's video stream. Convert's "Your devices" panel uses them
-- to tell which players direct-play HEVC 10-bit (and which transcode it) before Convert
-- writes 10-bit files. 0 / '' = not reported: every play recorded before this, and Tautulli
-- imports — the panel shows those as "not seen" for 10-bit rather than guessing.
ALTER TABLE stream_sessions ADD COLUMN video_src_bitdepth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stream_sessions ADD COLUMN video_src_profile TEXT NOT NULL DEFAULT '';
