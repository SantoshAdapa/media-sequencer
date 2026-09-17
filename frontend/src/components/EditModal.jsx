import { useEffect } from 'react';
import PlaylistView from './PlaylistView';
import AddMediaForm from './AddMediaForm';
import './EditModal.css';

function EditModal({ windowData, onClose, onMediaAdded }) {
  // Close on Escape key
  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  if (!windowData) return null;

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal-content" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="modal-title">
            <span className="window-name">{windowData.name}</span>
            <span className="window-meta">Manage Playlist</span>
          </div>
          <button className="btn-close" onClick={onClose} aria-label="Close modal">×</button>
        </div>

        <div className="modal-body">
          {/* View to manage the items currently in the playlist */}
          <PlaylistView 
            windowId={windowData.id} 
            mediaItems={windowData.mediaItems} 
            onSuccess={onMediaAdded} 
          />

          {/* Form to add a new media item to this window */}
          <AddMediaForm 
            windowId={windowData.id} 
            onSuccess={onMediaAdded} 
          />
        </div>
      </div>
    </div>
  );
}

export default EditModal;
