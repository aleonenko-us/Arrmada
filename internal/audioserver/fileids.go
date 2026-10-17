package audioserver

import (
	"context"

	"github.com/tristenlammi/arrmada/internal/library"
)

// editionPath is an audiobook edition's recorded path, and whether it's a folder of files.
type editionPath struct {
	path   string
	folder bool
}

// BackfillFileIDs stores the id of every audiobook file the database knows by path but has
// no id for yet — rows probed before ids were stored — with the value its name gives it
// today, the one apps already hold. It reads only the database (the books' recorded paths
// and audio_file_meta), never the disk, so it's safe at start-up with a share offline;
// afterwards a rename carries each file's id with it, even one never listed since. Runs
// once at start-up; returns how many ids it stored.
func (s *Server) BackfillFileIDs(ctx context.Context) (int, error) {
	all, err := s.books.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range all {
		var editions []editionPath
		if b.Audiobook != nil && b.Audiobook.Path != "" {
			editions = append(editions, editionPath{b.Audiobook.Path, b.Audiobook.FileCount > 1})
		}
		for _, v := range b.AudioVersions {
			if v.File != nil && v.File.Path != "" {
				editions = append(editions, editionPath{v.File.Path, v.File.FileCount > 1})
			}
		}
		for _, e := range editions {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			clean, from, to := underRange(e.path)
			q := `SELECT path FROM audio_file_meta WHERE ino = '' AND path = ?`
			args := []any{clean}
			if e.folder {
				q = `SELECT path FROM audio_file_meta WHERE ino = '' AND (path = ? OR (path >= ? AND path < ?))`
				args = append(args, from, to)
			}
			rows, err := s.db.QueryContext(ctx, q, args...)
			if err != nil {
				return n, err
			}
			var paths []string
			for rows.Next() {
				var p string
				if err := rows.Scan(&p); err != nil {
					rows.Close()
					return n, err
				}
				paths = append(paths, p)
			}
			rows.Close()
			base := library.ItemBase(clean, e.folder)
			for _, p := range paths {
				res, err := s.db.ExecContext(ctx, `UPDATE audio_file_meta SET ino = ? WHERE path = ? AND ino = ''`,
					inoFor(relTo(base, p)), p)
				if err != nil {
					return n, err
				}
				if c, _ := res.RowsAffected(); c > 0 {
					n++
				}
			}
		}
	}
	return n, nil
}
