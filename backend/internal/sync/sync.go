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

	"github.com/SantoshAdapa/media-sequencer/backend/internal/models"
)

// cycleLengthSeconds defines the fixed 5-hour outer loop (18 000 seconds).
// Playlists repeat continuously within this super-cycle.
const cycleLengthSeconds int64 = 5 * 60 * 60

// ComputeElapsedInCycle returns seconds elapsed since cycleStartTime, modulo the 5-hour cycle.
func ComputeElapsedInCycle(cycleStartTime int64, now int64) int64 {
	elapsed := now - cycleStartTime
	if elapsed < 0 { // Guard against device clock skew
		elapsed = 0
	}
	return elapsed % cycleLengthSeconds
}

// ComputeCurrentItem determines which media item should be playing at elapsedSeconds.
// It wraps elapsedSeconds over the total playlist duration, enabling seamless looping
// within the super-cycle.
func ComputeCurrentItem(items []models.MediaItem, elapsedSeconds int64) (models.MediaItem, error) {
	if len(items) == 0 {
		return models.MediaItem{}, errors.New("cannot compute current item: playlist is empty")
	}

	var totalDuration int64
	for _, item := range items {
		totalDuration += int64(item.DurationSeconds)
	}

	if totalDuration <= 0 {
		return models.MediaItem{}, errors.New("cannot compute current item: all items have zero duration")
	}

	positionInPlaylist := elapsedSeconds % totalDuration

	var cursor int64
	for _, item := range items {
		itemEnd := cursor + int64(item.DurationSeconds)
		if positionInPlaylist >= cursor && positionInPlaylist < itemEnd {
			return item, nil
		}
		cursor = itemEnd
	}

	return models.MediaItem{}, fmt.Errorf(
		"internal error: position %d not found in playlist with total duration %d",
		positionInPlaylist, totalDuration,
	)
}

// ComputeSyncStatus determines if a global sync event is currently active, returning
// the remaining duration. It performs pure mathematical evaluation of the state.
func ComputeSyncStatus(state models.SyncState, now int64) (active bool, remainingSeconds int64) {
	if !state.Active {
		return false, 0
	}

	elapsed := now - state.StartedAt
	if elapsed >= int64(state.DurationSeconds) {
		return false, 0
	}

	return true, int64(state.DurationSeconds) - elapsed
}
