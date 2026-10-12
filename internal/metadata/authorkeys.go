package metadata

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Author keys: the catalogue's own id for the person a book is credited to. A library
// book carries them so it can be linked to its author row by identity — two people who
// share a name stay two people, and two spellings of one name with the same id are one.
// Every reader here is defensive: a key that is missing or in a shape this code doesn't
// know is simply "", and the book is then linked by name, as before.

var olAuthorKeyRe = regexp.MustCompile(`^OL\d+A$`)

// olAuthorKey normalises "/authors/OL23919A" to "OL23919A"; anything else is "".
func olAuthorKey(raw string) string {
	if k := authorKey(strings.TrimSpace(raw)); olAuthorKeyRe.MatchString(k) {
		return k
	}
	return ""
}

// olAuthorKeyFor is the key of the first credited author in an Open Library search doc,
// whose author_name and author_key lists run in parallel. Without a name there is no
// author to key.
func olAuthorKeyFor(names, keys []string) string {
	if len(names) == 0 || len(keys) == 0 {
		return ""
	}
	return olAuthorKey(keys[0])
}

// hcAuthorKey is a Hardcover author id as a key ("" for no id).
func hcAuthorKey(id int) string {
	if id <= 0 {
		return ""
	}
	return hcAuthorPrefix + strconv.Itoa(id)
}

// mainAuthorKey is the id of the contributor mainAuthor names — the same pick, so the
// key and the name always describe one person.
func (b hcBook) mainAuthorKey() string {
	for _, c := range b.Contributions {
		if mainAuthorRoles[strings.ToLower(strings.TrimSpace(c.Contribution))] && strings.TrimSpace(c.Author.Name) != "" {
			return hcAuthorKey(c.Author.ID)
		}
	}
	if len(b.Contributions) > 0 {
		return hcAuthorKey(b.Contributions[0].Author.ID)
	}
	return ""
}

// mainAuthorKey reads the id of the search hit's credited writer from its contributions,
// when the index carries them with ids (ids may arrive as numbers or strings). When the
// name came from the flat author_names list instead, no id goes with it.
func (d hcSearchDoc) mainAuthorKey() string {
	var cs []struct {
		Contribution string `json:"contribution"`
		Author       struct {
			ID   json.RawMessage `json:"id"`
			Name string          `json:"name"`
		} `json:"author"`
	}
	if len(d.Contributions) == 0 || json.Unmarshal(d.Contributions, &cs) != nil {
		return ""
	}
	name := d.mainAuthor()
	for _, c := range cs {
		if mainAuthorRoles[strings.ToLower(strings.TrimSpace(c.Contribution))] && strings.TrimSpace(c.Author.Name) != "" {
			if strings.TrimSpace(c.Author.Name) != name {
				return ""
			}
			return hcAuthorKey(rawInt(c.Author.ID))
		}
	}
	return ""
}
