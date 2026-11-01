package migrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestLegacyBackupSchemaMigratesBeforeIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-backup.db")
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE categories(id VARCHAR PRIMARY KEY, name VARCHAR, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, is_system BOOLEAN);
		CREATE TABLE games(id VARCHAR PRIMARY KEY, name VARCHAR, cover_url VARCHAR, company VARCHAR, summary VARCHAR, path VARCHAR, save_path VARCHAR, status VARCHAR DEFAULT 'not_started', source_type VARCHAR, cached_at TIMESTAMPTZ, source_id VARCHAR, created_at TIMESTAMPTZ, use_locale_emulator BOOLEAN DEFAULT FALSE, use_magpie BOOLEAN DEFAULT FALSE, process_name VARCHAR DEFAULT '');
		CREATE TABLE game_categories(game_id VARCHAR, category_id VARCHAR, PRIMARY KEY(game_id, category_id));
		CREATE TABLE play_sessions(id VARCHAR PRIMARY KEY, game_id VARCHAR, start_time TIMESTAMPTZ, end_time TIMESTAMPTZ, duration INTEGER);
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, description VARCHAR, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
		CREATE TABLE users(id VARCHAR PRIMARY KEY, created_at TIMESTAMPTZ, default_backup_target VARCHAR);
		INSERT INTO schema_migrations (version, description) VALUES
			(131, 'legacy'),
			(134, 'legacy'),
			(140, 'legacy');
		INSERT INTO games (id, name) VALUES ('legacy-game', 'Legacy Game');
	`); err != nil {
		t.Fatalf("create legacy backup schema: %v", err)
	}

	if err := InitSchema(db); err != nil {
		t.Fatalf("initialize tables for legacy backup: %v", err)
	}
	if err := Run(context.Background(), db); err != nil {
		t.Fatalf("run migrations for legacy backup: %v", err)
	}
	if err := InitIndexes(db); err != nil {
		t.Fatalf("initialize indexes for migrated backup: %v", err)
	}

	var rating float64
	var releaseDate string
	if err := db.QueryRow(`SELECT rating, release_date FROM games WHERE id = 'legacy-game'`).Scan(&rating, &releaseDate); err != nil {
		t.Fatalf("query migrated game metadata columns: %v", err)
	}
	if rating != 0 || releaseDate != "" {
		t.Fatalf("unexpected migrated metadata defaults: rating=%v release_date=%q", rating, releaseDate)
	}

	var ratingIndexCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM duckdb_indexes()
		WHERE index_name = 'idx_games_rating'
	`).Scan(&ratingIndexCount); err != nil {
		t.Fatalf("inspect migrated rating index: %v", err)
	}
	if ratingIndexCount != 1 {
		t.Fatalf("unexpected rating index count: %d", ratingIndexCount)
	}
}

func TestMigration165BackfillsCoverSourceAndGameDirectory(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			cover_url TEXT,
			path TEXT
		)
	`); err != nil {
		t.Fatalf("create games table: %v", err)
	}

	gameRoot := filepath.Join(t.TempDir(), "game-root")
	executableDir := filepath.Join(gameRoot, "bin")
	if err := os.MkdirAll(executableDir, 0o755); err != nil {
		t.Fatalf("create executable directory: %v", err)
	}
	executablePath := filepath.Join(executableDir, "game.exe")
	if err := os.WriteFile(executablePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("create executable: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO games (id, cover_url, path)
		VALUES
			('remote-cover', 'https://example.com/cover.jpg', ?),
			('directory-path', '/local/covers/local.jpg', ?)
	`, executablePath, gameRoot); err != nil {
		t.Fatalf("insert games: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration165(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration165: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration165: %v", err)
	}

	var coverSourceURL string
	var gameDirectory string
	if err := db.QueryRow(`
		SELECT cover_source_url, game_directory
		FROM games
		WHERE id = 'remote-cover'
	`).Scan(&coverSourceURL, &gameDirectory); err != nil {
		t.Fatalf("query migrated remote-cover game: %v", err)
	}
	if coverSourceURL != "https://example.com/cover.jpg" {
		t.Fatalf("unexpected cover source URL: %q", coverSourceURL)
	}
	if gameDirectory != executableDir {
		t.Fatalf("unexpected executable parent directory: got %q want %q", gameDirectory, executableDir)
	}

	if err := db.QueryRow(`
		SELECT cover_source_url, game_directory
		FROM games
		WHERE id = 'directory-path'
	`).Scan(&coverSourceURL, &gameDirectory); err != nil {
		t.Fatalf("query migrated directory-path game: %v", err)
	}
	if coverSourceURL != "" {
		t.Fatalf("local cover should not be copied to cover source URL: %q", coverSourceURL)
	}
	if gameDirectory != gameRoot {
		t.Fatalf("existing directory path should be preserved: got %q want %q", gameDirectory, gameRoot)
	}
}

