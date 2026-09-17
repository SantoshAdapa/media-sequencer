package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// InitDB opens or creates the SQLite database, verifies the connection,
// configures pragmas, and ensures all tables exist and are seeded.
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database file %q: %w", dbPath, err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Enable Write-Ahead Logging (WAL) for concurrent read/write.
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	// Enable foreign key enforcement.
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	if err := setupDatabaseAndSeed(db); err != nil {
		return nil, fmt.Errorf("setting up database: %w", err)
	}

	return db, nil
}

func setupDatabaseAndSeed(db *sql.DB) error {
	// The sync_state table is a singleton (id = 1). This enforces that there
	// can only ever be ONE active global synchronisation event at a time and
	// structurally prevents race conditions.
	//
	schema := `
	CREATE TABLE IF NOT EXISTS windows (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		name             TEXT    NOT NULL,
		cycle_start_time INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS media_items (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		window_id        INTEGER NOT NULL,
		type             TEXT    NOT NULL CHECK(type IN ('image','video','blank')),
		url              TEXT    NOT NULL,
		duration_seconds INTEGER NOT NULL,
		order_index      INTEGER NOT NULL,
		FOREIGN KEY(window_id) REFERENCES windows(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS sync_state (
		id               INTEGER PRIMARY KEY CHECK(id = 1),
		active           INTEGER NOT NULL DEFAULT 0,
		media_url        TEXT    NOT NULL DEFAULT '',
		media_type       TEXT    NOT NULL DEFAULT '',
		started_at       INTEGER NOT NULL DEFAULT 0,
		duration_seconds INTEGER NOT NULL DEFAULT 0
	);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("running schema migrations: %w", err)
	}

	// Guarantee the singleton row exists.
	_, err := db.Exec(`
		INSERT OR IGNORE INTO sync_state (id, active, media_url, media_type, started_at, duration_seconds)
		VALUES (1, 0, '', '', 0, 0);
	`)
	if err != nil {
		return fmt.Errorf("initialising sync_state singleton: %w", err)
	}

	var windowCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM windows").Scan(&windowCount); err != nil {
		return fmt.Errorf("checking window count: %w", err)
	}
	if windowCount > 0 {
		// Data already exists — nothing to seed.
		log.Println("Database already contains data, skipping seed.")
		return nil
	}

	// -----------------------------------------------------------------------
	// SEED DATA
	// -----------------------------------------------------------------------
	log.Println("Database is empty — inserting seed data...")

	// All windows share the same cycle_start_time of "right now".
	// This means every window's loop is considered to have begun at the moment
	// the server first started, making the front-end time calculations consistent.
	now := time.Now().Unix()

	// Three freely-licensed sample video URLs we rotate across windows.
	// These are well-known public test files that work without authentication.
	const (
		videoURL1 = "https://www.w3schools.com/html/mov_bbb.mp4"
		videoURL2 = "https://samplelib.com/lib/preview/mp4/sample-5s.mp4"
		videoURL3 = "https://www.learningcontainer.com/wp-content/uploads/2020/05/sample-mp4-file.mp4"
	)

	// seedWindows holds the name for each of the 5 windows we create.
	seedWindows := []string{
		"Window 1", "Window 2", "Window 3", "Window 4", "Window 5",
	}

	// seedItems defines the playlist for each window.
	// Each inner slice is one window's playlist, in order.
	// Fields: type, url, duration_seconds
	type seedItem struct {
		typ, url string
		dur      int
	}

	seedPlaylists := [][]seedItem{
		// Window 1 — mix of two images, two videos, one blank
		{
			{"image", "https://picsum.photos/seed/w1item0/800/600", 10},
			{"video", videoURL1, 30},
			{"image", "https://picsum.photos/seed/w1item2/800/600", 12},
			{"blank", "", 5},
			{"video", videoURL2, 20},
			{"image", "https://picsum.photos/seed/w1item5/800/600", 8},
		},
		// Window 2
		{
			{"video", videoURL2, 25},
			{"image", "https://picsum.photos/seed/w2item1/800/600", 15},
			{"blank", "", 5},
			{"image", "https://picsum.photos/seed/w2item3/800/600", 10},
			{"video", videoURL3, 40},
		},
		// Window 3
		{
			{"image", "https://picsum.photos/seed/w3item0/800/600", 12},
			{"image", "https://picsum.photos/seed/w3item1/800/600", 8},
			{"video", videoURL1, 30},
			{"blank", "", 5},
			{"video", videoURL3, 45},
			{"image", "https://picsum.photos/seed/w3item5/800/600", 10},
		},
		// Window 4
		{
			{"blank", "", 5},
			{"video", videoURL3, 35},
			{"image", "https://picsum.photos/seed/w4item2/800/600", 14},
			{"video", videoURL1, 22},
			{"image", "https://picsum.photos/seed/w4item4/800/600", 9},
		},
		// Window 5
		{
			{"image", "https://picsum.photos/seed/w5item0/800/600", 11},
			{"video", videoURL2, 28},
			{"blank", "", 5},
			{"image", "https://picsum.photos/seed/w5item3/800/600", 13},
			{"video", videoURL1, 38},
			{"image", "https://picsum.photos/seed/w5item5/800/600", 8},
		},
	}

	// We perform all seed inserts inside a single database transaction.
	// A transaction is like a "save point": either every insert succeeds and is committed,
	// or if anything fails mid-way, the entire batch is rolled back so we never end up
	// with a partially-seeded, broken database state.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("beginning seed transaction: %w", err)
	}
	// If anything panics or we return an error before committing, roll back automatically.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	for i, name := range seedWindows {
		// Insert the window and retrieve the auto-generated ID the database assigned to it.
		result, err := tx.Exec(
			"INSERT INTO windows (name, cycle_start_time) VALUES (?, ?)",
			name, now,
		)
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("inserting window %q: %w", name, err)
		}

		windowID, err := result.LastInsertId()
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("reading new window id for %q: %w", name, err)
		}

		// Insert each media item for this window using its playlist position as order_index.
		for orderIndex, item := range seedPlaylists[i] {
			_, err := tx.Exec(
				`INSERT INTO media_items (window_id, type, url, duration_seconds, order_index)
				 VALUES (?, ?, ?, ?, ?)`,
				windowID, item.typ, item.url, item.dur, orderIndex,
			)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("inserting media item %d for window %q: %w", orderIndex, name, err)
			}
		}

		log.Printf("  Seeded %q (id=%d) with %d media items.", name, windowID, len(seedPlaylists[i]))
	}

	// Everything went well — commit the transaction to make all inserts permanent.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing seed transaction: %w", err)
	}

	log.Println("Seed complete.")
	return nil
}
