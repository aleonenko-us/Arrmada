package httpapi

import "context"

// watchIdentities is the ids an Arrmada user's plays are recorded under on the media
// servers — their Plex account id and their Jellyfin user id — for the personal Discover
// rows built from watch history. Plays on either server count; none when the account is
// linked to neither.
//
// TODO(J4): add the Jellyfin user id once accounts carry it (users.jellyfin_id, migration
// 0311): something like `if id := a.deps.Auth.JellyfinIDForUser(ctx, userID); id != ""`.
// insights.RecentlyWatchedByUser already takes it.
func (a *api) watchIdentities(ctx context.Context, userID int64) []string {
	var ids []string
	if a.deps.Auth == nil {
		return nil
	}
	if id := a.deps.Auth.PlexIDForUser(ctx, userID); id != "" {
		ids = append(ids, id)
	}
	return ids
}
