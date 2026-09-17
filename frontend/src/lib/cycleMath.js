// cycleMath.js
// JavaScript mirror of the Go playback-position math in /internal/sync/sync.go.
// Calculates playback position locally using arithmetic rather than server polling.

const CYCLE_LENGTH_SECONDS = 5 * 60 * 60; // 5 hours

/**
 * computeElapsedInCycle
 *
 * Returns seconds elapsed since cycleStartTime, modulo the 5-hour cycle.
 */
export function computeElapsedInCycle(cycleStartTime, now) {
  let elapsed = now - cycleStartTime;
  if (elapsed < 0) elapsed = 0; // Guard against device clock skew
  return elapsed % CYCLE_LENGTH_SECONDS;
}

/**
 * computeCurrentItem
 *
 * Wraps elapsedSeconds over the total playlist duration to determine which
 * media item should be playing, enabling seamless looping within the super-cycle.
 */
export function computeCurrentItem(items, elapsedSeconds) {
  if (!items || items.length === 0) return null;

  const totalDuration = items.reduce((sum, item) => sum + item.durationSeconds, 0);
  if (totalDuration <= 0) return null;

  const positionInPlaylist = elapsedSeconds % totalDuration;

  let cursor = 0;
  for (const item of items) {
    const itemEnd = cursor + item.durationSeconds;
    if (positionInPlaylist >= cursor && positionInPlaylist < itemEnd) {
      // Attach the computed playback offset (how many seconds into this item we are)
      // so video players can synchronize their currentTime.
      return { ...item, offset: positionInPlaylist - cursor };
    }
    cursor = itemEnd;
  }

  return null;
}