func TestMigration166AddsLocalSteamIdentity(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE games (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create games table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO games (id) VALUES ('existing')`); err != nil {
		t.Fatalf("insert existing game: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration166(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration166: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration166: %v", err)
	}

	var launchID string
	var launchKind string
	var userID string
	if err := db.QueryRow(`
		SELECT steam_launch_id, steam_launch_kind, steam_user_id
		FROM games
		WHERE id = 'existing'
	`).Scan(&launchID, &launchKind, &userID); err != nil {
		t.Fatalf("query migrated Steam identity: %v", err)
	}
	if launchID != "" || launchKind != "" || userID != "" {
		t.Fatalf("unexpected Steam identity defaults: %q %q %q", launchID, launchKind, userID)
	}
}

func TestMigration167BackfillsCompatibilityLaunchMode(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			wine_runner TEXT,
			launch_mode TEXT
		);
		INSERT INTO games VALUES
			('wine', 'system', 'normal'),
			('custom', 'custom', 'normal'),
			('steam', 'crossover', 'steam'),
			('native', '', 'normal');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration167(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration167: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration167: %v", err)
	}

	tests := []struct {
		id         string
		wantRunner string
		wantMode   string
	}{
		{id: "wine", wantRunner: "system", wantMode: "compatibility"},
		{id: "custom", wantRunner: "system", wantMode: "compatibility"},
		{id: "steam", wantRunner: "crossover", wantMode: "steam"},
		{id: "native", wantRunner: "", wantMode: "normal"},
	}
	for _, test := range tests {
		var runner string
		var mode string
		if err := db.QueryRow(`SELECT wine_runner, launch_mode FROM games WHERE id = ?`, test.id).Scan(&runner, &mode); err != nil {
			t.Fatalf("query migrated game %s: %v", test.id, err)
		}
		if runner != test.wantRunner || mode != test.wantMode {
			t.Fatalf("game %s migrated to runner=%q mode=%q, want runner=%q mode=%q", test.id, runner, mode, test.wantRunner, test.wantMode)
		}
	}
}

func TestMigration168AddsSteamLaunchOptions(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			wine_runner TEXT,
			launch_mode TEXT
		);
		INSERT INTO games (id, wine_runner, launch_mode)
		VALUES ('existing', 'system', 'normal');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration168(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration168: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration168: %v", err)
	}

	var launchOptions string
	var launchMode string
	if err := db.QueryRow(`
		SELECT steam_launch_options, launch_mode
		FROM games
		WHERE id = 'existing'
	`).Scan(&launchOptions, &launchMode); err != nil {
		t.Fatalf("query migrated Steam launch options: %v", err)
	}
	if launchOptions != "" {
		t.Fatalf("unexpected Steam launch options default: %q", launchOptions)
	}
	if launchMode != "compatibility" {
		t.Fatalf("migration168 did not repair compatibility launch mode: %q", launchMode)
	}
}

func TestMigration169AddsGameAliases(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (id TEXT PRIMARY KEY);
		INSERT INTO games (id) VALUES ('existing');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration169(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration169: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration169: %v", err)
	}

	var aliases string
	if err := db.QueryRow(`SELECT aliases FROM games WHERE id = 'existing'`).Scan(&aliases); err != nil {
		t.Fatalf("query migrated aliases: %v", err)
	}
	if aliases != "[]" {
		t.Fatalf("unexpected aliases default: %q", aliases)
	}
}

func TestMigration170BackfillsMetadataSources(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			source_type TEXT,
			source_id TEXT,
			cached_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ,
			updated_at TIMESTAMPTZ
		);
		INSERT INTO games (id, source_type, source_id, created_at, updated_at) VALUES
			('bangumi-game', 'Bangumi', '42', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('local-game', 'local', 'local-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('mixed-game', 'mixed', 'legacy', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration170(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration170: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration170: %v", err)
	}

	var sourceType string
	var sourceID string
	if err := db.QueryRow(`SELECT source_type, source_id FROM game_metadata_sources WHERE game_id = 'bangumi-game'`).Scan(&sourceType, &sourceID); err != nil {
		t.Fatalf("query backfilled metadata source: %v", err)
	}
	if sourceType != "bangumi" || sourceID != "42" {
		t.Fatalf("unexpected backfilled metadata source: %s/%s", sourceType, sourceID)
	}
	var redundantColumnCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = 'games' AND column_name = 'preferred_metadata_source'
	`).Scan(&redundantColumnCount); err != nil {
		t.Fatalf("inspect games columns: %v", err)
	}
	if redundantColumnCount != 0 {
		t.Fatalf("unexpected preferred_metadata_source column count: %d", redundantColumnCount)
	}

	var skippedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_metadata_sources WHERE game_id IN ('local-game', 'mixed-game')`).Scan(&skippedCount); err != nil {
		t.Fatalf("count skipped legacy sources: %v", err)
	}
	if skippedCount != 0 {
		t.Fatalf("expected local and mixed legacy records to be skipped, got %d", skippedCount)
	}
}

func TestMigration171CreatesGameFilterPresets(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration171(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration171: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration171: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO game_filter_presets (
			id, name, tags, exclude_tags, status, exclude_status
		)
		VALUES ('preset-1', 'test', '["tag1"]', true, 'unplayed', true)
	`); err != nil {
		t.Fatalf("insert migrated preset: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_filter_presets`).Scan(&count); err != nil {
		t.Fatalf("count migrated presets: %v", err)
	}
	if count != 1 {
		t.Fatalf("unexpected migrated preset count: %d", count)
	}
}

