import { useState, useEffect, useRef } from 'react';
import { getSyncStatus } from '../lib/api';
import { computeElapsedInCycle, computeCurrentItem } from '../lib/cycleMath';
import './WindowPlayer.css';

// ─────────────────────────────────────────────────────────────────────────────
// HOW THE POLLING + FALLBACK LOGIC WORKS (for non-technical readers)
// ─────────────────────────────────────────────────────────────────────────────
//
// Every half-second (500 ms) each WindowPlayer:
//
//  1. Asks the server: "Is there a global sync event happening right now?"
//     (GET /sync/status)
//
//  2a. YES — sync is active:
//      All windows stop their individual playlists and show the same media.
//      A blue "SYNC" badge appears so operators know it's a takeover.
//
//  2b. NO — no sync:
//      The player ignores the server completely and calculates LOCALLY which
//      media item belongs right now, using only arithmetic against the window's
//      cycleStartTime. No extra network call needed, no lag, no drift.
//
//  3. If the network request in step 1 fails (server unreachable, timeout):
//     The player silently falls back to local computation (2b). This makes
//     each window self-sufficient during brief outages.
//
// ASSUMPTION — Video looping:
//   Videos are set to loop continuously. If the actual video file is shorter
//   than duration_seconds, the video will simply loop within that slot. If
//   the file is longer, it will be cut off when the slot ends and the next
//   item takes over. This is an acceptable simplification for this assignment.
// ─────────────────────────────────────────────────────────────────────────────

// ─── MediaRenderer ────────────────────────────────────────────────────────────
// Picks the correct HTML element to display for any given media item type.

function MediaRenderer({ item }) {
  const videoRef = useRef(null);

  // When the item updates (which is every 500ms due to the WindowPlayer tick),
  // we check if the video's actual playback time is drifting from the mathematically
  // correct offset. If it's off by more than 1 second, we force a sync.
  // This guarantees all windows stay in sync, but prevents stuttering.
  useEffect(() => {
    if (videoRef.current && item?.type === 'video' && item.offset != null) {
      const vid = videoRef.current;
      // If the video has loaded its metadata, we know its true duration.
      // We modulo the offset by duration so that if a 10s video is played
      // in a 30s slot, it correctly syncs to the 2nd or 3rd loop.
      const targetTime = (vid.duration && vid.duration > 0) 
        ? (item.offset % vid.duration) 
        : item.offset;

      const diff = Math.abs(vid.currentTime - targetTime);
      if (diff > 1.0) {
        vid.currentTime = targetTime;
      }
    }
  }, [item]);

  if (!item) {
    // No item at all — show a dark placeholder.
    return <div className="media-blank" aria-label="No media" />;
  }

  if (item.type === 'blank') {
    // An intentional empty slot in the schedule — dark box, not white.
    return <div className="media-blank" aria-label="Blank slot" />;
  }

  if (item.type === 'image') {
    return (
      <img
        src={item.url}
        alt="Scheduled media"
        className="media-content"
        // If the image fails to load (dead URL etc.), we fall back to a visible
        // error style rather than a broken-image icon filling the viewport.
        onError={(e) => { e.currentTarget.className = 'media-error'; }}
      />
    );
  }

  if (item.type === 'video') {
    // key={item.url} forces React to unmount and remount the <video> element
    // whenever the URL changes. Without this, the browser keeps the old video
    // in the player buffer even after the src attribute changes.
    return (
      <video
        ref={videoRef}
        key={item.url}
        src={item.url}
        className="media-content"
        autoPlay
        loop
        muted
        playsInline
      />
    );
  }

  return <div className="media-blank">{item.type} (unsupported)</div>;
}

// ─── WindowPlayer ─────────────────────────────────────────────────────────────

/**
 * WindowPlayer is responsible for continuously showing the correct piece of media
 * for one display window. It ticks every 500 ms, checks for a global sync override,
 * and falls back to local cycle-math computation when no sync is active.
 *
 * Props:
 *   window — the full window object from the backend (id, name, cycleStartTime, mediaItems, currentItem)
 */
