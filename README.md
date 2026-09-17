# Multi-Window Media Sequencer

A full-stack assignment project that continuously plays an ordered sequence of images, videos, and blank screens across multiple virtual display windows — kept in sync via a shared, clock-based playback model.

**Stack:** Go 1.27 (chi router, SQLite) • React (Vite) • Render (backend) • Vercel (frontend)

---

## 1. Project overview

The Media Sequencer manages **N display windows**, each with an independently configured playlist of media items (images, videos, or intentional blank gaps). Every item has a `duration_seconds` value. The system loops through each window's playlist continuously inside a fixed **5-hour super-cycle**.

A **global sync override** feature lets an operator broadcast a single media item to *all* windows simultaneously for a fixed number of seconds. Once the duration expires, every window automatically resumes its own playlist at the mathematically correct position.

## 2. Architecture

```text
┌────────────────────────────────────────────────────────────┐
│  Browser (React / Vite)                                    │
│                                                            │
│  ┌───────────┐  ┌───────────┐  ┌───────────┐  ...          │
│  │ Window 1  │  │ Window 2  │  │ Window 3  │               │
│  │  Player   │  │  Player   │  │  Player   │               │
│  └─────┬─────┘  └─────┬─────┘  └─────┬─────┘               │
│        │              │              │                     │
│  App polling loop (GET /sync/status every 500ms)           │
└────────┼───────────────────────────────────────────────────┘
         │ HTTP / JSON
┌────────▼───────────────────────────────────────────────────┐
│  Go backend (chi router)                                   │
│                                                            │
│  GET  /health          GET  /windows                       │
│  POST /windows/:id/media                                   │
│  POST /sync            GET  /sync/status                   │
│                                                            │
│  SQLite  ────────────────────────────► sequencer.db        │
└────────────────────────────────────────────────────────────┘
```

The system uses a React SPA frontend and a Go REST API backend backed by SQLite. State is fetched via HTTP polling; the frontend uses a central polling loop every 500ms to fetch the global sync state, passing the result down to the individual window players.

## 3. How playback works

Instead of running a "ticker" that decrements seconds (which drifts when a browser tab sleeps), the server establishes a single `cycleStartTime` timestamp (Unix epoch) for each window when it is first created.

Every 500 ms, the frontend calculates exactly how many seconds have elapsed since that cycle start time, wraps it around the 5-hour super-cycle using modulo math, and mathematically deduces which media item in the playlist "owns" the current second.

This means if a browser tab is sent to sleep by the OS and wakes up 10 minutes later, it doesn't need to "catch up" — it recalculates the current time and instantly skips to the correct media item.

## 4. Synchronization behavior

Synchronization relies on a deterministic timestamp-based solution:
- When a global sync is triggered, the backend establishes a common `startedAt` timestamp and stores it in the database.
- The frontend retrieves this sync state via the 500ms `/sync/status` polling loop.
- All windows override their local playlist and display the sync media.
- For video elements, the client calculates the exact playback position from the `startedAt` timestamp (`now - startedAt`). If the native `<video>` element drifts from this mathematical timeline by more than 1 second, it forces a chronological seek.
- Due to the nature of 500ms HTTP polling, network latency, browser scheduling, and native media buffering, synchronization is generally very tight but minor fraction-of-a-second variations can occur between windows.
- Once the sync expires, each window returns to its own playlist at the mathematically correct point.

## 5. Setup / local development

### Prerequisites
- Go 1.27.1+
- Node.js 20+
- npm 10+

### Step 1 — Start the backend
```bash
cd backend
go run ./cmd/server
# Server starts on http://localhost:8080
# On first run it creates sequencer.db and seeds 5 windows with sample playlists.
```

**Environment variables (all optional locally):**
- `PORT`: TCP port the server listens on (default `8080`).
- `DB_PATH`: Path to the SQLite database file (default `sequencer.db`).
- `FRONTEND_ORIGIN`: Allowed origin for CORS (default `http://localhost:5173`).

