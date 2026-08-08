package media

import (
    "bytes"
    "fmt"
    "io"
    "net/http"
    "strings"
)

// MaxUploadBytes caps a single upload. Without a cap, one request can drive the
// process out of memory: net/http buffers multipart parts up to the limit.
const MaxUploadBytes = 32 << 20 // 32 MiB

// sniffLen is what http.DetectContentType inspects.
const sniffLen = 512

// allowed maps a sniffed MIME type to the "type" stored on the post.
//
// This is an allowlist, not a denylist, and it is keyed on types that Go's
// http.DetectContentType actually emits. Deliberately absent:
//
//   - image/svg+xml and text/html: they can carry <script>. Every object in the
//     bucket is world-readable, so an uploaded .svg would be a stored XSS
//     served from the app's own storage domain.
//   - anything Go cannot positively identify: it falls through to
//     application/octet-stream and is rejected.
var allowed = map[string]string{
    "image/jpeg": "image",
    "image/png":  "image",
    "image/gif":  "image",
    "image/webp": "image",
    "video/mp4":  "video",
    "video/webm": "video",
    "video/avi":  "video",
}

// ErrUnsupported reports a payload whose real content type is not allowed. The
// message carries the sniffed type so the client learns what was rejected.
type ErrUnsupported struct {
    MIME string
}

func (e *ErrUnsupported) Error() string {
    return fmt.Sprintf("unsupported media type %q", e.MIME)
}

// Sniff determines the real content type of r by inspecting its leading bytes,
// and returns a reader that replays the whole stream from the start.
//
// The filename extension is deliberately ignored. It is supplied by the client,
// so trusting it lets anyone store an HTML page as "cat.jpg" and have it served
// publicly with an HTML content type.
func Sniff(r io.Reader) (postType, mime string, body io.Reader, err error) {
    header := make([]byte, sniffLen)
    n, err := io.ReadFull(r, header)
    // Short files are fine: ReadFull reports EOF (nothing read) or
    // ErrUnexpectedEOF (fewer than sniffLen read). Neither is a failure here.
    if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
        return "", "", nil, fmt.Errorf("read media header: %w", err)
    }
    header = header[:n]

    if n == 0 {
        return "", "", nil, &ErrUnsupported{MIME: "empty"}
    }

    mime = http.DetectContentType(header)
    // DetectContentType may append parameters, e.g. "text/plain; charset=utf-8".
    if i := strings.IndexByte(mime, ';'); i >= 0 {
        mime = strings.TrimSpace(mime[:i])
    }

    postType, ok := allowed[mime]
    if !ok {
        return "", "", nil, &ErrUnsupported{MIME: mime}
    }

    return postType, mime, io.MultiReader(bytes.NewReader(header), r), nil
}
