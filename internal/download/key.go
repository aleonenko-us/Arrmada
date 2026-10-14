package download

import "strings"

// A download key is how Arrmada names one download in a client, and it is what grabs,
// imports and reviews store in their hash columns (grabs.info_hash, imports.download_hash,
// import_reviews.hash). A torrent's key is its info hash, as it always was. A usenet job's
// key is "sab-" plus SABnzbd's nzo_id, lowercased: the columns are compared with lower()
// everywhere, and the prefix keeps a job id from ever being mistaken for a torrent hash.

// Transport is how a download travels: BitTorrent or Usenet. A client handles exactly one,
// and a release can only be handed to a client of its own transport.
type Transport string

const (
	TransportTorrent Transport = "torrent"
	TransportUsenet  Transport = "usenet"
)

// usenetKeyPrefix starts every usenet download key.
const usenetKeyPrefix = "sab-"

// maxUsenetIDLen bounds the id after the prefix. SABnzbd's ids are about 20 characters;
// the cap only keeps an absurd value out of URLs and logs.
const maxUsenetIDLen = 80

// ValidKey reports whether key names exactly one download: a torrent's info hash (40 or
// 64 hex characters, either case, as ValidHash) or a usenet key ("sab-" and 1–80 of
// a-z, 0-9 and "_", lowercase only).
//
// Nothing else may reach a client. qBittorrent reads "all" as every torrent it holds and
// "a|b" as several, and SABnzbd takes comma-separated ids — a delete carrying such a value
// empties the whole client. So "all", separators of any kind, an empty value and a bare
// prefix are all refused here, and every remove and every command at the HTTP edge goes
// through this.
func ValidKey(key string) bool {
	if ValidHash(key) {
		return true
	}
	id, ok := strings.CutPrefix(key, usenetKeyPrefix)
	if !ok || id == "" || len(id) > maxUsenetIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// UsenetKey is the download key for a usenet job id (SABnzbd's nzo_id), or "" when the id
// can't make a valid key.
func UsenetKey(id string) string {
	key := usenetKeyPrefix + strings.ToLower(strings.TrimSpace(id))
	if KeyTransport(key) != TransportUsenet {
		return ""
	}
	return key
}

// UsenetID is the job id inside a usenet key (lowercased, as stored), and false for a key
// that isn't a valid usenet key.
func UsenetID(key string) (string, bool) {
	if KeyTransport(key) != TransportUsenet {
		return "", false
	}
	return strings.TrimPrefix(key, usenetKeyPrefix), true
}

// KeyTransport is the transport a valid key belongs to, and "" for an invalid one — a
// caller can't route a value that names no single download.
func KeyTransport(key string) Transport {
	switch {
	case ValidHash(key):
		return TransportTorrent
	case ValidKey(key):
		return TransportUsenet
	}
	return ""
}

// Transport is the transport this kind of client handles, "" for a kind Arrmada doesn't
// know (such a client is sent nothing new).
func (k Kind) Transport() Transport {
	switch k {
	case KindQbittorrent:
		return TransportTorrent
	case KindSABnzbd:
		return TransportUsenet
	}
	return ""
}
