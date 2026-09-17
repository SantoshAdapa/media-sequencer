import { useState, useEffect, useCallback } from 'react';
import { getWindows } from './lib/api';
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

  if (loading) return <p className="status-msg">Loading windows…</p>;
  if (error)   return <p className="status-msg error">{error}</p>;

  return (
    <div className="app">
      <header className="app-header">
        <h1>🎬 Multi-Window Media Sequencer</h1>
      </header>

      <main className="app-main">
        {/* Global sync control — affects all windows at once */}
        <SyncTrigger />

        {/* Grid of individual window players */}
        <WindowGrid windows={windows} onMediaAdded={fetchWindows} />
      </main>
    </div>
  );
}

export default App;
