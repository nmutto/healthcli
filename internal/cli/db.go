package cli

import (
	"database/sql"

	"github.com/nmutto/health/internal/db"
)

// openDB opens the database named by --db, or the default location when the
// flag was not given. The caller must Close the result.
func openDB(opts *options) (*sql.DB, error) {
	path := opts.dbPath
	if path == "" {
		var err error
		path, err = db.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	return db.Open(path)
}
