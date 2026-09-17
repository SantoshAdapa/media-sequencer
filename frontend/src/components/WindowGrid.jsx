import { useState } from 'react';
import WindowPlayer from './WindowPlayer';
import EditModal from './EditModal';
import './WindowGrid.css';

/**
 * WindowGrid renders all display windows in a responsive CSS grid.
 * The design target is to fit 5 window tiles side-by-side on a standard desktop screen.
 *
 * Each "card" in the grid contains ONLY:
 *   - Window title bar
 *   - Live media player preview
 *
 * Clicking a card opens the EditModal to manage the playlist.
 */
function WindowGrid({ windows, onMediaAdded, syncStatus }) {
  const [editingWindowId, setEditingWindowId] = useState(null);

  if (windows.length === 0) {
    return <p className="status-msg">No windows found. Check that the backend seeded correctly.</p>;
  }

  const editingWindow = windows.find(w => w.id === editingWindowId);

  return (
    <section>
      <h2>Display Windows</h2>
      <div className="window-grid">
        {windows.map((win) => (
          <article key={win.id} className="window-card">
            {/* Window title bar */}
            <div className="window-card-header">
              <div className="window-title-group">
                <span className="window-name">{win.name}</span>
                <span className="window-meta">{win.mediaItems?.length ?? 0} items</span>
              </div>
              <button 
                className="btn-manage"
                onClick={() => setEditingWindowId(win.id)}
                aria-label={`Manage playlist for ${win.name}`}
              >
                Manage
              </button>
            </div>

            {/* Live media player */}
            <WindowPlayer window={win} syncStatus={syncStatus} />
          </article>
        ))}
      </div>

      {/* Edit Modal (renders conditionally) */}
      {editingWindowId && (
        <EditModal 
          windowData={editingWindow} 
          onClose={() => setEditingWindowId(null)} 
          onMediaAdded={onMediaAdded}
        />
      )}
    </section>
  );
}

export default WindowGrid;
