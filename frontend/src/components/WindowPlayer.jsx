import { useState, useEffect, useRef } from 'react';

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
//   than duration_seconds, the video will simply loop within that slot. If
//   the file is longer, it will be cut off when the slot ends and the next
//   item takes over. This is an acceptable simplification for this assignment.
// ─────────────────────────────────────────────────────────────────────────────

// MediaRenderer picks the correct HTML element to display for a given media item type.
function MediaRenderer({ item }) {
  const videoRef = useRef(null);

  // If the video's actual playback time is drifting from the mathematically correct offset,
  // we force a sync. This guarantees all windows stay in sync, but prevents stuttering.
  useEffect(() => {
    if (videoRef.current && item?.type === 'video' && item.offset != null) {
      const vid = videoRef.current;
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
    return <div className="media-blank" aria-label="No media" />;
  }

  if (item.type === 'blank') {
    return <div className="media-blank" aria-label="Blank slot" />;
  }

  if (item.type === 'image') {
    return (
      <img
        src={item.url}
        alt="Scheduled media"
        className="media-content"
        onError={(e) => { e.currentTarget.className = 'media-error'; }}
      />
    );
  }

  if (item.type === 'video') {
    // key={item.url} forces React to unmount and remount the <video> element
    // whenever the URL changes, clearing the old video buffer.
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

/**
 * WindowPlayer is responsible for continuously showing the correct piece of media
 * for one display window. It ticks every 500ms, checks for a global sync override,
 * and falls back to local cycle-math computation.
 */
function WindowPlayer({ window: win, syncStatus }) {
  const [currentItem, setCurrentItem] = useState(win.currentItem ?? null);

  const mediaItemsRef = useRef(win.mediaItems ?? []);
  const cycleStartRef = useRef(win.cycleStartTime);
  const syncStatusRef = useRef(syncStatus);

  useEffect(() => {
    mediaItemsRef.current = win.mediaItems ?? [];
    cycleStartRef.current = win.cycleStartTime;
    syncStatusRef.current = syncStatus;
  }, [win.mediaItems, win.cycleStartTime, syncStatus]);

  useEffect(() => {
    const tick = () => {
      const status = syncStatusRef.current;

      if (status?.active) {
        // SYNC MODE
        const nowSeconds = Math.floor(Date.now() / 1000);
        setCurrentItem({
          type: status.mediaType,
          url:  status.mediaUrl,
          offset: Math.max(0, nowSeconds - status.startedAt),
        });
      } else {
        // NORMAL MODE: zero network calls, purely local arithmetic
        const nowSeconds = Math.floor(Date.now() / 1000);
        const elapsed    = computeElapsedInCycle(cycleStartRef.current, nowSeconds);
        const item       = computeCurrentItem(mediaItemsRef.current, elapsed);
        setCurrentItem(item);
      }
    };

    tick();
    const id = setInterval(tick, 500);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win.id]);

  return (
    <div className="window-player">
      {/* 16:9 viewport wrapper */}
      <div className="media-viewport">
        <MediaRenderer item={currentItem} />

        {syncStatus?.active && (
          <div className="badge badge-sync" title={`${syncStatus.remainingSeconds}s remaining`}>
            SYNCED — {syncStatus.remainingSeconds > 0 ? `${syncStatus.remainingSeconds}s left` : 'ending…'}
          </div>
        )}
      </div>

      <div className="player-footer">
        {currentItem ? (
          <span className="player-caption">
            {syncStatus?.active
              ? `Sync override: ${currentItem.type} — ${syncStatus.remainingSeconds}s remaining`
              : `Now playing: ${
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
