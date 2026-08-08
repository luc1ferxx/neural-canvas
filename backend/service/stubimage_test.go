package service

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/luc1ferxx/neural-canvas/backend/media"
)

// TestStubImagePassesTheRealValidator is the assertion that matters.
//
// Everything downstream of the provider treats the bytes as untrusted: they go
// through media.Sniff, which reads the magic number, refuses anything off the
// allowlist and decides the post type from the content. If the stub were not a
// genuine PNG it would be rejected there, and the offline stack would exercise
// noticeably less of the real pipeline than it looked like it did.
func TestStubImagePassesTheRealValidator(t *testing.T) {
	data, err := renderStubImage("a cat riding a bicycle")
	if err != nil {
		t.Fatalf("renderStubImage(): %v", err)
	}

	postType, mime, body, err := media.Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the stub image was rejected by media.Sniff: %v", err)
	}
	if postType != "image" {
		t.Errorf("post type = %q, want image", postType)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}

	// Sniff replays what it read, so the whole image must still be readable after
	// validation -- otherwise the upload would store a truncated object.
	replayed, err := png.Decode(body)
	if err != nil {
		t.Fatalf("the image could not be decoded after Sniff replayed it: %v", err)
	}
	bounds := replayed.Bounds()
	if bounds.Dx() != stubImageSize || bounds.Dy() != stubImageSize {
		t.Errorf("size = %dx%d, want %dx%d",
			bounds.Dx(), bounds.Dy(), stubImageSize, stubImageSize)
	}
}

// TestStubImageVariesByPrompt keeps the gallery honest: identical output for every
// prompt would make it impossible to see at a glance whether each generation
// produced its own stored object, or whether every post points at one file.
func TestStubImageVariesByPrompt(t *testing.T) {
	first, err := renderStubImage("prompt one")
	if err != nil {
		t.Fatalf("renderStubImage(): %v", err)
	}
	second, err := renderStubImage("prompt two")
	if err != nil {
		t.Fatalf("renderStubImage(): %v", err)
	}

	if bytes.Equal(first, second) {
		t.Error("two different prompts produced byte-identical images")
	}
}

// TestStubImageIsDeterministic means a failure is reproducible from the prompt
// alone, rather than depending on when it ran.
func TestStubImageIsDeterministic(t *testing.T) {
	first, err := renderStubImage("same prompt")
	if err != nil {
		t.Fatalf("renderStubImage(): %v", err)
	}
	second, err := renderStubImage("same prompt")
	if err != nil {
		t.Fatalf("renderStubImage(): %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Error("the same prompt produced different bytes")
	}
}

// TestStubImageHandlesAnEmptyPrompt guards the boundary. The handler rejects an
// empty prompt before this is reached, but a renderer that panicked on one would
// be a latent crash waiting for that check to be relaxed.
func TestStubImageHandlesAnEmptyPrompt(t *testing.T) {
	data, err := renderStubImage("")
	if err != nil {
		t.Fatalf("renderStubImage(\"\"): %v", err)
	}
	if _, _, _, err := media.Sniff(bytes.NewReader(data)); err != nil {
		t.Errorf("the empty-prompt image was rejected: %v", err)
	}
}

func TestLerp(t *testing.T) {
	cases := []struct {
		a, b uint8
		t    float64
		want uint8
	}{
		{0, 255, 0, 0},
		{0, 255, 1, 255},
		{0, 255, 0.5, 128},
		{100, 100, 0.5, 100},
		{255, 0, 0.5, 128},
		// Out-of-range t must clamp rather than wrap around, which is what an
		// unchecked uint8 conversion would do.
		{0, 255, 2, 255},
		{0, 255, -1, 0},
	}

	for _, tc := range cases {
		if got := lerp(tc.a, tc.b, tc.t); got != tc.want {
			t.Errorf("lerp(%d, %d, %v) = %d, want %d", tc.a, tc.b, tc.t, got, tc.want)
		}
	}
}
