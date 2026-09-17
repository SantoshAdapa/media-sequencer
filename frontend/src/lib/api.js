// api.js
// Central API client for backend requests.

const BASE_URL = import.meta.env.VITE_API_BASE_URL || 
  (import.meta.env.PROD ? 'https://media-sequencer-api-sdad.onrender.com' : 'http://localhost:8080');

/**
 * Performs a fetch and throws an Error if the response is not OK.
 */
async function apiFetch(path, options = {}) {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });

  if (!res.ok) {
    let message = `Request to ${path} failed with status ${res.status}`;
    try {
      const body = await res.json();
      if (body.error) message = body.error;
    } catch (_) { /* ignore */ }
    throw new Error(message);
  }

  if (res.status === 204) return null;
  return res.json();
}

// ─── public API functions ────────────────────────────────────────────────────

/**
 * Fetches all display windows, each including its ordered media playlist
 * and a server-computed "currentItem" field.
 *
 * @returns {Promise<Array>} Array of window objects.
 */
export function getWindows() {
  return apiFetch('/windows');
}

/**
 * Appends a new media item to the end of a specific window's playlist.
 *
 * @param {number} windowId - The ID of the window to add the item to.
 * @param {{ type: string, url: string, duration_seconds: number }} body
 * @returns {Promise<Object>} The newly created MediaItem.
 */
export function addMedia(windowId, body) {
  return apiFetch(`/windows/${windowId}/media`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

/**
 * Removes a specific media item from a window's playlist.
 *
 * @param {number} windowId - The ID of the window.
 * @param {number} itemId - The ID of the media item to remove.
 * @returns {Promise<void>}
 */
export function deleteMedia(windowId, itemId) {
  return apiFetch(`/windows/${windowId}/media/${itemId}`, {
    method: 'DELETE',
  });
}

/**
 * Starts a global sync event that overrides every window's playlist.
 * All windows will show the given media simultaneously for `duration_seconds`.
 *
 * @param {{ media_url: string, media_type: string, duration_seconds: number }} body
 * @returns {Promise<Object>} The resulting SyncState record.
 */
export function triggerSync(body) {
  return apiFetch('/sync', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

/**
 * Checks whether a global sync event is currently active and, if so,
 * how many seconds remain before windows return to their normal playlists.
 *
 * @returns {Promise<{ active: boolean, mediaUrl: string, mediaType: string, remainingSeconds: number }>}
 */
export function getSyncStatus() {
  return apiFetch('/sync/status');
}