function WindowPlayer({ window: win }) {
  // currentItem: the MediaItem being shown right now (either from sync or from playlist).
  const [currentItem, setCurrentItem] = useState(win.currentItem ?? null);

  // syncInfo: if a sync is active, holds { active, mediaUrl, mediaType, remainingSeconds }.
  const [syncInfo, setSyncInfo] = useState(null);

  // We keep a stable reference to win.mediaItems so the interval closure always
  // sees the latest playlist even if the parent re-renders with updated data.
  const mediaItemsRef = useRef(win.mediaItems ?? []);
  const cycleStartRef = useRef(win.cycleStartTime);

  // Keep refs in sync with incoming props.
  useEffect(() => {
    mediaItemsRef.current = win.mediaItems ?? [];
    cycleStartRef.current = win.cycleStartTime;
  }, [win.mediaItems, win.cycleStartTime]);

  useEffect(() => {
    // ── TICK FUNCTION ──────────────────────────────────────────────────────
    // This runs every 500 ms. It is defined as async so we can await the
    // network call to /sync/status without blocking the browser's event loop.
    const tick = async () => {
      let status = null;

      try {
        // Step 1: Ask the server if a global sync event is happening.
        status = await getSyncStatus();
      } catch (_) {
        // Network error — proceed as if sync is off (local fallback below).
      }

      if (status?.active) {
        // ── SYNC MODE ──
        // Override this window's normal playlist with the global sync media.
        setSyncInfo(status);
        const nowSeconds = Math.floor(Date.now() / 1000);
        setCurrentItem({
          type: status.mediaType,
          url:  status.mediaUrl,
          offset: Math.max(0, nowSeconds - status.startedAt),
        });
      } else {
        // ── NORMAL MODE ─────────────────────────────────────────────────────
        // No sync. Compute which playlist item owns the current timestamp
        // using purely local arithmetic — zero extra network calls.
        setSyncInfo(null);
        const nowSeconds = Math.floor(Date.now() / 1000);
        const elapsed    = computeElapsedInCycle(cycleStartRef.current, nowSeconds);
        const item       = computeCurrentItem(mediaItemsRef.current, elapsed);
        setCurrentItem(item);
      }
    };

    tick(); // Run immediately on mount so we don't show stale data for 500 ms.
    const id = setInterval(tick, 500);

    // Cleanup: when this component is removed from the page, cancel the interval.
    // Without this, the interval would keep running and try to update state on
    // a component that no longer exists, causing React warnings.
    return () => clearInterval(id);

    // We intentionally leave win.mediaItems / win.cycleStartTime out of the
    // dependency array and instead use refs — this prevents the interval from
    // being destroyed and recreated on every parent re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win.id]);

  return (
    <div className="window-player">
      {/* 16:9 viewport wrapper */}
      <div className="media-viewport">
        <MediaRenderer item={currentItem} />

        {/* Overlay badge — shown only while a global sync is active */}
        {syncInfo?.active && (
          <div className="badge badge-sync" title={`${syncInfo.remainingSeconds}s remaining`}>
            SYNCED — {syncInfo.remainingSeconds > 0 ? `${syncInfo.remainingSeconds}s left` : 'ending…'}
          </div>
        )}
      </div>

      {/* Footer: human-readable caption of what is currently playing */}
      <div className="player-footer">
        {currentItem ? (
          <span className="player-caption">
            {syncInfo?.active
              ? /* During a sync, say so clearly */
                `Sync override: ${currentItem.type} — ${syncInfo.remainingSeconds}s remaining`
              : /* Normal playback: spell out type, duration, and position */
                `Now playing: ${
                  currentItem.type.charAt(0).toUpperCase() + currentItem.type.slice(1)
                }${currentItem.durationSeconds != null ? ` (${currentItem.durationSeconds}s)` : ''}${
                  currentItem.orderIndex != null
                    ? ` — Item ${currentItem.orderIndex + 1} of ${win.mediaItems?.length ?? '?'}`
                    : ''
                }`
            }
          </span>
        ) : (
          <span className="player-empty">No items in playlist yet.</span>
        )}
      </div>
    </div>
  );
}

export default WindowPlayer;
