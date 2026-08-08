package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/service"
)

const (
	maxPromptLen    = 1000
	maxGenerateBody = 8 << 10
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
	log := logging.FromContext(r.Context())

	username, ok := usernameFromContext(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
		return
	}

	var req generateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxGenerateBody)).Decode(&req); err != nil {
		log.Debug("could not decode generate request", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "Cannot decode request")
		return
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "Prompt is required")
		return
	}
	if len(prompt) > maxPromptLen {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			fmt.Sprintf("Prompt must be at most %d characters", maxPromptLen))
		return
	}

	started := time.Now()
	post, err := service.GenerateAndSavePost(r.Context(), username, prompt)
	if err != nil {
		// The upstream message can carry quota and key details, so log it and
		// return something generic.
		log.Error("could not generate image",
			slog.String("username", username), slog.String("cause", err.Error()))
		writeError(w, r, http.StatusBadGateway, codeUpstreamFailed,
			"Failed to generate image, please try again")
		return
	}

	// Duration is worth recording here specifically: this is the one path that
	// spends money per call, so a latency change is also a cost signal.
	log.Info("image generated",
		slog.String("post_id", post.Id),
		slog.String("username", username),
		slog.Int64("duration_ms", time.Since(started).Milliseconds()))
	writeJSON(w, http.StatusOK, post)
}
