package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/media"
	"github.com/luc1ferxx/neural-canvas/backend/model"

	"github.com/luc1ferxx/neural-canvas/backend/store"

	"github.com/pborman/uuid"
)

const (
	openAIImageEndpoint = "https://api.openai.com/v1/images/generations"
	openAIModel         = "dall-e-3"
	openAIImageSize     = "1024x1024"

	// generateTimeout covers the DALL-E call, which routinely takes 10-30s.
	generateTimeout = 120 * time.Second
	// downloadTimeout covers fetching the rendered image from OpenAI's CDN.
	downloadTimeout = 60 * time.Second
)

var generateClient = &http.Client{Timeout: generateTimeout}

type openAIImageRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

type openAIImageResponse struct {
	Data []struct {
		URL string `json:"url"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// GenerateAndSavePost generates an image from prompt, stores it in GCS, indexes
// the post, and returns it.
//
// This runs server-side so the OpenAI key never reaches a browser. It also
// removes the need for the old frontend workaround that proxied OpenAI's CDN
// through /api to dodge CORS: the download happens here, where CORS does not
// apply.
func GenerateAndSavePost(ctx context.Context, username, prompt string) (*model.Post, error) {
	imageURL, err := generateImageURL(ctx, prompt)
	if err != nil {
		return nil, err
	}

	imageBytes, err := downloadImage(ctx, imageURL)
	if err != nil {
		return nil, err
	}

	// Validate what OpenAI returned against the same allowlist as user uploads,
	// rather than trusting the source.
	postType, mime, body, err := media.Sniff(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("generated image failed validation: %w", err)
	}

	post := &model.Post{
		Id:      uuid.New(),
		User:    username,
		Message: prompt,
		Type:    postType,
	}

	url, err := store.GCSBackend.SaveToGCS(body, post.Id, mime)
	if err != nil {
		return nil, fmt.Errorf("save generated image: %w", err)
	}
	post.Url = url

	if err := store.ESBackend.SaveToES(post, constants.POST_INDEX, post.Id); err != nil {
		return nil, fmt.Errorf("index generated post: %w", err)
	}

	return post, nil
}

func generateImageURL(ctx context.Context, prompt string) (string, error) {
	payload, err := json.Marshal(openAIImageRequest{
		Model:  openAIModel,
		Prompt: prompt,
		N:      1,
		Size:   openAIImageSize,
	})
	if err != nil {
		return "", fmt.Errorf("encode openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIImageEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+config.C.OpenAIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := generateClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call openai: %w", err)
	}
	defer resp.Body.Close()

	// Cap the response so a misbehaving upstream cannot stream unbounded data.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read openai response: %w", err)
	}

	var parsed openAIImageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode openai response (status %d): %w", resp.StatusCode, err)
	}

	if parsed.Error != nil {
		// Surface the upstream reason internally; the handler maps this to a
		// generic client-facing message so the key/quota state is not leaked.
		return "", fmt.Errorf("openai error (status %d): %s", resp.StatusCode, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai returned status %d", resp.StatusCode)
	}
	if len(parsed.Data) == 0 || parsed.Data[0].URL == "" {
		return "", fmt.Errorf("openai returned no image")
	}

	return parsed.Data[0].URL, nil
}

func downloadImage(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build image download request: %w", err)
	}

	resp, err := generateClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download generated image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download generated image: status %d", resp.StatusCode)
	}

	// Same ceiling as a user upload.
	data, err := io.ReadAll(io.LimitReader(resp.Body, media.MaxUploadBytes))
	if err != nil {
		return nil, fmt.Errorf("read generated image: %w", err)
	}
	return data, nil
}
