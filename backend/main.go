package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/handler"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

func main() {
	fmt.Println("started-service")

	// Load and validate configuration before anything else, so a missing secret
	// is one clear message at startup rather than a panic on the first request.
	if err := config.Load(); err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	if err := store.InitElasticsearchBackend(); err != nil {
		log.Fatalf("elasticsearch: %v", err)
	}
	if err := store.InitGCSBackend(); err != nil {
		log.Fatalf("google cloud storage: %v", err)
	}

	// App Engine supplies PORT; default 8080 for local runs.
	addr := ":" + config.C.Port
	fmt.Printf("listening on %s\n", addr)

	// An explicit server rather than http.ListenAndServe, which has no timeouts
	// at all: a client can open a connection, dribble out a request header and
	// hold a goroutine indefinitely.
	//
	// ReadTimeout and WriteTimeout are deliberately left unset. A single read
	// deadline would have to cover a 32 MiB upload, which takes minutes on a slow
	// connection, and a write deadline would have to cover /generate, which waits
	// on DALL-E and then on a GCS upload. Any value tight enough to be useful
	// against a slow client would also cut off legitimate requests. ReadHeaderTimeout
	// is the one that addresses the actual attack, because a request header is
	// small and arrives quickly no matter how slow the link.
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler.InitRouter(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
