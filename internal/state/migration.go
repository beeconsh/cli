package state

// CurrentVersion is the latest state schema version.
const CurrentVersion = 2

// Migration describes a state schema migration.
type Migration struct {
	Version int
	Migrate func(st *State) error
}

// migrations is the ordered list of state schema migrations.
var migrations = []Migration{
	{Version: 2, Migrate: migrateV1toV2},
}

// runMigrations applies any pending migrations to bring state to CurrentVersion.
func runMigrations(st *State) error {
	for _, m := range migrations {
		if st.Version < m.Version {
			if err := m.Migrate(st); err != nil {
				return err
			}
			st.Version = m.Version
		}
	}
	return nil
}

// migrateV1toV2 adds DriftFirstDetected and DriftCount fields to ResourceRecord.
// These fields are zero-valued by default (nil / 0), so no data transformation needed —
// the migration just bumps the version. The fields are populated by Drift() going forward.
func migrateV1toV2(st *State) error {
	// No data transformation needed; the new fields are added to the struct
	// and will be populated on next drift detection run.
	return nil
}