func TestMigration172CreatesGameReviews(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE games (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create games fixture: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration172(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration172: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration172: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO games (id) VALUES ('game-1');
		INSERT INTO game_reviews (game_id, rating, content, is_spoiler)
		VALUES ('game-1', 9, 'great', true);
	`); err != nil {
		t.Fatalf("insert game review: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO game_reviews (game_id, rating, content)
		VALUES ('game-2', 11, 'invalid')
	`); err == nil {
		t.Fatal("expected rating constraint to reject value 11")
	}
}

func TestMigration173AddsMetadataSourceToGameFilterPresets(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE game_filter_presets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		);
		INSERT INTO game_filter_presets (id, name) VALUES ('existing', 'Existing');
	`); err != nil {
		t.Fatalf("create game filter preset fixture: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration173(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration173: %v", err)
	}
	if err := migration173(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration173 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration173: %v", err)
	}

	var metadataSource string
	if err := db.QueryRow(`
		SELECT metadata_source
		FROM game_filter_presets
		WHERE id = 'existing'
	`).Scan(&metadataSource); err != nil {
		t.Fatalf("query migrated metadata source: %v", err)
	}
	if metadataSource != "" {
		t.Fatalf("unexpected metadata source default: %q", metadataSource)
	}
}

func TestMigration174AddsSortingToGameFilterPresets(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE game_filter_presets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		);
		INSERT INTO game_filter_presets (id, name) VALUES ('existing', 'Existing');
	`); err != nil {
		t.Fatalf("create game filter preset fixture: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration174(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration174: %v", err)
	}
	if err := migration174(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration174 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration174: %v", err)
	}

	var sortBy string
	var sortOrder string
	if err := db.QueryRow(`
		SELECT sort_by, sort_order
		FROM game_filter_presets
		WHERE id = 'existing'
	`).Scan(&sortBy, &sortOrder); err != nil {
		t.Fatalf("query migrated sorting preferences: %v", err)
	}
	if sortBy != "" || sortOrder != "" {
		t.Fatalf("unexpected sorting defaults: %q %q", sortBy, sortOrder)
	}
}

