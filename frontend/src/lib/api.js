// api.js
// Centralises every network call the frontend makes.
//
// The backend base URL is read from the environment variable VITE_API_BASE_URL.
// Set it in a .env.local file for local development, or in your hosting
// provider's dashboard for production deployments. This means you never
// need to change a line of code when switching between environments.

/** The root URL of the Go backend, e.g. "http://localhost:8080". */
const BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080';

// ─── helper ─────────────────────────────────────────────────────────────────

/**
 * Performs a fetch and throws a descriptive Error if the response is not OK.
 * This avoids repeating error-handling boilerplate in every function below.
 */
async function apiFetch(path, options = {}) {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });

  if (!res.ok) {
    // Try to read the server's error message; fall back to a generic one.
    let message = `Request to ${path} failed with status ${res.status}`;
    try {
      const body = await res.json();
      if (body.error) message = body.error;
    } catch (_) { /* ignore JSON parse failure */ }
    throw new Error(message);
  }

  // 204 No Content responses have no body to parse.
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
