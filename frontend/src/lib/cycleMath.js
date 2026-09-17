// cycleMath.js
// =============================================================================
// This file is a JavaScript mirror of the Go playback-position math in
// /internal/sync/sync.go. Both files must stay in agreement.
//
// WHY COMPUTE POSITION LOCALLY INSTEAD OF ASKING THE SERVER?
// -----------------------------------------------------------
// If the frontend asked the server "what should Window 3 be showing right now?"
// on every tick, we'd generate hundreds of network requests per minute and the
// display would stutter every time the network hiccuped.
//
// Instead, the server hands us ONE piece of data per window: cycleStartTime —
// the Unix timestamp (seconds) when that window's playlist was considered to
// start from the beginning. From that single number, the browser can calculate
// the correct item at any moment in the future using only arithmetic:
//
//   elapsed   = (now - cycleStartTime) mod 5_hours
//   position  = elapsed mod total_playlist_length
//   → walk items until we find which one owns that position
//
// This means: no polling for position, no drift, no stutter on network lag.
// =============================================================================

/** Total duration of the outer super-cycle in seconds (5 hours). */
const CYCLE_LENGTH_SECONDS = 5 * 60 * 60; // 18 000

/**
 * computeElapsedInCycle
 *
 * Answers: "How many seconds into its 5-hour super-cycle is this window right now?"
 *
 * Works the same way as the Go version:
 *   1. Subtract cycleStartTime from now to get raw elapsed seconds.
 *   2. Clamp to 0 if negative (guards against slight clock skew between devices).
 *   3. Fold into the 5-hour window with modulo.
 *
 * @param {number} cycleStartTime - Unix timestamp (seconds) when the window's loop began.
 * @param {number} now            - Current Unix timestamp (seconds).
 * @returns {number} Seconds elapsed in the current 5-hour cycle, always in [0, 18000).
 */
export function computeElapsedInCycle(cycleStartTime, now) {
  let elapsed = now - cycleStartTime;

  // Defensive: if this device's clock is slightly behind the server's recorded
  // start time, elapsed can be negative. Treat that as "the cycle just started".
  if (elapsed < 0) elapsed = 0;

  return elapsed % CYCLE_LENGTH_SECONDS;
}

/**
 * computeCurrentItem
 *
 * Answers: "Given that `elapsedSeconds` have passed, which media item should be playing?"
 *
 * The playlist is treated as a repeating loop: if elapsedSeconds exceeds the total
 * duration of all items, it wraps back to the beginning. This ensures media plays
 * continuously for the full 5-hour cycle regardless of playlist length.
 *
 * Example: playlist [A=10s, B=20s, C=15s] → total 45s
 *   elapsed=50 → 50 mod 45 = 5 → still inside A [0,10) → return A
 *
 * @param {Array}  items          - Array of MediaItem objects, sorted by orderIndex ascending.
 * @param {number} elapsedSeconds - How far into the current cycle we are (from computeElapsedInCycle).
 * @returns {Object|null} The MediaItem that should be showing now, or null if the playlist is empty.
 */
export function computeCurrentItem(items, elapsedSeconds) {
  if (!items || items.length === 0) return null;

  // Add up every item's duration to find the full playlist length.
  const totalDuration = items.reduce((sum, item) => sum + item.durationSeconds, 0);
  if (totalDuration <= 0) return null;

  // Wrap elapsed into one playlist length so the list repeats seamlessly.
  const positionInPlaylist = elapsedSeconds % totalDuration;

  // Walk the items as if they sit on a number line:
  //   item[0] owns [0, d0)
  //   item[1] owns [d0, d0+d1)
  //   item[2] owns [d0+d1, d0+d1+d2)  … and so on.
  // The first item whose window contains our position is the current item.
  let cursor = 0;
  for (const item of items) {
    const itemEnd = cursor + item.durationSeconds;
    if (positionInPlaylist >= cursor && positionInPlaylist < itemEnd) {
      return item;
    }
    cursor = itemEnd;
  }

  // Mathematically unreachable: positionInPlaylist is always in [0, totalDuration).
  return null;
}
