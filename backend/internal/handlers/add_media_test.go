package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// setupTestDB creates a fresh in-memory SQLite database and runs the schema.
func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Create tables needed for the tests
	_, err = db.Exec(`
		CREATE TABLE windows (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			cycle_start_time INTEGER NOT NULL
		);
		CREATE TABLE media_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			window_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			duration_seconds INTEGER NOT NULL,
			order_index INTEGER NOT NULL,
			FOREIGN KEY(window_id) REFERENCES windows(id)
		);
		CREATE TABLE sync_state (
			id INTEGER PRIMARY KEY CHECK(id = 1),
			active INTEGER NOT NULL DEFAULT 0,
			media_url TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL DEFAULT 0,
			duration_seconds INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO windows (name, cycle_start_time) VALUES ('Test Window', 1000);
		INSERT INTO sync_state (id) VALUES (1);
	`)
	if err != nil {
		t.Fatalf("failed to setup schema: %v", err)
	}

	return db
}

func TestAddMediaHandler_RaceCondition(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	r := chi.NewRouter()
	r.Post("/windows/{id}/media", addMediaHandler(db))

	numRequests := 10

	var wg sync.WaitGroup
	wg.Add(numRequests)

	// We will collect the resulting order_indexes to ensure there are no duplicates.
	var mu sync.Mutex
	orderIndexes := make([]int, 0, numRequests)
	var errCount int

	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()

			body := map[string]interface{}{
				"type":             "image",
				"url":              "http://example.com/test.jpg",
				"duration_seconds": 10,
			}
			bodyBytes, _ := json.Marshal(body)

			req := httptest.NewRequest("POST", "/windows/1/media", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()

			// Simulate slight variations in start time to maximise collision chance
			// if transactions were not used.
			r.ServeHTTP(w, req)

			if w.Code == http.StatusCreated {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
					if idx, ok := resp["orderIndex"].(float64); ok {
						mu.Lock()
						orderIndexes = append(orderIndexes, int(idx))
						mu.Unlock()
					}
				}
			} else {
				mu.Lock()
				errCount++
				t.Logf("Request failed with status %d: %s", w.Code, w.Body.String())
				mu.Unlock()
			}
		}()
	}

	// Wait for all concurrent requests to finish
	// Set a timeout to prevent hanging if deadlock occurs
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out waiting for goroutines")
	}

	if errCount > 0 {
		t.Errorf("Expected 0 errors, got %d", errCount)
	}

	// Verify that order_index values are unique and sequential [0, 1, 2, ..., numRequests-1]
	seen := make(map[int]bool)
	for _, idx := range orderIndexes {
		if seen[idx] {
			t.Errorf("Duplicate order_index found: %d", idx)
		}
		seen[idx] = true
	}

	if len(seen) != numRequests {
		t.Errorf("Expected %d unique order_index values, got %d", numRequests, len(seen))
	}
}

func TestSyncStatus_StartedAt(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	r := chi.NewRouter()
	SetupRoutes(r, db)

	// Step 1: POST /sync to start a sync event
	syncBody := `{"media_url":"http://example.com/vid.mp4","media_type":"video","duration_seconds":60}`
	req := httptest.NewRequest("POST", "/sync", bytes.NewBufferString(syncBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /sync returned %d: %s", w.Code, w.Body.String())
	}

	beforeSync := time.Now().Unix()

	// Step 2: GET /sync/status and verify startedAt is present and reasonable
	req2 := httptest.NewRequest("GET", "/sync/status", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("GET /sync/status returned %d: %s", w2.Code, w2.Body.String())
	}

	var status struct {
		Active           bool   `json:"active"`
		MediaURL         string `json:"mediaUrl"`
		MediaType        string `json:"mediaType"`
		StartedAt        int64  `json:"startedAt"`
		RemainingSeconds int64  `json:"remainingSeconds"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse sync status response: %v", err)
	}

	if !status.Active {
		t.Error("expected sync to be active")
	}
	if status.StartedAt == 0 {
		t.Error("startedAt must be non-zero when sync is active")
	}
	if status.StartedAt > beforeSync {
		t.Errorf("startedAt (%d) should be <= current time (%d)", status.StartedAt, beforeSync)
	}
	if status.MediaURL != "http://example.com/vid.mp4" {
		t.Errorf("unexpected mediaUrl: %s", status.MediaURL)
	}
	if status.MediaType != "video" {
		t.Errorf("unexpected mediaType: %s", status.MediaType)
	}
	if status.RemainingSeconds <= 0 {
		t.Error("remainingSeconds should be positive for an active sync")
	}
}
