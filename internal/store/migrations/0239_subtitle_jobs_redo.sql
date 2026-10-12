-- 0239_subtitle_jobs_redo: a per-language redo's overrides survive a restart.
--
-- A redo (mode next | ai | extract | pick, its languages in langs) may name the audio
-- track the AI listens to (audio_stream, 0:a:N), the embedded track to extract
-- (track_index, 0:s:N) or the OpenSubtitles file to use (pick_file_id). NULL = the job
-- picks, as before.
ALTER TABLE subtitle_jobs ADD COLUMN audio_stream INTEGER;
ALTER TABLE subtitle_jobs ADD COLUMN track_index INTEGER;
ALTER TABLE subtitle_jobs ADD COLUMN pick_file_id TEXT;
