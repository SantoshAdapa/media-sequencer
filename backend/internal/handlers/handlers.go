package handlers

import (
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
	playback "github.com/SantoshAdapa/media-sequencer/backend/internal/sync"
)

type windowResponse struct {
	ID             int                `json:"id"`
	Name           string             `json:"name"`
	CycleStartTime int64              `json:"cycleStartTime"`
	MediaItems     []models.MediaItem `json:"mediaItems"`
	CurrentItem    *models.MediaItem  `json:"currentItem"`
}

type syncStatusResponse struct {
	Active           bool   `json:"active"`
	MediaURL         string `json:"mediaUrl"`
	MediaType        string `json:"mediaType"`
	RemainingSeconds int64  `json:"remainingSeconds"`
}

func SetupRoutes(r chi.Router, db *sql.DB) {
	allowedOrigins := []string{
		"http://localhost:5173",
		"https://frontend-eta-seven-28.vercel.app",
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
		MaxAge:           300,
	}))

	r.Get("/health", healthCheckHandler)

	r.Get("/windows", getWindowsHandler(db))
	r.Post("/windows/{id}/media", addMediaHandler(db))
	r.Delete("/windows/{id}/media/{itemId}", deleteMediaHandler(db))
	r.Post("/sync", triggerSyncHandler(db))
	r.Get("/sync/status", getSyncStatusHandler(db))
}

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

// getWindowsHandler returns every display window with its ordered playlist
// and a computed "currentItem" based on the deterministic playback math.
func getWindowsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Printf("error iterating windows: %v", err)
			writeError(w, http.StatusInternalServerError, "error iterating windows")
			return
		}

		// Fetch all items at once to avoid N+1 queries.
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
		responses := []windowResponse{}

		// Assemble the final response.
		for _, win := range windowsData {
			items := itemsMap[win.ID]
			if items == nil {
				items = []models.MediaItem{} // ensure JSON encodes as [] not null
			}

			elapsed := playback.ComputeElapsedInCycle(win.CycleStartTime, now)
			currentItem, computeErr := playback.ComputeCurrentItem(items, elapsed)

			resp := windowResponse{
				ID:             win.ID,
				Name:           win.Name,
				CycleStartTime: win.CycleStartTime,
				MediaItems:     items,
			}
			if computeErr == nil {
				resp.CurrentItem = &currentItem
			}

			responses = append(responses, resp)
		}

		writeJSON(w, http.StatusOK, responses)
	}
}

// addMediaHandler appends a new piece of media to the end of a specific window's playlist.
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

		// Use a transaction to safely determine the next order_index and insert.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			log.Printf("failed to begin transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to begin transaction")
			return
		}
		defer func() { _ = tx.Rollback() }()

		// Force SQLite to acquire an exclusive lock immediately to prevent deadlocks.
		_, _ = tx.ExecContext(r.Context(), "UPDATE windows SET id = id WHERE id = ?", windowID)

		var exists bool
		err = tx.QueryRowContext(r.Context(),
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

		// ── Determine the next order_index ────────────────────────────────────
		// We find the current highest position in the playlist and add 1.
		// COALESCE(-1, …) handles the case where the playlist is empty: the first
		// item will get index 0 (−1 + 1 = 0).
		var maxIndex int
		err = tx.QueryRowContext(r.Context(),
			"SELECT COALESCE(MAX(order_index), -1) FROM media_items WHERE window_id = ?",
			windowID).Scan(&maxIndex)
		if err != nil {
			log.Printf("failed to determine playlist position: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to determine playlist position")
			return
		}
		nextIndex := maxIndex + 1

		// ── Insert the new media item ─────────────────────────────────────────
		result, err := tx.ExecContext(r.Context(), `
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

		if err := tx.Commit(); err != nil {
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

		// Return 204 No Content on success
		w.WriteHeader(http.StatusNoContent)
	}
}

// triggerSyncHandler starts a global synchronisation event.
// Overwrites the singleton sync_state row (id = 1).
func triggerSyncHandler(db *sql.DB) http.HandlerFunc {
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

		startedAt := time.Now().Unix()

		// Overwrite the singleton sync_state row. The schema's CHECK constraint
		// guarantees id=1 is the only row, preventing active sync race conditions.
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

// getSyncStatusHandler reports whether a global sync event is active.
// Lazy-expires the sync in the DB if the math indicates it has elapsed.
func getSyncStatusHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
				writeJSON(w, http.StatusOK, syncStatusResponse{Active: false})
				return
			}
			log.Printf("failed to read sync state: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to read sync state")
			return
		}
		state.Active = activeInt == 1

		now := time.Now().Unix()
		isActive, remaining := playback.ComputeSyncStatus(state, now)

		// Lazy expiry: if the DB says active but the time has passed, deactivate it.
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
			RemainingSeconds: remaining,
		})
	}
}
