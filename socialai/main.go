package main

import (
    "fmt"
    "log"
    "net/http"

    "socialai/backend"
    "socialai/config"
    "socialai/handler"
)

func main() {
    fmt.Println("started-service")

    // Load and validate configuration before anything else, so a missing secret
    // is one clear message at startup rather than a panic on the first request.
    if err := config.Load(); err != nil {
        log.Fatalf("configuration error: %v", err)
    }

    if err := backend.InitElasticsearchBackend(); err != nil {
        log.Fatalf("elasticsearch: %v", err)
    }
    if err := backend.InitGCSBackend(); err != nil {
        log.Fatalf("google cloud storage: %v", err)
    }

    // App Engine supplies PORT; default 8080 for local runs.
    addr := ":" + config.C.Port
    fmt.Printf("listening on %s\n", addr)
    log.Fatal(http.ListenAndServe(addr, handler.InitRouter()))
}
