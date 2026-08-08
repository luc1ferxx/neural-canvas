package media

import (
    "bytes"
    "errors"
    "io"
    "strings"
    "testing"
)

// pngHeader is a real 8-byte PNG signature followed by an IHDR chunk opener.
var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func TestSniffAcceptsPNG(t *testing.T) {
    postType, mime, body, err := Sniff(bytes.NewReader(pngHeader))
    if err != nil {
        t.Fatalf("Sniff(png) returned error: %v", err)
    }
    if postType != "image" {
        t.Errorf("postType = %q, want %q", postType, "image")
    }
    if mime != "image/png" {
        t.Errorf("mime = %q, want %q", mime, "image/png")
    }

    // The returned reader must replay the bytes consumed for sniffing,
    // otherwise the stored object would be missing its header.
    got, err := io.ReadAll(body)
    if err != nil {
        t.Fatalf("reading replayed body: %v", err)
    }
    if !bytes.Equal(got, pngHeader) {
        t.Errorf("replayed body = %q, want %q", got, pngHeader)
    }
}

func TestSniffReplaysFullStreamBeyondSniffLen(t *testing.T) {
    // A payload longer than sniffLen must come back intact, not truncated.
    payload := append(append([]byte{}, pngHeader...), bytes.Repeat([]byte("A"), sniffLen*3)...)

    _, _, body, err := Sniff(bytes.NewReader(payload))
    if err != nil {
        t.Fatalf("Sniff returned error: %v", err)
    }
    got, err := io.ReadAll(body)
    if err != nil {
        t.Fatalf("reading replayed body: %v", err)
    }
    if !bytes.Equal(got, payload) {
        t.Errorf("replayed %d bytes, want %d", len(got), len(payload))
    }
}

// TestSniffRejectsScriptCapableTypes is the regression guard for the stored-XSS
// path: bucket objects are world-readable, so anything that can execute script
// must never be accepted, regardless of what the filename claims.
func TestSniffRejectsScriptCapableTypes(t *testing.T) {
    cases := []struct {
        name    string
        payload string
    }{
        {"svg", `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
        {"html", "<!DOCTYPE html><html><body><script>alert(1)</script></body></html>"},
        {"plain text", "just some text that is definitely not an image"},
        {"empty", ""},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, _, _, err := Sniff(strings.NewReader(tc.payload))
            if err == nil {
                t.Fatalf("Sniff(%s) accepted a payload it must reject", tc.name)
            }
            var unsupported *ErrUnsupported
            if !errors.As(err, &unsupported) {
                t.Fatalf("Sniff(%s) error = %v, want *ErrUnsupported", tc.name, err)
            }
        })
    }
}

// TestSniffIgnoresClaimedExtension documents that naming does not influence the
// decision: the caller passes only bytes, and HTML stays rejected.
func TestSniffIgnoresClaimedExtension(t *testing.T) {
    html := "<!DOCTYPE html><html><body>pretend this was uploaded as cat.jpg</body></html>"
    if _, _, _, err := Sniff(strings.NewReader(html)); err == nil {
        t.Fatal("HTML content was accepted; extension must not be trusted")
    }
}
