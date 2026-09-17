import { useState, useEffect, useCallback } from 'react';
import { getWindows, getSyncStatus } from './lib/api';
import WindowGrid from './components/WindowGrid';
import SyncTrigger from './components/SyncTrigger';
import './index.css';

/**
 * App is the root component of the Media Sequencer frontend.
 *
 * On first load it fetches the full list of display windows (including each
 * window's playlist and cycle_start_time) from the backend and stores them in
 * state. That state is then passed down to child components for rendering.
 *
 * fetchWindows is passed as a callback to child components so they can
 * trigger a re-fetch whenever something changes (e.g., a new media item is added).
 */
function App() {
  const [windows, setWindows]   = useState([]);
  const [loading, setLoading]   = useState(true);
  const [error,   setError]     = useState(null);
  const [syncStatus, setSyncStatus] = useState(null);

  /**
   * fetchWindows talks to the backend and refreshes our local list of windows.
   * useCallback ensures the function reference is stable so it can safely be
   * listed in dependency arrays of useEffect hooks in child components.
   */
  const fetchWindows = useCallback(async () => {
    try {
      const data = await getWindows();
      setWindows(data ?? []);
      setError(null);
    } catch (err) {
      setError('Could not reach the backend. Make sure the Go server is running on port 8080.');
    } finally {
      setLoading(false);
    }
  }, []);

  // Fetch windows once when the page first loads.
  useEffect(() => {
    fetchWindows();
  }, [fetchWindows]);

  // Global Sync Polling Loop:
  // Instead of every WindowPlayer polling independently (causing N requests per 500ms),
  // we poll once here and pass the state down.
  useEffect(() => {
    let lastStatusStr = '';
    const tick = async () => {
      try {
        const status = await getSyncStatus();
        const statusStr = JSON.stringify(status);
        // Only update state (and trigger re-renders) if the status actually changed.
        // During an active sync, remainingSeconds ticks down, so this updates regularly.
        // During normal playback, this stays static, saving massive amounts of CPU/renders.
        if (statusStr !== lastStatusStr) {
          lastStatusStr = statusStr;
          setSyncStatus(status);
        }
      } catch (_) {
        if (lastStatusStr !== 'null') {
          lastStatusStr = 'null';
          setSyncStatus(null);
        }
      }
    };

    tick();
    const id = setInterval(tick, 500);
    return () => clearInterval(id);
  }, []);

  if (loading) return <p className="status-msg">Loading windows…</p>;
  if (error)   return <p className="status-msg error">{error}</p>;

  return (
    <div className="app">
      <header className="app-header">
        <div className="app-title-group">
          <h1>Multi-Window Media Sequencer</h1>
          <p className="app-subtitle">
            Manage independent media playlists across multiple display windows,
            with the ability to broadcast a single item to all screens at once.
          </p>
        </div>
      </header>

      <main className="app-main">
        {/* Global sync control — affects all windows at once */}
        <SyncTrigger />

        {/* Grid of individual window players */}
        <WindowGrid windows={windows} onMediaAdded={fetchWindows} syncStatus={syncStatus} />
      </main>
    </div>
  );
}

export default App;
