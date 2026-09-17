package models

// Window represents a physical or virtual display screen that plays media in a continuous loop.
// CycleStartTime is the Unix timestamp (seconds since Jan 1 1970) that marks when this window's
// playlist started from the beginning. The front-end uses this to calculate which media item
// should be playing RIGHT NOW without having to ask the server every second.
type Window struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	CycleStartTime int64  `json:"cycleStartTime"` // Unix seconds: when this window's loop last reset to item 0
}

// MediaItem represents a single piece of content inside a window's playlist.
// Type must be one of three values:
//   - "image" — a still picture shown for a fixed number of seconds
//   - "video" — a video clip that plays for its defined duration
//   - "blank" — an intentional black/empty screen (used for pacing between segments)
//
// OrderIndex controls the position of this item within its window's playlist.
// Items are played in ascending order (0 first, then 1, then 2, and so on).
type MediaItem struct {
	ID              int    `json:"id"`
	WindowID        int    `json:"windowId"`        // Which screen this item belongs to
	Type            string `json:"type"`            // "image", "video", or "blank"
	URL             string `json:"url"`             // Public web address of the media file
	DurationSeconds int    `json:"durationSeconds"` // How many seconds this item occupies in the sequence
	OrderIndex      int    `json:"orderIndex"`      // Playlist position — 0 is first, played in ascending order
}

// SyncState describes a global takeover event where ONE specific piece of media
// is forced onto ALL windows simultaneously, overriding each window's normal loop.
//
// This struct maps to a SINGLETON database row (id always = 1). See db.go for why.
//
// When Active is false, every window plays its own independent loop as normal.
// When Active is true, every window ignores its own playlist and shows MediaURL instead.
type SyncState struct {
	Active          bool   `json:"active"`          // true = global takeover is happening right now
	MediaURL        string `json:"mediaUrl"`        // The URL of the media being shown on all screens
	MediaType       string `json:"mediaType"`       // "image", "video", or "blank"
	StartedAt       int64  `json:"startedAt"`       // Unix seconds: when the takeover began
	DurationSeconds int    `json:"durationSeconds"` // How long the takeover lasts before normal playback resumes
}
