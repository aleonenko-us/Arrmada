package httpapi

import "context"

// watchIdentities is the ids an Arrmada user's plays are recorded under on the media
// servers — their Plex account id (users.plex_id) and their Jellyfin user id
// (users.jellyfin_id) — for the personal Discover rows built from watch history. Plays on
// either server count; none when the account is linked to neither.
func (a *api) watchIdentities(ctx context.Context, userID int64) []string {
	if a.deps.Auth == nil {
		return nil
	}
	var ids []string
	if id := a.deps.Auth.PlexIDForUser(ctx, userID); id != "" {
		ids = append(ids, id)
	}
	if id := a.deps.Auth.JellyfinIDForUser(ctx, userID); id != "" {
		ids = append(ids, id)
	}
	return ids
}
