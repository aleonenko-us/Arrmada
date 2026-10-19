package store

// Hooks for the external store_test package, whose tests need both this package's
// migration helpers and packages that themselves import store (internal/music).

var (
	MigrationsBefore = migrationsBefore
	RunMigrations    = runMigrations
	TestDB           = testDB
	FillOldMusic     = fillOldMusic
	FillBigOldMusic  = fillBigOldMusic
)

// MusicModelMigration is MUS-28's migration file.
const MusicModelMigration = musicModelMigration
