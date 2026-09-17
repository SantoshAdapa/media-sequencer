package main

import (
	"log"
	"net/http"
	"os"

	"github.com/SantoshAdapa/media-sequencer/backend/internal/db"
	"github.com/SantoshAdapa/media-sequencer/backend/internal/handlers"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// main is the entry point of the Media Sequencer server.
// It runs in order: read config → connect to database → build router → start server.
func main() {
	// Read the network port from the environment so the server can be deployed
	// to cloud platforms (like Cloud Run or Heroku) that assign ports dynamically.
	// If no PORT is set, we default to 8080 for local development.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// DB_PATH lets the deployment environment specify where the SQLite file should live.
	// On Render we might point this at a persistent volume (e.g. /data/sequencer.db) 
	// on paid tiers so data survives redeploys. Locally it defaults to the current 
	// directory for convenience.
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "sequencer.db"
	}

	// Open the SQLite database file. If it doesn't exist, this call creates it.
	// It also runs schema migrations and inserts seed data the very first time.
	database, err := db.InitDB(dbPath)
	if err != nil {
		// Without a database the server cannot function at all, so we stop immediately.
		log.Fatalf("Failed to initialize database: %v", err)
	}
	// Guarantee the database file is properly closed when the process exits.
	defer database.Close()

	// Create a fresh router. The router inspects every incoming request URL and
	// dispatches it to the correct handler function.
	router := chi.NewRouter()

	// ── Global middleware ───────────────────────────────────────────────────
	// Middleware runs for EVERY request, before the specific handler does.

	// Logger prints one line to the console per request (method, path, status, duration).
	// This makes it easy to watch traffic in real time during development.
	router.Use(middleware.Logger)

	// Recoverer catches any unexpected crashes (panics) inside a handler and
	// converts them into a 500 response instead of killing the whole server process.
	router.Use(middleware.Recoverer)

	// Register all application routes (and the CORS middleware that lives there).
	// We wire up: GET /health, GET /windows, POST /windows/{id}/media,
	//             POST /sync, GET /sync/status
	handlers.SetupRoutes(router, database)

	log.Printf("Media Sequencer server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatalf("Server stopped unexpectedly: %v", err)
	}
}
