-- 0298_notification_options: per-connection choices that aren't how to reach it.
--
-- config holds what a connection is (a preset's form fields, a push connection's user)
-- and is rebuilt whenever its destination is edited. options holds what the admin chose
-- about the messages themselves, kept apart so editing a webhook can't drop them:
--   {"stream_users": ["<plex user id>", …], "remote_only": true, "transcode_only": true,
--    "templates": {"<event key>": "<message text with {placeholders}>"}}
-- Nothing secret goes here; it's shown back to the Alerts page as is.
ALTER TABLE notifications ADD COLUMN options TEXT NOT NULL DEFAULT '{}';
