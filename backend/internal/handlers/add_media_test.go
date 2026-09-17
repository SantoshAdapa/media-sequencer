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

	// Create tables needed for the test
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
		INSERT INTO windows (name, cycle_start_time) VALUES ('Test Window', 1000);
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
