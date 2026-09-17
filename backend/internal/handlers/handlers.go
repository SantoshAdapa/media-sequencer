// Package handlers contains all the HTTP request handlers for the Media Sequencer API.
// Each handler is responsible for one specific URL: it reads the request, talks to the
// database if needed, runs any calculations, and writes back a JSON response.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"media-sequencer/internal/models"
	// Our sync package name clashes with the standard library "sync" package,
	// so we import it under the alias "playback" to keep things readable.
	playback "media-sequencer/internal/sync"
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
// RemainingSeconds is zero when the sync is not active.
type syncStatusResponse struct {
	Active           bool   `json:"active"`
	MediaURL         string `json:"mediaUrl"`
	MediaType        string `json:"mediaType"`
	RemainingSeconds int64  `json:"remainingSeconds"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ROUTER SETUP
// ─────────────────────────────────────────────────────────────────────────────

// SetupRoutes registers every URL path our server knows how to handle and attaches
// the correct handler function to each one. It also installs "middleware" — code that
// runs automatically for EVERY request before the handler does (e.g. CORS headers).
func SetupRoutes(r chi.Router, db *sql.DB) {
	// ── CORS Middleware ──────────────────────────────────────────────────────
	// Browsers enforce a security rule called the "Same-Origin Policy" that
	// blocks a web page on domain A from calling an API on domain B.
	// CORS (Cross-Origin Resource Sharing) is the standard way to opt out of
	// that restriction for trusted callers. For this assignment we allow all
	// origins ("*"). In a real production system this would be locked down to
	// only the specific frontend domain.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
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
	r.Post("/sync", triggerSyncHandler(db))
	r.Get("/sync/status", getSyncStatusHandler(db))
}

// ─────────────────────────────────────────────────────────────────────────────
// SHARED HELPERS
// ─────────────────────────────────────────────────────────────────────────────

// writeJSON serialises any Go value into JSON and sends it to the caller with
// the correct Content-Type header and HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends a standardised JSON error response, e.g.:
//
//	{"error": "window not found"}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// fetchMediaItems retrieves the complete ordered playlist for a given window from the database.
// Results are sorted by order_index ascending (0 first) so callers receive items in play order.
// Returns an empty (non-nil) slice — never nil — so JSON always encodes as [] rather than null.
func fetchMediaItems(ctx context.Context, db *sql.DB, windowID int) ([]models.MediaItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, window_id, type, url, duration_seconds, order_index
		FROM   media_items
		WHERE  window_id = ?
		ORDER  BY order_index ASC`, windowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []models.MediaItem{} // pre-initialised so JSON encodes as [] not null
	for rows.Next() {
		var item models.MediaItem
		if err := rows.Scan(
			&item.ID, &item.WindowID, &item.Type,
			&item.URL, &item.DurationSeconds, &item.OrderIndex,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
			writeError(w, http.StatusInternalServerError, "failed to query windows")
			return
		}
		defer rows.Close()

		// Capture the current Unix timestamp once so every window's calculation uses
		// the same "now" value — important for consistency in a single response.
		now := time.Now().Unix()
		responses := []windowResponse{} // empty slice, never nil

		for rows.Next() {
			var win models.Window
			if err := rows.Scan(&win.ID, &win.Name, &win.CycleStartTime); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to read window row")
				return
			}

			// Load this window's full ordered playlist from the database.
			items, err := fetchMediaItems(r.Context(), db, win.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to query media items")
				return
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

		if err := rows.Err(); err != nil {
			writeError(w, http.StatusInternalServerError, "error iterating windows")
			return
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
	// validTypes is the complete set of allowed values for the "type" field.
	// Defined once here, at handler creation time, rather than rebuilding the map on every request.
	validTypes := map[string]bool{"image": true, "video": true, "blank": true}

	return func(w http.ResponseWriter, r *http.Request) {
		// ── Parse and validate the window ID from the URL ─────────────────────
		// chi.URLParam extracts the "{id}" segment from the URL path.
		windowID, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "window id in URL must be an integer")
			return
		}

		// ── Confirm the window exists ─────────────────────────────────────────
		// We check before doing anything else so we can return a clear 404 rather
		// than a confusing foreign-key constraint error from the database.
		var exists bool
		err = db.QueryRowContext(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM windows WHERE id = ?)", windowID).Scan(&exists)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check if window exists")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "window not found")
			return
		}

		// ── Decode the JSON request body ──────────────────────────────────────
		var body struct {
			Type            string `json:"type"`
			URL             string `json:"url"`
			DurationSeconds int    `json:"duration_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}

		// ── Validate fields ───────────────────────────────────────────────────
		if !validTypes[body.Type] {
			writeError(w, http.StatusBadRequest, `"type" must be one of: "image", "video", "blank"`)
			return
		}
		if body.DurationSeconds <= 0 {
			writeError(w, http.StatusBadRequest, `"duration_seconds" must be a positive integer`)
			return
		}

		// ── Determine the next order_index ────────────────────────────────────
		// We find the current highest position in the playlist and add 1.
		// COALESCE(-1, …) handles the case where the playlist is empty: the first
		// item will get index 0 (−1 + 1 = 0).
		var maxIndex int
		err = db.QueryRowContext(r.Context(),
			"SELECT COALESCE(MAX(order_index), -1) FROM media_items WHERE window_id = ?",
			windowID).Scan(&maxIndex)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to determine playlist position")
			return
		}
		nextIndex := maxIndex + 1

		// ── Insert the new media item ─────────────────────────────────────────
		result, err := db.ExecContext(r.Context(), `
			INSERT INTO media_items (window_id, type, url, duration_seconds, order_index)
			VALUES (?, ?, ?, ?, ?)`,
			windowID, body.Type, body.URL, body.DurationSeconds, nextIndex)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save media item")
			return
		}

		newID, err := result.LastInsertId()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read new item ID")
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
		// ── Decode the JSON request body ──────────────────────────────────────
		var body struct {
			MediaURL        string `json:"media_url"`
			MediaType       string `json:"media_type"`
			DurationSeconds int    `json:"duration_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "malformed JSON body: "+err.Error())
			return
		}

		// ── Validate fields ───────────────────────────────────────────────────
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

		// ── Overwrite the singleton sync_state row ────────────────────────────
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
			_, _ = db.ExecContext(r.Context(),
				"UPDATE sync_state SET active = 0 WHERE id = 1")
		}

		writeJSON(w, http.StatusOK, syncStatusResponse{
			Active:           isActive,
			MediaURL:         state.MediaURL,
			MediaType:        state.MediaType,
			RemainingSeconds: remaining,
		})
	}
}
