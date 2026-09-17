import { useState, useEffect, useCallback } from 'react';
import { getWindows, getSyncStatus } from './lib/api';
import WindowGrid from './components/WindowGrid';
import SyncTrigger from './components/SyncTrigger';
import './index.css';

/**
 * App is the root component of the Media Sequencer frontend.
 * Manages the global sync polling loop and passes state down.
 */
function App() {
  const [windows, setWindows]   = useState([]);
  const [loading, setLoading]   = useState(true);
  const [error,   setError]     = useState(null);
  const [syncStatus, setSyncStatus] = useState(null);

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

  useEffect(() => {
    fetchWindows();
  }, [fetchWindows]);

  // Global Sync Polling Loop:
  // Polls the sync state once centrally to avoid N concurrent requests per 500ms
  // from individual WindowPlayers.
  useEffect(() => {
    let lastStatusStr = '';
    const tick = async () => {
      try {
        const status = await getSyncStatus();
        const statusStr = JSON.stringify(status);
        // Only update state if the status actually changed to minimize re-renders.
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