### Step 2 — Start the frontend
```bash
cd frontend
npm install
npm run dev
# Frontend starts on http://localhost:5173
```
Create a `.env` file in the frontend folder if your backend is running elsewhere:
`VITE_API_BASE_URL=http://localhost:8080`

## 6. API endpoints

All responses are `Content-Type: application/json`.

### `GET /health`
Liveness check used by monitoring systems.
**Response 200:** `{ "status": "ok" }`

### `GET /windows`
Returns all display windows, each with its ordered media playlist and a server-computed `currentItem` (to verify clock math).
**Response 200:** Array of window objects
```json
[
  {
    "id": 1,
    "name": "Window 1",
    "cycleStartTime": 1789630578,
    "mediaItems": [
      {
        "id": 1,
        "windowId": 1,
        "type": "image",
        "url": "https://picsum.photos/seed/w1item0/800/600",
        "durationSeconds": 10,
        "orderIndex": 0
      }
    ],
    "currentItem": { ... }
  }
]
```

### `POST /windows/{id}/media`
Appends a new media item to the end of a window's playlist.
**Body:**
```json
{
  "type": "image",
  "url": "https://example.com/image.jpg",
  "duration_seconds": 12
}
```
**Response 201:** The created item object.
**Errors:** `400` invalid body, `404` window not found, `500` internal error.

### `DELETE /windows/{id}/media/{itemId}`
Deletes a specific media item from a window's playlist and reorders the remaining items to close any gaps.
**Response 204:** No Content.
**Errors:** `404` window/item not found, `500` internal error.

### `POST /sync`
Starts a global sync event. All windows switch to displaying the specified media. Calling this overwrites any active sync.
**Body:**
```json
{
  "media_url": "https://example.com/video.mp4",
  "media_type": "video",
  "duration_seconds": 30
}
```
**Response 200:** The resulting sync state.
**Errors:** `400` invalid body, `500` internal error.

### `GET /sync/status`
Reports whether a global sync is active. If the sync has expired naturally, the server marks it inactive.
**Response 200:**
```json
{
  "active": true,
  "mediaUrl": "https://example.com/video.mp4",
  "mediaType": "video",
  "startedAt": 1789630578,
  "remainingSeconds": 22
}
```

## 7. Deployment

The backend is deployed as a Dockerized Web Service on [Render](https://render.com). The frontend is deployed as a static Single Page Application on [Vercel](https://vercel.com). Deployments are triggered via GitHub webhooks (standard push deployments).

**Live URLs:**
- Frontend: https://frontend-eta-seven-28.vercel.app
- Backend API: https://media-sequencer-api-sdad.onrender.com

## 8. Assumptions

- **Cycle length:** A fixed 5-hour super-cycle is assumed for playlist repetition.
- **Video looping:** If a video file is shorter than its `duration_seconds` slot, it loops continuously until the slot ends.
- **No authentication:** All endpoints are completely open for this assignment.
- **Single-instance backend:** SQLite does not support multiple concurrent writers across horizontal scaling. The free tier of Render correctly restricts this to a single instance.

## 9. Trade-offs / limitations

- **HTTP polling vs WebSockets:** WebSockets would offer slightly lower latency and avoid 500ms polling, but require connection management and more complex state tracking. Polling is simpler, deterministic, and sufficient for the assignment constraints.
- **Audio Overlap:** If multiple windows are opened on the same physical machine, fraction-of-a-second delays between polling cycles may cause audio phasing/echo (videos are muted by default).
- **Network delay jitter:** The frontend assumes the client clock is relatively close to the server clock. Massive client-side clock skew will affect synchronization.
- **Ephemeral Persistence:** The SQLite database provides local, application-level persistence. However, because the backend is deployed on Render's free tier, the disk is ephemeral. The database wipes and resets to the default seed data whenever the instance spins down due to inactivity. Upgrading to a paid tier with a persistent disk or switching to managed PostgreSQL is required for production-grade persistence.
