-- 0259_user_kindle: each person's Send-to-Kindle address (BOOK-24).
--
-- Amazon gives every Kindle an address (name_42@kindle.com); a book emailed there from an
-- approved sender lands on the device. It's per person, set by that person on My shelf, and
-- '' means they haven't set one. Nothing records which books were sent: the result goes to
-- the sender's own inbox only.
ALTER TABLE users ADD COLUMN kindle_email TEXT NOT NULL DEFAULT '';
