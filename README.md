# Multi-Window Media Sequencer

A full-stack assignment project that continuously plays an ordered sequence of images,
videos, and blank screens across multiple virtual display windows — all in sync via a
shared, drift-free, clock-based playback model.

**Stack:** Go (chi router, SQLite) · React (Vite) · Fly.io (backend) · Vercel (frontend)

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Local Development Setup](#2-local-development-setup)
3. [Sync Design: Why Computed Time, Not a Running Timer](#3-sync-design-why-computed-time-not-a-running-timer)
4. [API Reference](#4-api-reference)
5. [Deployment](#5-deployment)
   - [Backend — Fly.io](#backend--flyio)
   - [Frontend — Vercel](#frontend--vercel)
6. [Assumptions & Trade-offs](#6-assumptions--trade-offs)
7. [Live URLs](#7-live-urls)

---

## 1. Project Overview

The Media Sequencer manages **N display windows**, each with an independently configured
playlist of media items (images, videos, or intentional blank gaps). Every item has a
`duration_seconds` value. The system loops through each window's playlist continuously
inside a fixed **5-hour super-cycle**.

A **global sync override** feature lets an operator broadcast a single media item to
*all* windows simultaneously for a fixed number of seconds. Once the duration expires,
every window automatically resumes its own playlist at the mathematically correct position.

```
┌─────────────────────────────────────────────────────────┐
│  Browser (React / Vite)                                 │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  …           │
│  │ Window 1 │  │ Window 2 │  │ Window 3 │              │
│  │  Player  │  │  Player  │  │  Player  │              │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘              │
│       │              │              │                    │
│  every 500 ms: GET /sync/status (shared server call)    │
│  if inactive: compute item locally via cycleMath.js     │
└───────────────────────────┬─────────────────────────────┘
                            │ HTTP / JSON
┌───────────────────────────▼─────────────────────────────┐
│  Go backend (chi router, port 8080)                     │
│                                                         │
│  GET  /health          GET  /windows                    │
│  POST /windows/:id/media                                │
│  POST /sync            GET  /sync/status                │
│                                                         │
│  SQLite  ──────────────────────────────── sequencer.db  │
└─────────────────────────────────────────────────────────┘
```

---

## 2. Local Development Setup

### Prerequisites

| Tool | Version tested |
|------|---------------|
| Go   | 1.22+         |
| Node | 20+           |
| npm  | 10+           |

### Step 1 — Start the backend

```bash
cd backend
go run ./cmd/server
# Server starts on http://localhost:8080
# On first run it creates sequencer.db and seeds 5 windows with sample playlists.
```

**Environment variables (all optional locally):**

| Variable | Default         | Purpose |
|----------|-----------------|---------|
| `PORT`   | `8080`          | TCP port the server listens on |
| `DB_PATH`| `sequencer.db`  | Path to the SQLite database file |

### Step 2 — Start the frontend

```bash
cd frontend

# Create your local env file (one-time setup):
cp .env.example .env.local
# .env.local already contains: VITE_API_BASE_URL=http://localhost:8080

npm install   # only needed once
npm run dev
# Dev server starts on http://localhost:5173
```

Both services must be running at the same time. The frontend reads the backend URL
exclusively from `VITE_API_BASE_URL` — no hardcoded addresses anywhere in the code.

---

## 3. Sync Design: Why Computed Time, Not a Running Timer

### The problem with a running timer

A traditional media player keeps a counter: *"this window is at 3 m 42 s"*. That
counter resets to zero if the server restarts or the browser refreshes, leaving every
window out of sync and requiring complex state recovery logic.

### The solution: a fixed `cycleStartTime`

Each window stores a single Unix timestamp — `cycleStartTime` — that records when its
playlist was considered to start from item 0. To know what *any* window should be
showing **right now**, any device anywhere needs only arithmetic:

```
elapsed_in_cycle = (now − cycleStartTime) mod 18000      # fold into 5-hour window
position_in_list = elapsed_in_cycle mod total_playlist_s  # wrap within the playlist
→ walk items until you find the one that owns `position_in_list`
```

This same formula is implemented twice — once in Go (`/internal/sync/sync.go`) and
once in JavaScript (`/frontend/src/lib/cycleMath.js`) — and both are covered by
unit tests to ensure they agree.

### Three concrete benefits

| Benefit | Why it matters |
|---------|---------------|
| **Restart-safe** | The server can reboot, the browser can refresh. Both recalculate from `cycleStartTime` and resume in exactly the right place. |
| **No drift** | A running timer accumulates tiny tick errors. Arithmetic against a wall-clock timestamp is always exact. |
| **Trivially syncable** | Every window's state is a pure function of `(cycleStartTime, now)`. The frontend only needs `cycleStartTime` once; after that it computes position forever without polling for it. |

### Global sync override

When an operator posts to `POST /sync`, the server overwrites a **singleton row**
(`id = 1`) in the `sync_state` table with `active = true`, the chosen media URL,
and a `started_at` timestamp. Every window's player polls `GET /sync/status` every
500 ms. When `active` is true, the window shows the sync media instead of its own
playlist item. When `started_at + duration_seconds ≤ now`, the server marks the row
inactive on the very next `GET /sync/status` call (lazy expiry), and every window
returns to its computed playlist position.

The singleton design (`id` fixed to 1 with a `CHECK(id = 1)` constraint) makes race
conditions structurally impossible: there is literally only one row to read or update.

---

## 4. API Reference

All responses are `Content-Type: application/json`. All endpoints support CORS from
any origin.

---

### `GET /health`

Liveness check used by monitoring systems and Fly.io health probes.

**Response 200**
```json
{ "status": "ok" }
```

---

### `GET /windows`

Returns all display windows, each with its ordered media playlist and a
server-computed `currentItem` (the item the server believes should be playing now).

**Response 200** — array of window objects
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
    "currentItem": {
      "id": 1,
      "windowId": 1,
      "type": "image",
      "url": "https://picsum.photos/seed/w1item0/800/600",
      "durationSeconds": 10,
      "orderIndex": 0
    }
  }
]
```

> `currentItem` will be `null` if the window's playlist is empty.

---

### `POST /windows/{id}/media`

Appends a new media item to the end of the specified window's playlist.

**URL parameter:** `id` — integer window ID

**Request body**
```json
{
  "type": "image",
  "url": "https://picsum.photos/seed/example/800/600",
  "duration_seconds": 12
}
```

| Field | Type | Allowed values |
|-------|------|----------------|
| `type` | string | `"image"`, `"video"`, `"blank"` |
| `url`  | string | Any URL. May be empty string for `"blank"` type. |
| `duration_seconds` | integer | Must be ≥ 1 |

**Response 201** — the created item
```json
{
  "id": 29,
  "windowId": 1,
  "type": "image",
  "url": "https://picsum.photos/seed/example/800/600",
  "durationSeconds": 12,
  "orderIndex": 6
}
```

**Errors:** `400` invalid body or type · `404` window not found · `500` DB error

---

### `POST /sync`

Starts a global sync event. All windows immediately switch to displaying the
specified media item for `duration_seconds`, then return to their own playlists.
Calling this endpoint while a sync is already active overwrites it.

**Request body**
```json
{
  "media_url": "https://www.w3schools.com/html/mov_bbb.mp4",
  "media_type": "video",
  "duration_seconds": 30
}
```

| Field | Type | Allowed values |
|-------|------|----------------|
| `media_url` | string | Required, non-empty URL |
| `media_type` | string | `"image"` or `"video"` (blank not allowed for global override) |
| `duration_seconds` | integer | Must be ≥ 1 |

**Response 200** — the resulting sync state
```json
{
  "active": true,
  "mediaUrl": "https://www.w3schools.com/html/mov_bbb.mp4",
  "mediaType": "video",
  "startedAt": 1789630584,
  "durationSeconds": 30
}
```

**Errors:** `400` invalid body or type · `500` DB error

---

### `GET /sync/status`

Reports whether a global sync is currently active and how many seconds remain.
As a side effect, if the sync has expired, the server marks it inactive in the DB.

**Response 200**
```json
{
  "active": true,
  "mediaUrl": "https://www.w3schools.com/html/mov_bbb.mp4",
  "mediaType": "video",
  "remainingSeconds": 22
}
```

When inactive:
```json
{
  "active": false,
  "mediaUrl": "",
  "mediaType": "",
  "remainingSeconds": 0
}
```

---

## 5. Deployment

### Backend — Fly.io

The backend ships as a self-contained Docker image (multi-stage build, final stage is
Alpine Linux). SQLite data is stored on a Fly.io **persistent volume** so it survives
redeploys and machine restarts.

#### First-time setup

```bash
# 1. Authenticate
flyctl auth login

# 2. Move into the backend directory — fly commands must run from here
cd backend

# 3. Create a new Fly.io app.
#    When prompted for a Postgres database, say NO — we use SQLite.
#    When prompted for a deploy, say NO — we set up the volume first.
flyctl launch --name media-sequencer-api --no-deploy

# 4. Create the persistent volume in the same region as the app.
#    1 GB is more than sufficient for SQLite + typical usage.
flyctl volumes create sequencer_data --region sin --size 1

# 5. Deploy the first build
flyctl deploy

# 6. Confirm the app is live
flyctl open
# or:
curl https://media-sequencer-api.fly.dev/health
```

#### Subsequent deploys

```bash
cd backend
flyctl deploy
```

#### What `fly.toml` configures

| Setting | Value | Reason |
|---------|-------|--------|
| `PORT` env | `8080` | Matches `internal_port` |
| `DB_PATH` env | `/data/sequencer.db` | Points into the mounted volume |
| Volume mount | `/data` | Persistent disk for SQLite file |
| Health check | `GET /health` every 15 s | Fly replaces unhealthy machines automatically |
| `min_machines_running` | `1` | Avoid cold-starts for display windows |

---

### Frontend — Vercel

Vercel auto-detects Vite projects (framework preset, build command, output directory).
The only manual step is providing the backend URL as an environment variable.

#### First-time setup

```bash
# 1. Authenticate
vercel login

# 2. Move into the frontend directory
cd frontend

# 3. Add the backend URL as a production environment variable.
#    Replace the URL with your actual Fly.io app URL.
vercel env add VITE_API_BASE_URL production
# When prompted, enter: https://media-sequencer-api.fly.dev

# 4. Deploy to production
vercel --prod
```

> **Why is `VITE_API_BASE_URL` set in Vercel and not in `.env.local`?**
> Vite bakes environment variables into the JavaScript bundle at build time.
> `.env.local` is for local development only and is `.gitignore`-d.
> Vercel injects `VITE_API_BASE_URL` into the build environment when it runs
> `npm run build` on its servers, so the deployed bundle points at the right URL.

#### Subsequent deploys

```bash
cd frontend
vercel --prod
# Vercel re-reads the environment variable from its dashboard automatically.
```

#### What `vercel.json` configures

The `vercel.json` in `/frontend` contains a single rewrite rule:
```json
{ "source": "/(.*)", "destination": "/index.html" }
```
This ensures all URLs serve `index.html` (required for single-page applications).

---

## 6. Assumptions & Trade-offs

| Topic | Decision | Reasoning |
|-------|----------|-----------|
| **Cycle length** | Fixed 5-hour super-cycle (18 000 s) | The playlist repeats within this window regardless of its actual total duration. A 3-minute playlist plays ~100 times inside one 5-hour cycle. |
| **Sync mechanism** | HTTP polling every 500 ms | WebSockets would offer lower latency but require significant extra complexity (connection management, reconnection, server-side goroutine per client). Polling at 500 ms is imperceptible to the human eye for a signage application and appropriate for a take-home assignment scope. |
| **Database** | SQLite with `modernc.org/sqlite` (pure-Go driver) | Zero external dependencies, no database server to operate, works perfectly inside Docker without CGO. The data volume is tiny (< 1 MB for typical usage). Upgrade to PostgreSQL when you need concurrent writers or horizontal scaling. |
| **Video looping within slot** | `<video loop>` attribute | If a video file is shorter than its `duration_seconds` slot, it loops continuously until the slot ends. If the file is longer, the math will cut to the next item before the video finishes. This is an acceptable approximation for a signage assignment. |
| **No authentication** | All endpoints are open | Production deployment should add at minimum an API key for the `POST /sync` and `POST /windows/{id}/media` endpoints. |
| **Single-instance SQLite** | One machine on Fly.io | SQLite does not support multiple concurrent writers across machines. `min_machines_running = 1` in `fly.toml` ensures Fly.io never runs more than one instance. Scale horizontally by migrating to PostgreSQL. |
| **Seed data** | Inserted once on empty DB | The seed check is `COUNT(*) FROM windows = 0`. Wiping the DB and restarting the server will re-seed automatically. |

---

## 7. Live URLs

> Fill these in after completing deployment.

| Service | URL |
|---------|-----|
| Backend (Fly.io) | `https://media-sequencer-api.fly.dev` |
| Frontend (Vercel) | `https://your-project.vercel.app` |
| Health check | `https://media-sequencer-api.fly.dev/health` |
