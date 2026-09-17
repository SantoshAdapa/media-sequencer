import { useState } from 'react';
import { addMedia } from '../lib/api';
import './AddMediaForm.css';

/**
 * AddMediaForm renders a compact form beneath each window player.
 * It lets the user append a new media item (image, video, or blank pause)
 * to that window's playlist. On success it calls `onSuccess` so the parent
 * can refetch the windows list and reflect the change in the UI immediately.
 *
 * Props:
 *   windowId  — the numeric ID of the window to add the item to.
 *   onSuccess — callback fired after the item is successfully saved.
 */
function AddMediaForm({ windowId, onSuccess }) {
  const [type,     setType]     = useState('image');
  const [url,      setUrl]      = useState('');
  const [duration, setDuration] = useState(10);
  const [status,   setStatus]   = useState('idle'); // 'idle' | 'loading' | 'success' | 'error'
  const [errMsg,   setErrMsg]   = useState('');

  const handleSubmit = async (e) => {
    e.preventDefault();
    setStatus('loading');
    setErrMsg('');

    try {
      await addMedia(windowId, {
        type,
        // Blank items have no meaningful URL; we send an empty string.
        url: type === 'blank' ? '' : url,
        duration_seconds: Number(duration),
      });

      // Clear the URL field on success (keep type & duration as sensible defaults).
      setUrl('');
      setStatus('success');

      // Notify the parent so the windows list is refreshed.
      onSuccess();

      // Reset the success message after 2 seconds so it doesn't linger.
      setTimeout(() => setStatus('idle'), 2000);
    } catch (err) {
      setErrMsg(err.message);
      setStatus('error');
    }
  };

  return (
    <form className="add-media-form" onSubmit={handleSubmit}>
      <h4>Add to playlist</h4>

      <div className="amf-fields">
        {/* Type selector */}
        <label>
          Type
          <select value={type} onChange={(e) => setType(e.target.value)}>
            <option value="image">Image</option>
            <option value="video">Video</option>
            <option value="blank">Blank</option>
          </select>
        </label>

        {/* URL input — hidden for "blank" since it needs no URL */}
        {type !== 'blank' && (
          <label>
            URL
            <input
              type="url"
              value={url}
              placeholder="https://…"
              onChange={(e) => setUrl(e.target.value)}
              required
            />
          </label>
        )}

        {/* Duration in seconds */}
        <label>
          Duration (s)
          <input
            type="number"
            value={duration}
            min={1}
            onChange={(e) => setDuration(e.target.value)}
            required
          />
        </label>
      </div>

      <div className="amf-footer">
        <button type="submit" disabled={status === 'loading'}>
          {status === 'loading' ? 'Saving…' : '+ Add'}
        </button>

        {status === 'success' && <span className="msg-success">✓ Added</span>}
        {status === 'error'   && <span className="msg-error">{errMsg}</span>}
      </div>
    </form>
  );
}

export default AddMediaForm;
