package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	// We use the modernc.org/sqlite driver because it is "pure Go" —
	// it does NOT require a C compiler (CGO) on the host machine.
	// This makes cross-platform builds and Docker images dramatically simpler.
	_ "modernc.org/sqlite"
)

// InitDB opens (or creates) the SQLite database file at the given path,
// verifies the connection is alive, then ensures all tables exist and are seeded.
// Returns the live database connection for the rest of the application to use.
func InitDB(dbPath string) (*sql.DB, error) {
	// sql.Open does not actually connect yet — it just validates the arguments.
	// The real connection happens on the first query or on Ping() below.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database file %q: %w", dbPath, err)
	}

	// Ping actually touches the database to confirm we can communicate with it.
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Enable Write-Ahead Logging (WAL) mode.
	// WAL allows one writer and many readers to work at the same time without blocking each other,
	// which is important for a live media player that reads frequently while also accepting updates.
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	// Enable foreign key enforcement. By default SQLite ignores foreign key constraints;
	// this pragma turns on the safety check so we cannot accidentally orphan media items.
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	// Run our schema migrations and seed data in one step.
	if err := setupDatabaseAndSeed(db); err != nil {
		return nil, fmt.Errorf("setting up database: %w", err)
	}

	return db, nil
}

// setupDatabaseAndSeed creates all tables if they do not already exist,
// guarantees the sync_state singleton row is present, and populates
// the windows/media_items tables with rich sample data on first launch.
func setupDatabaseAndSeed(db *sql.DB) error {
	// -----------------------------------------------------------------------
	// SCHEMA
	// -----------------------------------------------------------------------
	//
	// Three tables power this application:
	//
	//  1. windows         — each row is one display screen.
	//  2. media_items     — each row is one playlist entry attached to a window.
	//  3. sync_state      — SINGLETON TABLE (exactly one row, id always = 1).
	//
	// Why is sync_state a singleton?
	// --------------------------------
	// There can only ever be ONE active global synchronisation event at a time.
	// If it were a normal list, code would have to query "which row is active?",
	// creating a window of time where two rows could both look active — a classic
	// race condition. By fixing id=1 and doing an UPDATE in place, the database
	// itself enforces the "only one" rule: there is literally nowhere else to write.
	// Any code that wants to start a sync does: UPDATE sync_state SET ... WHERE id=1.
	// Any code that wants to read sync does:    SELECT ... FROM sync_state WHERE id=1.
	// Simple, atomic, and impossible to get wrong.
	schema := `
	CREATE TABLE IF NOT EXISTS windows (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		name             TEXT    NOT NULL,
		cycle_start_time INTEGER NOT NULL  -- Unix timestamp (seconds). When this window's loop last reset to item 0.
	);

	CREATE TABLE IF NOT EXISTS media_items (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		window_id        INTEGER NOT NULL,
		type             TEXT    NOT NULL CHECK(type IN ('image','video','blank')),
		url              TEXT    NOT NULL,
		duration_seconds INTEGER NOT NULL,
		order_index      INTEGER NOT NULL, -- Playlist position; 0 = plays first. Items run in ascending order.
		FOREIGN KEY(window_id) REFERENCES windows(id) ON DELETE CASCADE
	);

	-- Singleton table: id is always 1. See the design note above for why.
	CREATE TABLE IF NOT EXISTS sync_state (
		id               INTEGER PRIMARY KEY CHECK(id = 1), -- Hard constraint: this table MUST have exactly one row.
		active           INTEGER NOT NULL DEFAULT 0,        -- 0 = inactive (normal loop), 1 = global takeover active
		media_url        TEXT    NOT NULL DEFAULT '',
		media_type       TEXT    NOT NULL DEFAULT '',
		started_at       INTEGER NOT NULL DEFAULT 0,        -- Unix timestamp when the takeover began
		duration_seconds INTEGER NOT NULL DEFAULT 0
	);
	`

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("running schema migrations: %w", err)
	}

	// -----------------------------------------------------------------------
	// SYNC_STATE SINGLETON GUARANTEE
	// -----------------------------------------------------------------------
	// On every start-up we make sure the singleton row exists.
	// "INSERT OR IGNORE" means: insert id=1 only if it isn't already there.
	// If the row already exists (subsequent boots), this is a harmless no-op.
	_, err := db.Exec(`
		INSERT OR IGNORE INTO sync_state (id, active, media_url, media_type, started_at, duration_seconds)
		VALUES (1, 0, '', '', 0, 0);
	`)
	if err != nil {
		return fmt.Errorf("initialising sync_state singleton: %w", err)
	}

	// -----------------------------------------------------------------------
	// SEED CHECK
	// -----------------------------------------------------------------------
	// We only seed if the windows table is completely empty.
	// This ensures we never duplicate data on a server restart.
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
	type seedItem struct{ typ, url string; dur int }

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
