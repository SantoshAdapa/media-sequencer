package sync

import (
	"testing"

	"github.com/SantoshAdapa/media-sequencer/backend/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// HELPERS — shared test fixtures
// ─────────────────────────────────────────────────────────────────────────────

// makeItems builds a simple playlist from a variadic list of duration values.
// OrderIndex is assigned in order (0, 1, 2 …). Types and URLs are filled with
// recognisable placeholder values so test failure messages are easy to read.
func makeItems(durations ...int) []models.MediaItem {
	items := make([]models.MediaItem, len(durations))
	for i, d := range durations {
		items[i] = models.MediaItem{
			ID:              i + 1,
			WindowID:        1,
			Type:            "image",
			URL:             "https://example.com/img" + string(rune('A'+i)),
			DurationSeconds: d,
			OrderIndex:      i,
		}
	}
	return items
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 1 — ComputeElapsedInCycle
// ─────────────────────────────────────────────────────────────────────────────

func TestComputeElapsedInCycle(t *testing.T) {
	// 5-hour cycle in seconds, so we can reference it in expected values.
	const cycle = int64(5 * 60 * 60) // 18 000

	tests := []struct {
		name           string
		cycleStartTime int64
		now            int64
		wantElapsed    int64
	}{
		{
			// Ordinary case: 30 minutes into the first cycle.
			name:           "normal mid-cycle position",
			cycleStartTime: 1_000_000,
			now:            1_000_000 + 1_800, // +30 min
			wantElapsed:    1_800,
		},
		{
			// Exactly at the boundary between the first and second cycle.
			// elapsed = cycle, so (cycle) mod cycle = 0.
			name:           "exactly one full cycle elapsed — wraps to 0",
			cycleStartTime: 1_000_000,
			now:            1_000_000 + cycle,
			wantElapsed:    0,
		},
		{
			// One full cycle plus 500 seconds into the next — should land at 500.
			name:           "wraparound into second cycle",
			cycleStartTime: 1_000_000,
			now:            1_000_000 + cycle + 500,
			wantElapsed:    500,
		},
		{
			// Multiple complete cycles elapsed — should still wrap correctly.
			name:           "multiple cycles elapsed",
			cycleStartTime: 0,
			now:            3*cycle + 7_777,
			wantElapsed:    7_777,
		},
		{
			// Defensive case: now is BEFORE cycleStartTime (clock skew / bad config).
			// Must return 0, not a negative number.
			name:           "negative elapsed clamped to zero (clock skew)",
			cycleStartTime: 1_000_000,
			now:            999_999, // 1 second BEFORE start
			wantElapsed:    0,
		},
		{
			// Both timestamps are identical — zero time has elapsed.
			name:           "zero elapsed — now equals cycleStartTime",
			cycleStartTime: 5_000,
			now:            5_000,
			wantElapsed:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeElapsedInCycle(tt.cycleStartTime, tt.now)
			if got != tt.wantElapsed {
				t.Errorf("ComputeElapsedInCycle(%d, %d) = %d; want %d",
					tt.cycleStartTime, tt.now, got, tt.wantElapsed)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 2 — ComputeCurrentItem
// ─────────────────────────────────────────────────────────────────────────────

func TestComputeCurrentItem(t *testing.T) {
	// Reference playlist: A=10s, B=20s, C=15s  → total 45 s
	//   [0,10) → A
	//   [10,30) → B
	//   [30,45) → C
	playlist := makeItems(10, 20, 15) // total = 45 s

	tests := []struct {
		name        string
		items       []models.MediaItem
		elapsed     int64
		wantOrderIdx int
		wantErr     bool
	}{
		{
			// 5 seconds in → inside item A [0,10)
			name:         "normal position — first item",
			items:        playlist,
			elapsed:      5,
			wantOrderIdx: 0,
		},
		{
			// Exactly at 10 seconds → start of item B [10,30)
			name:         "exact boundary — transitions to second item",
			items:        playlist,
			elapsed:      10,
			wantOrderIdx: 1,
		},
		{
			// Exactly at 30 seconds → start of item C [30,45)
			name:         "exact boundary — transitions to third item",
			items:        playlist,
			elapsed:      30,
			wantOrderIdx: 2,
		},
		{
			// 29 seconds → still inside item B [10,30), one second before boundary
			name:         "one second before boundary — still in second item",
			items:        playlist,
			elapsed:      29,
			wantOrderIdx: 1,
		},
		{
			// 45 s elapsed = one full playlist length → wraps to 0 → item A again
			name:         "wraparound — exactly one playlist length → back to first item",
			items:        playlist,
			elapsed:      45,
			wantOrderIdx: 0,
		},
		{
			// 45+10 = 55 s → 55 mod 45 = 10 → inside item B [10,30)
			name:         "wraparound — second loop lands in second item",
			items:        playlist,
			elapsed:      55, // 55 mod 45 = 10 → item B
			wantOrderIdx: 1,
		},
		{
			// 45+30 = 75 s → 75 mod 45 = 30 → exactly start of item C
			name:         "wraparound — second loop exact boundary to third item",
			items:        playlist,
			elapsed:      75, // 75 mod 45 = 30 → item C
			wantOrderIdx: 2,
		},
		{
			// Far into the future — many full playlist loops
			// 1_000 mod 45 = 1000 - 22*45 = 1000 - 990 = 10 → item B
			name:         "many wraparounds — large elapsed time",
			items:        playlist,
			elapsed:      1_000, // 1000 mod 45 = 10 → item B [10,30)
			wantOrderIdx: 1,
		},
		{
			// Single-item playlist — must always return that item regardless of elapsed
			name:         "single-item playlist — always returns the only item",
			items:        makeItems(60),
			elapsed:      999,
			wantOrderIdx: 0,
		},
		{
			// Empty playlist — must return an error
			name:    "empty playlist — returns error",
			items:   []models.MediaItem{},
			elapsed: 10,
			wantErr: true,
		},
		{
			// All items have zero duration — must return an error
			name:    "all zero duration items — returns error",
			items:   makeItems(0, 0, 0),
			elapsed: 10,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeCurrentItem(tt.items, tt.elapsed)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected an error but got none (item returned: %+v)", got)
				}
				return // error case handled — nothing more to check
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.OrderIndex != tt.wantOrderIdx {
				t.Errorf("ComputeCurrentItem(elapsed=%d) returned OrderIndex=%d; want %d",
					tt.elapsed, got.OrderIndex, tt.wantOrderIdx)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 3 — ComputeSyncStatus
// ─────────────────────────────────────────────────────────────────────────────

func TestComputeSyncStatus(t *testing.T) {
	// A helper that builds a SyncState without having to spell out every field.
	makeSyncState := func(active bool, startedAt int64, durationSeconds int) models.SyncState {
		return models.SyncState{
			Active:          active,
			MediaURL:        "https://example.com/sync.mp4",
			MediaType:       "video",
			StartedAt:       startedAt,
			DurationSeconds: durationSeconds,
		}
	}

	tests := []struct {
		name            string
		state           models.SyncState
		now             int64
		wantActive      bool
		wantRemaining   int64
	}{
		{
			// Active=false in the DB → not active, zero remaining.
			name:          "no sync — Active flag is false",
			state:         makeSyncState(false, 1_000, 60),
			now:           1_030,
			wantActive:    false,
			wantRemaining: 0,
		},
		{
			// Sync started 30 s ago, lasts 60 s → 30 seconds remain.
			name:          "active sync — mid-duration",
			state:         makeSyncState(true, 1_000, 60),
			now:           1_030, // 30 s elapsed, 30 s remain
			wantActive:    true,
			wantRemaining: 30,
		},
		{
			// Sync started 1 s ago, lasts 60 s → 59 seconds remain.
			name:          "active sync — just started",
			state:         makeSyncState(true, 1_000, 60),
			now:           1_001,
			wantActive:    true,
			wantRemaining: 59,
		},
		{
			// Sync started 59 s ago, lasts 60 s → 1 second remains.
			name:          "active sync — one second before expiry",
			state:         makeSyncState(true, 1_000, 60),
			now:           1_059, // elapsed=59, remaining=1
			wantActive:    true,
			wantRemaining: 1,
		},
		{
			// Elapsed equals duration exactly → sync has JUST expired (boundary).
			name:          "expired sync — elapsed equals duration exactly",
			state:         makeSyncState(true, 1_000, 60),
			now:           1_060, // elapsed = 60 = DurationSeconds → expired
			wantActive:    false,
			wantRemaining: 0,
		},
		{
			// Elapsed exceeds duration — sync expired some time ago.
			name:          "expired sync — well past duration",
			state:         makeSyncState(true, 1_000, 60),
			now:           2_000, // elapsed = 1000s, duration = 60s → long expired
			wantActive:    false,
			wantRemaining: 0,
		},
		{
			// Zero-duration sync with Active=true should immediately be expired.
			name:          "zero-duration sync — immediately expired",
			state:         makeSyncState(true, 1_000, 0),
			now:           1_000, // elapsed=0, duration=0 → 0 >= 0 → expired
			wantActive:    false,
			wantRemaining: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotActive, gotRemaining := ComputeSyncStatus(tt.state, tt.now)

			if gotActive != tt.wantActive {
				t.Errorf("ComputeSyncStatus().active = %v; want %v", gotActive, tt.wantActive)
			}
			if gotRemaining != tt.wantRemaining {
				t.Errorf("ComputeSyncStatus().remainingSeconds = %d; want %d", gotRemaining, tt.wantRemaining)
			}
		})
	}
}
