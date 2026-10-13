-- 0305_book_search_spread: books that builds before the search ladder had given up on come
-- back gradually, not all in the first sweep after the update.
--
-- Before BOOK-04 the missing-books sweep tried a book twice and then never again. The
-- ladder (internal/books/ladder.go) never gives up: after 2 empty searches it waits 3
-- days, after 3 to 10 a week, after more a month. Every book an older build abandoned
-- was searched long ago, so on the first sweep after the update all of them were due at
-- once, and the sweep worked through the backlog 40 at a time, every 30 minutes, against
-- the same trackers.
--
-- Each overdue book (monitored, 2 or more empty searches, last search longer ago than its
-- wait) is given a last search somewhere inside its own wait instead, picked by its id,
-- so the backlog comes due spread across that wait: a third each day for the 3-day step,
-- a seventh each day for the weekly one. No book waits longer than the ladder's own wait
-- for its count, and a book still inside its wait isn't touched. The miss count stays as
-- it is.
UPDATE books
   SET last_search_at = datetime('now', '-' || (id % CASE
           WHEN search_misses = 2 THEN 3
           WHEN search_misses <= 10 THEN 7
           ELSE 30 END) || ' days')
 WHERE monitored = 1
   AND search_misses >= 2
   AND (last_search_at IS NULL OR last_search_at = '' OR last_search_at < datetime('now', '-' || CASE
           WHEN search_misses = 2 THEN 3
           WHEN search_misses <= 10 THEN 7
           ELSE 30 END || ' days'));
