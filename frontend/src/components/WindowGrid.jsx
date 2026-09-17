import WindowPlayer from './WindowPlayer';
import AddMediaForm from './AddMediaForm';
import './WindowGrid.css';

/**
 * WindowGrid renders all display windows in a responsive CSS grid.
 * On a wide screen you get 3 columns; it collapses to 2 then 1 on smaller screens.
 *
 * Each "card" in the grid contains:
 *   - A WindowPlayer that shows the live-computed current media item.
 *   - An AddMediaForm so the user can append items to that window's playlist.
 */
function WindowGrid({ windows, onMediaAdded }) {
  if (windows.length === 0) {
    return <p className="status-msg">No windows found. Check that the backend seeded correctly.</p>;
  }

  return (
    <section>
      <h2>Display Windows</h2>
      <div className="window-grid">
        {windows.map((win) => (
          <div key={win.id} className="window-card">
            {/* Window title bar */}
            <div className="window-card-header">
              <span className="window-name">{win.name}</span>
              <span className="window-meta">{win.mediaItems?.length ?? 0} items</span>
            </div>

            {/* Live media player */}
            <WindowPlayer window={win} />

            {/* Form to add a new media item to this window */}
            <AddMediaForm windowId={win.id} onSuccess={onMediaAdded} />
          </div>
        ))}
      </div>
    </section>
  );
}

export default WindowGrid;
