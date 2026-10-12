-- 0212_convert_history_repeats: the conversion ledger records every outcome now, the
-- waits included (still seeding, no room yet, already matches). A file that hits the same
-- one again — a seeding file is looked at twice a day — updates its row instead of adding
-- another, and repeats counts how many times it has, so the History tab shows "×N" rather
-- than a page of copies.
ALTER TABLE convert_history ADD COLUMN repeats INTEGER NOT NULL DEFAULT 1;
