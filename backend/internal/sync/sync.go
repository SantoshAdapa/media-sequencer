// Package sync contains the core playback-position mathematics for the Media Sequencer.
//
// ─────────────────────────────────────────────────────────────────────────────
// WHY WE COMPUTE POSITION FROM A FIXED START TIME INSTEAD OF A RUNNING TIMER
// ─────────────────────────────────────────────────────────────────────────────
//
// Imagine a traditional media player that keeps a counter: "the video is at
// 3 m 42 s". If that player crashes and restarts, the counter resets to zero
// and it has no idea where it left off. Every window would restart at item 0,
// and nothing would be in sync with anything else.
//
// Instead, this system records a single, fixed timestamp — cycleStartTime —
// that says "Window 3's playlist was considered to start playing at 9:00 AM".
// To know what Window 3 should be showing RIGHT NOW, any device anywhere in
// the world only needs to do arithmetic:
//
//     elapsed = (now) − (cycleStartTime)          ← how many seconds into the loop?
//     position = elapsed mod total_playlist_length ← where in the loop does that land?
//
// This design has three major advantages:
//
//  1. SURVIVES RESTARTS — the server can reboot, the browser can refresh, a TV
//     display can power-cycle. When they come back they calculate elapsed time
//     from the fixed start timestamp and instantly resume in exactly the right
//     place, no stored progress needed.
//
//  2. NO DRIFT — a running timer accumulates tiny errors every time it ticks.
//     Arithmetic against a wall-clock timestamp is always exact; two clients
//     that compute the position independently will arrive at the same answer.
//
//  3. TRIVIALLY SYNCABLE — because every window's state is a pure function of
//     (cycleStartTime, now), the front-end does not need to poll the server for
//     the current position. It only needs cycleStartTime once. After that, it
//     can compute the correct frame entirely on its own for as long as it runs.
//     Synchronising five windows is as simple as ensuring they all share the same
//     cycleStartTime.
//
// ─────────────────────────────────────────────────────────────────────────────
package sync

import (
	"errors"
	"fmt"

	"media-sequencer/internal/models"
)

// cycleLengthSeconds is the total duration of one "super-cycle" — the outer loop
// that the elapsed time is wrapped inside. We use 5 hours (18 000 seconds).
// Even if a playlist is only 3 minutes long, the 5-hour window ensures the
// playlist repeats many times before the outer cycle resets.
const cycleLengthSeconds int64 = 5 * 60 * 60 // 18 000 seconds

// ─────────────────────────────────────────────────────────────────────────────
// FUNCTION 1 — ComputeElapsedInCycle
// ─────────────────────────────────────────────────────────────────────────────

// ComputeElapsedInCycle answers the question:
// "How far into its current 5-hour super-cycle is this window right now?"
//
// It subtracts cycleStartTime from the current time, then folds the result into
// the range [0, 5 hours) using the modulo (remainder) operation. This means:
//   - A window that started at 9:00 AM, checked at 9:30 AM → 1 800 seconds elapsed.
//   - The same window checked at 2:30 PM (past the 5-hour mark) → wraps back to
//     1 800 seconds, as if the outer cycle reset and started again.
//
// DEFENSIVE BEHAVIOUR: if the clock on a device is slightly behind the stored
// cycleStartTime (clock skew), (now − cycleStartTime) can be negative. We treat
// that as 0 — "the cycle just began" — rather than crashing or returning garbage.
func ComputeElapsedInCycle(cycleStartTime int64, now int64) int64 {
	elapsed := now - cycleStartTime

	// Guard: if elapsed is negative (device clock skew or misconfiguration),
	// treat it as zero — the cycle is just starting from the perspective of this device.
	if elapsed < 0 {
		elapsed = 0
	}

	// Fold elapsed into the 5-hour window.
	// Example: if elapsed = 19 000 s, and the cycle is 18 000 s,
	// the result is 1 000 — one thousand seconds into the second super-cycle.
	return elapsed % cycleLengthSeconds
}

