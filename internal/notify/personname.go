package notify

import "strings"

// PersonName is how an alert or a webhook names a person: the first of names that isn't
// blank (callers put a Plex or Jellyfin name before the account name), with an email
// address cut down to the part before the @. An account name is what someone signs in
// with, often their email, and no alert or webhook may carry one (catalog_security.go).
// All blank is "".
func PersonName(names ...string) string {
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if local, _, ok := strings.Cut(n, "@"); ok {
			if local = strings.TrimSpace(local); local == "" {
				return "Someone"
			}
			return local
		}
		return n
	}
	return ""
}
