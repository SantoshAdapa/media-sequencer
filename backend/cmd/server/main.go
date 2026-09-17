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

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "sequencer.db"
	}

	database, err := db.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	handlers.SetupRoutes(router, database)

	log.Printf("Media Sequencer server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatalf("Server stopped unexpectedly: %v", err)
	}
}