// ─────────────────────────────────────────────────────────────────────────────
// FUNCTION 2 — ComputeCurrentItem
// ─────────────────────────────────────────────────────────────────────────────

// ComputeCurrentItem answers the question:
// "Given that elapsedSeconds seconds have passed, which media item should be playing?"
//
// It walks the playlist (which must be sorted by OrderIndex, smallest first) and
// accumulates item durations until it finds the item whose time-window covers
// elapsedSeconds.
//
// REPEAT BEHAVIOUR: most playlists are much shorter than 5 hours. If elapsedSeconds
// is longer than the full playlist, we wrap it using modulo so the playlist repeats
// seamlessly within the outer super-cycle. For example: a 3-minute playlist checked
// at elapsed = 4 minutes is treated as elapsed = 1 minute (second loop, 1 min in).
//
// Returns an error if the playlist is empty, because there is no answer to give.
func ComputeCurrentItem(items []models.MediaItem, elapsedSeconds int64) (models.MediaItem, error) {
	if len(items) == 0 {
		return models.MediaItem{}, errors.New("cannot compute current item: playlist is empty")
	}

	// Calculate the total duration of one complete run through the playlist.
	var totalDuration int64
	for _, item := range items {
		totalDuration += int64(item.DurationSeconds)
	}

	if totalDuration <= 0 {
		return models.MediaItem{}, errors.New("cannot compute current item: all items have zero duration")
	}

	// Wrap elapsedSeconds into one playlist length.
	// This is what makes the playlist repeat: a 180-second playlist checked at
	// second 400 becomes second 40 of the playlist (400 mod 180 = 40).
	positionInPlaylist := elapsedSeconds % totalDuration

	// Walk each item, accumulating durations to find the one that "owns" our position.
	// Think of the playlist as a number line:
	//   item0: [0, d0)  →  item1: [d0, d0+d1)  →  item2: [d0+d1, d0+d1+d2)  → …
	// We find the first item where positionInPlaylist falls inside its window.
	var cursor int64
	for _, item := range items {
		itemEnd := cursor + int64(item.DurationSeconds)
		if positionInPlaylist >= cursor && positionInPlaylist < itemEnd {
			return item, nil
		}
		cursor = itemEnd
	}

	// This branch is mathematically unreachable: positionInPlaylist is always in
	// [0, totalDuration), and our loop covers [0, totalDuration) exactly. We include
	// it only to satisfy the Go compiler's requirement that all code paths return.
	return models.MediaItem{}, fmt.Errorf(
		"internal error: position %d not found in playlist with total duration %d",
		positionInPlaylist, totalDuration,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// FUNCTION 3 — ComputeSyncStatus
// ─────────────────────────────────────────────────────────────────────────────

// ComputeSyncStatus answers the question:
// "Is a global sync event happening right now, and if so, how many seconds are left?"
//
// A global sync event temporarily overrides every window's normal playlist and
// forces them all to display the same piece of media at the same time. This
// function checks whether the event is still within its scheduled duration.
//
// IMPORTANT: this function only REPORTS the status — it does NOT update the
// database. If it tells the caller the sync has expired (second return value = 0),
// the caller is responsible for writing that change to the database so future
// requests don't have to do this calculation again.
//
// Returns:
//   - (false, 0) if there is no sync event active.
//   - (false, 0) if the sync event's duration has fully elapsed.
//   - (true, N)  if the sync is still running, where N is how many seconds remain.
func ComputeSyncStatus(state models.SyncState, now int64) (active bool, remainingSeconds int64) {
	// If the database says sync is inactive, there's nothing to check.
	if !state.Active {
		return false, 0
	}

	// Calculate how many seconds have passed since the sync began.
	elapsed := now - state.StartedAt

	// If the elapsed time is at or beyond the total duration, the sync has ended.
	if elapsed >= int64(state.DurationSeconds) {
		return false, 0
	}

	// The sync is still running. Return how many seconds are left.
	remaining := int64(state.DurationSeconds) - elapsed
	return true, remaining
}