func TestMigration175ResetsBangumiGamesWithoutSourceIDs(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			source_type TEXT,
			source_id TEXT,
			updated_at TIMESTAMPTZ
		);
		INSERT INTO games (id, source_type, source_id, updated_at) VALUES
			('empty-id', 'bangumi', '', CURRENT_TIMESTAMP),
			('whitespace-id', ' Bangumi ', '  ', CURRENT_TIMESTAMP),
			('valid-bangumi', 'bangumi', '42', CURRENT_TIMESTAMP),
			('other-source', 'steam', '', CURRENT_TIMESTAMP);
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration175(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration175: %v", err)
	}
	if err := migration175(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration175 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration175: %v", err)
	}

	got := make(map[string]struct {
		sourceType string
		sourceID   string
	})
	rows, err := db.Query(`SELECT id, source_type, source_id FROM games ORDER BY id`)
	if err != nil {
		t.Fatalf("query migrated games: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var sourceType string
		var sourceID string
		if err := rows.Scan(&id, &sourceType, &sourceID); err != nil {
			t.Fatalf("scan migrated game: %v", err)
		}
		got[id] = struct {
			sourceType string
			sourceID   string
		}{sourceType, sourceID}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated games: %v", err)
	}

	for _, id := range []string{"empty-id", "whitespace-id"} {
		if item := got[id]; item.sourceType != "local" || item.sourceID != "" {
			t.Fatalf("expected %s to be local with an empty source ID, got %q/%q", id, item.sourceType, item.sourceID)
		}
	}
	if item := got["valid-bangumi"]; item.sourceType != "bangumi" || item.sourceID != "42" {
		t.Fatalf("unexpected valid Bangumi game: %q/%q", item.sourceType, item.sourceID)
	}
	if item := got["other-source"]; item.sourceType != "steam" || item.sourceID != "" {
		t.Fatalf("unexpected non-Bangumi game: %q/%q", item.sourceType, item.sourceID)
	}
}

