import { useState } from 'react';
import { deleteMedia } from '../lib/api';
import './PlaylistView.css';

/**
 * PlaylistView renders a small, unobtrusive list of media items in a window's playlist.
 * Each item has a delete button (×) that prompts for confirmation before removing it.
 *
 * Props:
 *   windowId   — the numeric ID of the window
 *   mediaItems — the array of media items in the window's playlist
 *   onSuccess  — callback fired when an item is deleted successfully (to refresh the window data)
 */
function PlaylistView({ windowId, mediaItems, onSuccess }) {
  const [deletingId, setDeletingId] = useState(null);

  if (!mediaItems || mediaItems.length === 0) {
    return <div className="playlist-view empty">Playlist is empty</div>;
  }

  const handleDelete = async (itemId) => {
    if (!window.confirm("Remove this item from the playlist?")) {
      return;
    }
    
    setDeletingId(itemId);
    try {
      await deleteMedia(windowId, itemId);
      onSuccess();
    } catch (err) {
      alert("Failed to remove item: " + err.message);
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className="playlist-view">
      <p className="playlist-label">Current playlist</p>
      <ul className="playlist-list">
        {mediaItems.map((item, idx) => (
          <li key={item.id} className="playlist-item">
            <span className="item-index">{idx + 1}.</span>
            <span className={`item-type type-${item.type}`}>{item.type}</span>
            <span className="item-duration">{item.durationSeconds}s</span>
            <span className="item-url" title={item.url}>
              {item.type === 'blank' ? 'Black screen' : (item.url || 'No URL')}
            </span>
            <button
              className="btn-delete"
              title="Remove item"
              onClick={() => handleDelete(item.id)}
              disabled={deletingId === item.id}
            >
              ×
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

export default PlaylistView;
