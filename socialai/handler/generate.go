package handler

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"

    "socialai/service"
)

const (
    maxPromptLen      = 1000
    maxGenerateBody   = 8 << 10
)

type generateRequest struct {
    Prompt string `json:"prompt"`
}

// generateHandler creates an image from a prompt and stores it as a post.
//
// The OpenAI call lives here rather than in the browser. Previously the frontend
// held the API key (inlined into the bundle by Create React App) and called
// OpenAI directly, which published the key to every visitor.
func generateHandler(w http.ResponseWriter, r *http.Request) {
    fmt.Println("Received one generate request")

    username, ok := usernameFromContext(r)
    if !ok {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    var req generateRequest
    if err := json.NewDecoder(io.LimitReader(r.Body, maxGenerateBody)).Decode(&req); err != nil {
        http.Error(w, "Cannot decode request", http.StatusBadRequest)
        fmt.Printf("Cannot decode generate request %v\n", err)
        return
    }

    prompt := strings.TrimSpace(req.Prompt)
    if prompt == "" {
        http.Error(w, "Prompt is required", http.StatusBadRequest)
        return
    }
    if len(prompt) > maxPromptLen {
        http.Error(w,
            fmt.Sprintf("Prompt must be at most %d characters", maxPromptLen),
            http.StatusBadRequest)
        return
    }

    post, err := service.GenerateAndSavePost(r.Context(), username, prompt)
    if err != nil {
        // The upstream message can carry quota and key details, so log it and
        // return something generic.
        fmt.Printf("Failed to generate image: %v\n", err)
        http.Error(w, "Failed to generate image, please try again", http.StatusBadGateway)
        return
    }

    fmt.Printf("Generated post %s for %s\n", post.Id, username)
    writeJSON(w, http.StatusOK, post)
}