func TestMigration176AddsSecondarySortingToGameFilterPresets(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE game_filter_presets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL
		);
		INSERT INTO game_filter_presets (id, name) VALUES ('existing', 'Existing');
	`); err != nil {
		t.Fatalf("create game filter preset fixture: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration176(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration176: %v", err)
	}
	if err := migration176(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration176 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration176: %v", err)
	}

	var secondarySortBy string
	var secondarySortOrder string
	if err := db.QueryRow(`
		SELECT secondary_sort_by, secondary_sort_order
		FROM game_filter_presets
		WHERE id = 'existing'
	`).Scan(&secondarySortBy, &secondarySortOrder); err != nil {
		t.Fatalf("query migrated secondary sorting preferences: %v", err)
	}
	if secondarySortBy != "" || secondarySortOrder != "" {
		t.Fatalf("unexpected secondary sorting defaults: %q %q", secondarySortBy, secondarySortOrder)
	}
}

func TestMigration179AlignsMobileGameStatuses(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (
			id TEXT PRIMARY KEY,
			status TEXT
		);
		INSERT INTO games (id, status) VALUES
			('legacy-not-started', 'not_started'),
			('legacy-want-to-play', 'want_to_play'),
			('legacy-on-hold', 'on_hold'),
			('already-aligned', 'unplayed'),
			('untouched-playing', 'playing'),
			('untouched-completed', 'completed'),
			('untouched-dropped', 'dropped'),
			('null-status', NULL);
		CREATE TABLE game_filter_presets (
			id TEXT PRIMARY KEY,
			status TEXT
		);
		INSERT INTO game_filter_presets (id, status) VALUES
			('preset-not-started', 'not_started'),
			('preset-want-to-play', 'want_to_play'),
			('preset-on-hold', 'on_hold'),
			('preset-playing', 'playing');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration179(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration179: %v", err)
	}
	if err := migration179(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration179 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration179: %v", err)
	}

	gameStatuses := map[string]string{
		"legacy-not-started":  "unplayed",
		"legacy-want-to-play": "unplayed",
		"legacy-on-hold":      "onhold",
		"already-aligned":     "unplayed",
		"untouched-playing":   "playing",
		"untouched-completed": "completed",
		"untouched-dropped":   "dropped",
	}
	for id, want := range gameStatuses {
		var status string
		if err := db.QueryRow(`SELECT status FROM games WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatalf("query migrated game %s: %v", id, err)
		}
		if status != want {
			t.Fatalf("game %s migrated to %q, want %q", id, status, want)
		}
	}

	var nullStatus sql.NullString
	if err := db.QueryRow(`SELECT status FROM games WHERE id = 'null-status'`).Scan(&nullStatus); err != nil {
		t.Fatalf("query null status game: %v", err)
	}
	if nullStatus.Valid {
		t.Fatalf("null status should stay null, got %q", nullStatus.String)
	}

	presetStatuses := map[string]string{
		"preset-not-started":  "unplayed",
		"preset-want-to-play": "unplayed",
		"preset-on-hold":      "onhold",
		"preset-playing":      "playing",
	}
	for id, want := range presetStatuses {
		var status string
		if err := db.QueryRow(`SELECT status FROM game_filter_presets WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatalf("query migrated preset %s: %v", id, err)
		}
		if status != want {
			t.Fatalf("preset %s migrated to %q, want %q", id, status, want)
		}
	}
}

func TestMigration180AddsTrailerPath(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (id TEXT PRIMARY KEY);
		INSERT INTO games (id) VALUES ('existing');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration180(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration180: %v", err)
	}
	// 幂等：列已存在时应静默成功
	if err := migration180(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration180 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration180: %v", err)
	}

	var trailerPath string
	if err := db.QueryRow(`SELECT trailer_path FROM games WHERE id = 'existing'`).Scan(&trailerPath); err != nil {
		t.Fatalf("query migrated trailer_path: %v", err)
	}
	if trailerPath != "" {
		t.Fatalf("unexpected trailer_path default: %q", trailerPath)
	}
}

func TestMigration181AddsGameArtColumns(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (id TEXT PRIMARY KEY);
		INSERT INTO games (id) VALUES ('existing');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration181(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration181: %v", err)
	}
	// 幂等：列已存在时应静默成功
	if err := migration181(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration181 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration181: %v", err)
	}

	var logoPath, bgPath string
	if err := db.QueryRow(`SELECT logo_path, bg_path FROM games WHERE id = 'existing'`).Scan(&logoPath, &bgPath); err != nil {
		t.Fatalf("query migrated art columns: %v", err)
	}
	if logoPath != "" || bgPath != "" {
		t.Fatalf("unexpected art column defaults: logo=%q bg=%q", logoPath, bgPath)
	}
}

// migration182 新增 legacy_root_uri（跨端身份键）。老库升级后该列必须存在且为空串，
// 不能把已有的 path / game_directory 搬过去 —— 那是 Windows 本机路径，不是对端的 root_uri。
func TestMigration182AddsLegacyRootUri(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE games (id TEXT PRIMARY KEY, path TEXT DEFAULT '');
		INSERT INTO games (id, path) VALUES ('existing', 'D:/Games/one/game.exe');
	`); err != nil {
		t.Fatalf("create migration fixtures: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin migration transaction: %v", err)
	}
	if err := migration182(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration182: %v", err)
	}
	// 幂等
	if err := migration182(tx); err != nil {
		tx.Rollback()
		t.Fatalf("run migration182 a second time: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration182: %v", err)
	}

	var rootURI, path string
	if err := db.QueryRow(`SELECT COALESCE(legacy_root_uri, ''), path FROM games WHERE id = 'existing'`).Scan(&rootURI, &path); err != nil {
		t.Fatalf("query migrated column: %v", err)
	}
	if rootURI != "" {
		t.Fatalf("legacy_root_uri 默认应为空串（不得用本机 path 填充），实际 %q", rootURI)
	}
	if path != `D:/Games/one/game.exe` {
		t.Fatalf("原有 path 不该被改动，实际 %q", path)
	}
}
