import { useState } from 'react';
import { triggerSync } from '../lib/api';
import { validateImage, validateVideo } from '../lib/mediaValidator';
import './SyncTrigger.css';

/**
 * SyncTrigger is a global control panel that lets an operator broadcast
 * a single media item to ALL display windows simultaneously.
 *
 * When a sync is triggered, every WindowPlayer on the page will detect it
 * on its next 500 ms polling tick and switch to showing the sync media,
 * ignoring its own playlist for the specified duration.
 *
 * Once the duration expires, windows automatically return to their normal
 * individual playlists — no manual intervention required.
 */
function SyncTrigger() {
  const [mediaUrl,  setMediaUrl]  = useState('');
  const [mediaType, setMediaType] = useState('image');
  const [duration,  setDuration]  = useState(30);
  const [status,    setStatus]    = useState('idle'); // 'idle' | 'loading' | 'success' | 'error'
  const [errMsg,    setErrMsg]    = useState('');

  const handleSubmit = async (e) => {
    e.preventDefault();
    setStatus('loading');
    setErrMsg('');

    if (mediaType === 'image') {
      const isValid = await validateImage(mediaUrl);
      if (!isValid) {
        setErrMsg('Unable to load image. Check the URL.');
        setStatus('error');
        return;
      }
    } else if (mediaType === 'video') {
      const isValid = await validateVideo(mediaUrl);
      if (!isValid) {
        setErrMsg('Unable to load video. Check the URL.');
        setStatus('error');
        return;
      }
    }

    try {
      await triggerSync({
        media_url:        mediaUrl,
        media_type:       mediaType,
        duration_seconds: Number(duration),
      });
      setStatus('success');
      // Reset to idle after 4 seconds so the success state doesn't overstay its welcome.
      setTimeout(() => setStatus('idle'), 4000);
    } catch (err) {
      setErrMsg(err.message);
      setStatus('error');
    }
  };

  return (
    <section className="sync-trigger">
      <div className="sync-header">
        <h2>Global Sync Override</h2>
        <p className="sync-desc">
          This will immediately interrupt every window below and show the same item
          on all of them for the number of seconds you specify. After that time,
          each window automatically returns to its own playlist. Use this to push
          a single announcement or highlight to all screens at once.
        </p>
      </div>

      <form className="sync-form" onSubmit={handleSubmit}>
        <div className="sync-fields">
          {/* Type — blank doesn't make sense for a takeover event */}
          <label>
            Media type
            <select value={mediaType} onChange={(e) => setMediaType(e.target.value)}>
              <option value="image">Image</option>
              <option value="video">Video</option>
            </select>
          </label>

          {/* URL of the media to broadcast */}
          <label className="sync-url-label">
            Media URL
            <input
              type="url"
              value={mediaUrl}
              placeholder="e.g. https://picsum.photos/800/600"
              onChange={(e) => setMediaUrl(e.target.value)}
              required
            />
          </label>

          {/* How long the takeover should last */}
          <label>
            Duration (seconds)
            <input
              type="number"
              value={duration}
              min={1}
              placeholder="e.g. 30"
              onChange={(e) => setDuration(e.target.value)}
              required
            />
          </label>
        </div>

        <div className="sync-footer">
          <button type="submit" className="sync-btn" disabled={status === 'loading'}>
            {status === 'loading' ? 'Triggering…' : 'Trigger Sync on All Windows'}
          </button>

          {status === 'success' && (
            <span className="msg-success">
              Sync triggered — all windows will update within half a second.
            </span>
          )}
          {status === 'error' && (
            <span className="msg-error">{errMsg}</span>
          )}
        </div>
      </form>
    </section>
  );
}

export default SyncTrigger;
