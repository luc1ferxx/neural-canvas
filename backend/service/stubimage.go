package service

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// stubImageSize matches what DALL-E returns, so the two providers exercise the
// same downstream path at the same scale rather than the stub being a special
// small case.
const stubImageSize = 1024

// renderStubImage produces a PNG derived from the prompt.
//
// It is a real encoded PNG rather than a fixed byte blob or an empty file,
// because everything downstream of this point treats it as untrusted input: the
// bytes go through media.Sniff, which reads the magic number, refuses anything
// not on the allowlist, and decides the post type from the content. A stub that
// was not a genuine image would be rejected there, and the offline path would
// exercise less of the real pipeline than it appears to.
//
// The prompt is hashed into the colours so two prompts look different. That is
// not decoration: it makes it obvious at a glance in the gallery that each
// generation produced its own object rather than every post pointing at one
// cached file.
func renderStubImage(prompt string) ([]byte, error) {
	sum := sha256.Sum256([]byte(prompt))

	// Two corner colours from the digest, kept mid-range so the gradient stays
	// visible rather than washing out to near-black or near-white.
	from := color.RGBA{R: 60 + sum[0]/2, G: 60 + sum[1]/2, B: 60 + sum[2]/2, A: 255}
	to := color.RGBA{R: 60 + sum[3]/2, G: 60 + sum[4]/2, B: 60 + sum[5]/2, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, stubImageSize, stubImageSize))
	for y := 0; y < stubImageSize; y++ {
		for x := 0; x < stubImageSize; x++ {
			// Diagonal interpolation, so the result is obviously generated rather
			// than a flat fill that could be mistaken for a failed render.
			t := float64(x+y) / float64(2*(stubImageSize-1))
			img.Set(x, y, color.RGBA{
				R: lerp(from.R, to.R, t),
				G: lerp(from.G, to.G, t),
				B: lerp(from.B, to.B, t),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode stub image: %w", err)
	}
	return buf.Bytes(), nil
}

// lerp interpolates between two channel values.
func lerp(a, b uint8, t float64) uint8 {
	v := float64(a) + (float64(b)-float64(a))*t
	return uint8(math.Round(math.Max(0, math.Min(255, v))))
}
