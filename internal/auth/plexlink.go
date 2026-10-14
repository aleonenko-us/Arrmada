package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrPlexAlreadyLinked: the Plex account is linked to another Arrmada account. UserID and
// Username say which, for the caller to show (only an admin is told the id).
type ErrPlexAlreadyLinked struct {
	UserID   int64
	Username string
	Role     Role
}

func (e *ErrPlexAlreadyLinked) Error() string {
	return fmt.Sprintf("this Plex account is already linked to %s", e.Username)
}

// ErrPlexOnlyLogin: the account has no password anyone knows and no Jellyfin link, so Plex
// is its only way in and unlinking it would lock its owner out.
var ErrPlexOnlyLogin = errors.New("this account signs in only with Plex — set a password for it before unlinking Plex")

// PlexLink is an account's Plex link, as its owner sees it.
type PlexLink struct {
	Linked       bool   `json:"linked"`
	PlexUsername string `json:"plex_username,omitempty"`
	// CanUnlink: the account has a password (or a Jellyfin link), so unlinking Plex leaves
	// a way in.
	CanUnlink bool `json:"can_unlink"`
}

// PlexLinkFor reports userID's Plex link.
func (s *Service) PlexLinkFor(ctx context.Context, userID int64) (PlexLink, error) {
	l, err := s.linkFor(ctx, plexLink, userID)
	return PlexLink{Linked: l.linked, PlexUsername: l.name, CanUnlink: l.canUnlink}, err
}

// LinkPlex ties a Plex account to an existing account, replacing any Plex account it was
// linked to before. A Plex account linked to someone else is refused with
// *ErrPlexAlreadyLinked (the unique index settles a race the same way): one Plex account
// is one Arrmada account. Linking grants nothing by itself — whether Plex sign-in may
// enter a staff account is the sign-in's decision.
func (s *Service) LinkPlex(ctx context.Context, userID int64, plexID, plexUsername string) error {
	h, err := s.link(ctx, plexLink, userID, plexID, plexUsername)
	if h != nil {
		return &ErrPlexAlreadyLinked{UserID: h.userID, Username: h.username, Role: h.role}
	}
	return err
}

// UnlinkPlex removes an account's Plex link. Its next Plex sign-in is then a stranger's:
// it gets (or makes) the account linked to that Plex id, never this one. checkLogin
// refuses when Plex is the account's only way in (ErrPlexOnlyLogin); an admin removing the
// link from Settings → People & access passes false.
func (s *Service) UnlinkPlex(ctx context.Context, userID int64, checkLogin bool) error {
	return s.unlink(ctx, plexLink, userID, checkLogin, ErrPlexOnlyLogin)
}

// externalLink is one kind of link on one account.
type externalLink struct {
	linked, canUnlink bool
	name              string
}

// linkHolder is the account an outside id is linked to.
type linkHolder struct {
	userID   int64
	username string
	role     Role
}

// other is the kind of link that isn't k: a second way in.
func (k linkKind) other() linkKind {
	if k.idCol == plexLink.idCol {
		return jellyfinLink
	}
	return plexLink
}

func (s *Service) linkFor(ctx context.Context, k linkKind, userID int64) (externalLink, error) {
	var id, otherID sql.NullString
	var l externalLink
	var pw int
	err := s.db.QueryRowContext(ctx, `SELECT `+k.idCol+`, `+k.nameCol+`, `+k.other().idCol+`, password_login FROM users WHERE id = ?`, userID).
		Scan(&id, &l.name, &otherID, &pw)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, err
	}
	l.linked = strings.TrimSpace(id.String) != ""
	l.canUnlink = l.linked && (pw == 1 || strings.TrimSpace(otherID.String) != "")
	if !l.linked {
		l.name = ""
	}
	return l, nil
}

// link sets an account's link of kind k. A non-nil holder means the id belongs to another
// account and nothing changed.
func (s *Service) link(ctx context.Context, k linkKind, userID int64, extID, extName string) (*linkHolder, error) {
	extID = strings.TrimSpace(extID)
	if extID == "" {
		return nil, errors.New("missing " + strings.ToLower(k.server) + " id")
	}
	if h, err := s.holder(ctx, k, extID); err != nil {
		return nil, err
	} else if h != nil && h.userID != userID {
		return h, nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET `+k.idCol+` = ?, `+k.nameCol+` = ? WHERE id = ?`, extID, strings.TrimSpace(extName), userID)
	if isUniqueViolation(err) {
		if h, herr := s.holder(ctx, k, extID); herr == nil && h != nil {
			return h, nil
		}
		return &linkHolder{username: "another account"}, nil
	}
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return nil, nil
}

// holder is the account an outside id of kind k is linked to, or nil.
func (s *Service) holder(ctx context.Context, k linkKind, extID string) (*linkHolder, error) {
	var h linkHolder
	err := s.db.QueryRowContext(ctx, `SELECT id, username, role FROM users WHERE `+k.idCol+` = ?`, extID).Scan(&h.userID, &h.username, &h.role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// unlink clears an account's link of kind k. With checkLogin it refuses (onlyErr) when
// that link is the account's last way in: no password anyone knows and no other link.
func (s *Service) unlink(ctx context.Context, k linkKind, userID int64, checkLogin bool, onlyErr error) error {
	q := `UPDATE users SET ` + k.idCol + ` = NULL, ` + k.nameCol + ` = '' WHERE id = ?`
	if checkLogin {
		q += ` AND (password_login = 1 OR COALESCE(` + k.idCol + `, '') = '' OR COALESCE(` + k.other().idCol + `, '') != '')`
	}
	res, err := s.db.ExecContext(ctx, q, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.UserByID(ctx, userID); err != nil {
			return ErrNotFound
		}
		return onlyErr
	}
	return nil
}
