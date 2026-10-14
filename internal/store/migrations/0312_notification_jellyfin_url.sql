-- 0312_notification_jellyfin_url: a 'ready' notice for a title the owner's Jellyfin has keeps
-- the title's page in the Jellyfin web app (at its public address), next to plex_url, so the
-- bell can offer a Watch button per server. Empty for every other notice, and for every
-- notice sent before Jellyfin was connected.
ALTER TABLE user_notifications ADD COLUMN jellyfin_url TEXT NOT NULL DEFAULT '';
