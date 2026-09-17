// Package handlers contains all the HTTP request handlers for the Media Sequencer API.
// Each handler is responsible for one specific URL: it reads the request, talks to the
// database if needed, runs any calculations, and writes back a JSON response.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/SantoshAdapa/media-sequencer/backend/internal/models"
	// Our sync package name clashes with the standard library "sync" package,
	// so we import it under the alias "playback" to keep things readable.
	playback "github.com/SantoshAdapa/media-sequencer/backend/internal/sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// RESPONSE TYPES
// These structs define the exact shape of JSON we send back to callers.
// Keeping them separate from the database models lets us add computed fields
// (like currentItem) without polluting the core data layer.
// ─────────────────────────────────────────────────────────────────────────────

// windowResponse is what we send for each window in GET /windows.
// It contains the window's basic info, its full ordered playlist, and
// a server-computed "currentItem" field so the frontend can verify its own clock math.
type windowResponse struct {
	ID             int                `json:"id"`
	Name           string             `json:"name"`
	CycleStartTime int64              `json:"cycleStartTime"`
	MediaItems     []models.MediaItem `json:"mediaItems"`
	// CurrentItem will be null in JSON if the window's playlist is empty.
	CurrentItem *models.MediaItem `json:"currentItem"`
}

// syncStatusResponse is what we send for GET /sync/status.
// StartedAt is the Unix timestamp when the sync began — the frontend uses it
// to calculate the correct video playback offset (nowSeconds - startedAt).
// RemainingSeconds is zero when the sync is not active.
type syncStatusResponse struct {
	Active           bool   `json:"active"`
	MediaURL         string `json:"mediaUrl"`
	MediaType        string `json:"mediaType"`
	StartedAt        int64  `json:"startedAt"`
	RemainingSeconds int64  `json:"remainingSeconds"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ROUTER SETUP
// ─────────────────────────────────────────────────────────────────────────────

// SetupRoutes registers every URL path our server knows how to handle and attaches
// the correct handler function to each one. It also installs "middleware" — code that
// runs automatically for EVERY request before the handler does (e.g. CORS headers).
func SetupRoutes(r chi.Router, db *sql.DB) {
	// ── CORS Middleware ──

	allowedOrigins := []string{
		"http://localhost:5173",
		"https://frontend-eta-seven-28.vercel.app", // Added production origin out of the box
	}
	if envOrigin := os.Getenv("FRONTEND_ORIGIN"); envOrigin != "" {
		allowedOrigins = append(allowedOrigins, envOrigin)
	}

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // How long browsers may cache the CORS preflight response, in seconds
	}))

	// ── Routes ──────────────────────────────────────────────────────────────
	r.Get("/health", healthCheckHandler)

	// All window and sync handlers are "closures" that carry the database connection
	// inside them. This is idiomatic Go: instead of a global variable, we pass the
	// dependency in and capture it in the function.
	r.Get("/windows", getWindowsHandler(db))
	r.Post("/windows/{id}/media", addMediaHandler(db))
	r.Delete("/windows/{id}/media/{itemId}", deleteMediaHandler(db))
	r.Post("/sync", triggerSyncHandler(db))
	r.Get("/sync/status", getSyncStatusHandler(db))
}

// ─────────────────────────────────────────────────────────────────────────────
// SHARED HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ─────────────────────────────────────────────────────────────────────────────
// HANDLERS
// ─────────────────────────────────────────────────────────────────────────────

// healthCheckHandler is the simplest possible endpoint — it just confirms the server is alive.
// External monitoring systems and load balancers call this regularly; if it stops
// responding they know to restart the service.
func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── GET /windows ─────────────────────────────────────────────────────────────

// getWindowsHandler returns every display window, including its complete ordered
// playlist and a server-computed "currentItem" that shows exactly which media piece
// should be playing on that window at this exact moment.
//
// The "currentItem" is calculated using the same pure math the frontend uses.
// Returning it here gives the frontend a ground-truth reference point to verify
// that its own local clock computation matches the server's.
func getWindowsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fetch every window, sorted by ID for a consistent, predictable response order.
		rows, err := db.QueryContext(r.Context(),
			"SELECT id, name, cycle_start_time FROM windows ORDER BY id")
		if err != nil {
			log.Printf("failed to query windows: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to query windows")
			return
		}
		defer rows.Close()

		var windowsData []models.Window
		for rows.Next() {
			var win models.Window
			if err := rows.Scan(&win.ID, &win.Name, &win.CycleStartTime); err != nil {
				log.Printf("failed to read window row: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to read window row")
				return
			}
			windowsData = append(windowsData, win)
		}
		// Close rows immediately so we free up the DB connection before making another query.
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Printf("error iterating windows: %v", err)
			writeError(w, http.StatusInternalServerError, "error iterating windows")
			return
		}

		// Optimization: Avoid the N+1 query problem.
		// Instead of looping through windows and firing a new query for each one's media items,
		// we fetch all windows and all their media items in two queries total. We then group the
		// items in Go memory, ensuring response time doesn't grow linearly with the number of windows.
		itemRows, err := db.QueryContext(r.Context(), `
			SELECT id, window_id, type, url, duration_seconds, order_index
			FROM   media_items
			ORDER  BY window_id ASC, order_index ASC`)
		if err != nil {
			log.Printf("failed to query media items: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to query media items")
			return
		}
		defer itemRows.Close()

		// Group media items by window ID.
		itemsMap := make(map[int][]models.MediaItem)
		for itemRows.Next() {
			var item models.MediaItem
			if err := itemRows.Scan(
				&item.ID, &item.WindowID, &item.Type,
				&item.URL, &item.DurationSeconds, &item.OrderIndex,
			); err != nil {
				log.Printf("failed to read media item row: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to read media item row")
				return
			}
			itemsMap[item.WindowID] = append(itemsMap[item.WindowID], item)
		}
		itemRows.Close()
		if err := itemRows.Err(); err != nil {
			log.Printf("error iterating media items: %v", err)
			writeError(w, http.StatusInternalServerError, "error iterating media items")
			return
		}

		// Capture the current Unix timestamp once so every window's calculation uses
		// the same "now" value — important for consistency in a single response.
		now := time.Now().Unix()
		responses := []windowResponse{} // empty slice, never nil

		// Assemble the final response.
		for _, win := range windowsData {
			items := itemsMap[win.ID]
			if items == nil {
				items = []models.MediaItem{} // ensure JSON encodes as [] not null
			}

			// ── Compute current item ──────────────────────────────────────────
			// Step 1: How far into the 5-hour super-cycle are we right now?
			elapsed := playback.ComputeElapsedInCycle(win.CycleStartTime, now)
			// Step 2: Which playlist item does that elapsed time land on?
			currentItem, computeErr := playback.ComputeCurrentItem(items, elapsed)

			resp := windowResponse{
				ID:             win.ID,
				Name:           win.Name,
				CycleStartTime: win.CycleStartTime,
				MediaItems:     items,
				CurrentItem:    nil, // stays nil (JSON null) if playlist is empty
			}
			if computeErr == nil {
				// Only set CurrentItem when we successfully found one.
				resp.CurrentItem = &currentItem
			}

			responses = append(responses, resp)
		}

		writeJSON(w, http.StatusOK, responses)
	}
}

// ── POST /windows/{id}/media ──────────────────────────────────────────────────

// addMediaHandler appends a new piece of media to the end of a specific window's playlist.
// The caller tells us what kind of media it is (image, video, or blank), where to find it,
// and how long it should stay on screen. We automatically place it after all existing items.
//
// Returns 400 if the request body is invalid, 404 if the window doesn't exist, 500 on DB errors.
func addMediaHandler(db *sql.DB) http.HandlerFunc {
	validTypes := map[string]bool{"image": true, "video": true, "blank": true}

	return func(w http.ResponseWriter, r *http.Request) {
		windowID, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "window id in URL must be an integer")
			return
		}

		var body struct {
			Type            string `json:"type"`
			URL             string `json:"url"`
			DurationSeconds int    `json:"duration_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}

		if !validTypes[body.Type] {
			writeError(w, http.StatusBadRequest, `"type" must be one of: "image", "video", "blank"`)
			return
		}
		if body.DurationSeconds <= 0 {
			writeError(w, http.StatusBadRequest, `"duration_seconds" must be a positive integer`)
			return
		}
		// image/video items require a URL; blank items may omit it.
		if (body.Type == "image" || body.Type == "video") && body.URL == "" {
			writeError(w, http.StatusBadRequest, `"url" is required for image and video items`)
			return
		}

		// Wrapping the existence check, the max-index lookup, and the insert in a
		// transaction ensures that two concurrent requests cannot read the same
		// MAX(order_index) and insert conflicting duplicates.
		conn, err := db.Conn(r.Context())
		if err != nil {
			log.Printf("failed to get db connection: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to begin transaction")
			return
		}
		defer conn.Close()

		// This tells SQLite to lock the database for writing as soon as the transaction starts,
		// rather than waiting until the first write — this is what actually prevents two
		// simultaneous 'Add Item' requests from reading the same playlist position and creating a conflict.
		_, err = conn.ExecContext(r.Context(), "BEGIN IMMEDIATE")
		if err != nil {
			log.Printf("failed to begin immediate transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to begin transaction")
			return
		}
		defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

		// We check before doing anything else so we can return a clear 404 rather
		// than a confusing foreign-key constraint error from the database.
		var exists bool
		err = conn.QueryRowContext(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM windows WHERE id = ?)", windowID).Scan(&exists)
		if err != nil {
			log.Printf("failed to check if window exists: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to check if window exists")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "window not found")
			return
		}

		// We find the current highest position in the playlist and add 1.
		// COALESCE(-1, …) handles the case where the playlist is empty: the first
		// item will get index 0 (−1 + 1 = 0).
		var maxIndex int
		err = conn.QueryRowContext(r.Context(),
			"SELECT COALESCE(MAX(order_index), -1) FROM media_items WHERE window_id = ?",
			windowID).Scan(&maxIndex)
		if err != nil {
			log.Printf("failed to determine playlist position: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to determine playlist position")
			return
		}
		nextIndex := maxIndex + 1

		result, err := conn.ExecContext(r.Context(), `
			INSERT INTO media_items (window_id, type, url, duration_seconds, order_index)
			VALUES (?, ?, ?, ?, ?)`,
			windowID, body.Type, body.URL, body.DurationSeconds, nextIndex)
		if err != nil {
			log.Printf("failed to save media item: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to save media item")
			return
		}

		newID, err := result.LastInsertId()
		if err != nil {
			log.Printf("failed to read new item ID: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to read new item ID")
			return
		}

		if _, err := conn.ExecContext(r.Context(), "COMMIT"); err != nil {
			log.Printf("failed to commit transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		// Return the complete, newly-created MediaItem so the caller has the full record
		// (including the database-assigned ID and the calculated order_index).
		created := models.MediaItem{
			ID:              int(newID),
			WindowID:        windowID,
			Type:            body.Type,
			URL:             body.URL,
			DurationSeconds: body.DurationSeconds,
			OrderIndex:      nextIndex,
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// ── DELETE /windows/{id}/media/{itemId} ───────────────────────────────────────

// deleteMediaHandler removes a single media item from a window's playlist and
// re-normalises the order_index of all remaining items to prevent gaps.
func deleteMediaHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		windowID, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "window id in URL must be an integer")
			return
		}

		itemID, err := strconv.Atoi(chi.URLParam(r, "itemId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "item id in URL must be an integer")
			return
		}

		// Ensure the item exists and belongs to this window
		var exists bool
		err = db.QueryRowContext(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM media_items WHERE id = ? AND window_id = ?)",
			itemID, windowID).Scan(&exists)
		if err != nil {
			log.Printf("failed to check item existence: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to check item existence")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "media item not found in this window")
			return
		}

		// Run deletion and re-sequencing in a transaction so we never leave gaps
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Printf("failed to begin transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to begin transaction")
			return
		}
		defer func() { _ = tx.Rollback() }()

		// Delete the item
		_, err = tx.ExecContext(r.Context(),
			"DELETE FROM media_items WHERE id = ? AND window_id = ?", itemID, windowID)
		if err != nil {
			log.Printf("failed to delete media item: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to delete media item")
			return
		}

		// Re-normalise remaining items to eliminate any index gaps
		rows, err := tx.QueryContext(r.Context(),
			"SELECT id FROM media_items WHERE window_id = ? ORDER BY order_index ASC", windowID)
		if err != nil {
			log.Printf("failed to query remaining items: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to query remaining items")
			return
		}

		var remainingIDs []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				log.Printf("failed to read remaining items: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to read remaining items")
				return
			}
			remainingIDs = append(remainingIDs, id)
		}
		rows.Close()

		// Update order_index sequentially without gaps
		for newIndex, id := range remainingIDs {
			_, err = tx.ExecContext(r.Context(),
				"UPDATE media_items SET order_index = ? WHERE id = ?", newIndex, id)
			if err != nil {
				log.Printf("failed to update order_index: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to update order_index")
				return
			}
		}

		if err := tx.Commit(); err != nil {
			log.Printf("failed to commit transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to commit transaction")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// ── POST /sync ────────────────────────────────────────────────────────────────

// triggerSyncHandler starts a global synchronisation event.
// When triggered, every display window stops its individual playlist and instead
// shows the same specified media item simultaneously. The event lasts for a fixed
// number of seconds, after which windows automatically return to their own playlists.
//
// This overwrites the singleton sync_state row in the database (id always = 1).
func triggerSyncHandler(db *sql.DB) http.HandlerFunc {
	// Blank screens don't make sense as a "take-over" media type, so we only allow image/video.
	validTypes := map[string]bool{"image": true, "video": true}

	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MediaURL        string `json:"media_url"`
			MediaType       string `json:"media_type"`
			DurationSeconds int    `json:"duration_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}

		if !validTypes[body.MediaType] {
			writeError(w, http.StatusBadRequest, `"media_type" must be "image" or "video"`)
			return
		}
		if body.MediaURL == "" {
			writeError(w, http.StatusBadRequest, `"media_url" is required`)
			return
		}
		if body.DurationSeconds <= 0 {
			writeError(w, http.StatusBadRequest, `"duration_seconds" must be a positive integer`)
			return
		}

		// Capture "now" so the start time is consistent between the DB write and the response.
		startedAt := time.Now().Unix()

		// Because id is always 1, this UPDATE modifies exactly one specific row.
		// There is no ambiguity about which row to update, and no risk of accidentally
		// creating a second active sync row — the database schema makes that impossible.
		_, err := db.ExecContext(r.Context(), `
			UPDATE sync_state
			SET    active = 1,
			       media_url = ?,
			       media_type = ?,
			       started_at = ?,
			       duration_seconds = ?
			WHERE  id = 1`,
			body.MediaURL, body.MediaType, startedAt, body.DurationSeconds)
		if err != nil {
			log.Printf("failed to activate sync: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to activate sync")
			return
		}

		// Return the resulting sync state so the caller can confirm exactly what was set.
		state := models.SyncState{
			Active:          true,
			MediaURL:        body.MediaURL,
			MediaType:       body.MediaType,
			StartedAt:       startedAt,
			DurationSeconds: body.DurationSeconds,
		}
		writeJSON(w, http.StatusOK, state)
	}
}

// ── GET /sync/status ──────────────────────────────────────────────────────────

// getSyncStatusHandler reports whether a global sync event is active right now
// and how many seconds remain before normal window playback resumes.
//
// This handler has an important side effect: if the database still shows the sync
// as "active" but our math determines the duration has elapsed, we immediately flip
// the database row to inactive. This way the DB stays consistent without needing a
// separate background job — the first request after expiry cleans things up.
func getSyncStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ── Read the singleton sync_state row ─────────────────────────────────
		// SQLite stores booleans as integers (0 = false, 1 = true), so we scan
		// "active" into an int and convert it ourselves.
		var state models.SyncState
		var activeInt int
		err := db.QueryRowContext(r.Context(), `
			SELECT active, media_url, media_type, started_at, duration_seconds
			FROM   sync_state
			WHERE  id = 1`).
			Scan(&activeInt, &state.MediaURL, &state.MediaType,
				&state.StartedAt, &state.DurationSeconds)

		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// The singleton row is always created by InitDB, so this path means
				// something went very wrong — but we handle it gracefully rather than crashing.
				writeJSON(w, http.StatusOK, syncStatusResponse{Active: false})
				return
			}
			log.Printf("failed to read sync state: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to read sync state")
			return
		}
		state.Active = activeInt == 1

		// ── Ask the pure math function if the sync is still valid ─────────────
		now := time.Now().Unix()
		isActive, remaining := playback.ComputeSyncStatus(state, now)

		// ── Side effect: expire the DB row if needed ──────────────────────────
		// If the DB claims sync is active but the math says time has run out,
		// we update the DB right here. We ignore the error on this cleanup write
		// (log-worthy in production, but not a reason to return a 500 to the caller).
		if state.Active && !isActive {
			_, err := db.ExecContext(r.Context(),
				"UPDATE sync_state SET active = 0 WHERE id = 1")
			if err != nil {
				log.Printf("failed to mark expired sync as inactive: %v", err)
			}
		}

		writeJSON(w, http.StatusOK, syncStatusResponse{
			Active:           isActive,
			MediaURL:         state.MediaURL,
			MediaType:        state.MediaType,
			StartedAt:        state.StartedAt,
			RemainingSeconds: remaining,
		})
	}
}
