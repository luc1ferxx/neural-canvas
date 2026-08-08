package media

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// ftypBox builds an ISO base media file header: a big-endian box size, "ftyp",
// a major brand, a minor version, then the compatible brands.
func ftypBox(major string, compatible ...string) []byte {
	size := 16 + 4*len(compatible)

	buf := make([]byte, 0, size)
	sizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBytes, uint32(size))

	buf = append(buf, sizeBytes...)
	buf = append(buf, []byte("ftyp")...)
	buf = append(buf, []byte(major)...)
	buf = append(buf, 0x00, 0x00, 0x02, 0x00) // minor version
	for _, brand := range compatible {
		buf = append(buf, []byte(brand)...)
	}
	return buf
}

func TestDetectsQuickTime(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
	}{
		{"major brand qt", ftypBox("qt  ")},
		{"qt in compatible brands", ftypBox("isom", "qt  ", "avc1")},
		{"moov brand", ftypBox("moov")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			postType, mime, _, err := Sniff(bytes.NewReader(tc.header))
			if err != nil {
				t.Fatalf("Sniff rejected a QuickTime header: %v", err)
			}
			if postType != "video" {
				t.Errorf("postType = %q, want video", postType)
			}
			if mime != "video/quicktime" {
				t.Errorf("mime = %q, want video/quicktime", mime)
			}
		})
	}
}

// TestMP4StillWinsOverQuickTime pins the precedence: where Go's own sniffer
// recognises the format, its answer is used.
func TestMP4StillWinsOverQuickTime(t *testing.T) {
	header := ftypBox("mp42", "mp41", "isom")

	_, mime, _, err := Sniff(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("Sniff rejected an MP4 header: %v", err)
	}
	if mime != "video/mp4" {
		t.Errorf("mime = %q, want video/mp4", mime)
	}
}

func TestDetectsFLV(t *testing.T) {
	// "FLV", version 1, flags, then the header size.
	header := append([]byte{'F', 'L', 'V', 0x01, 0x05}, make([]byte, 20)...)

	postType, mime, _, err := Sniff(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("Sniff rejected an FLV header: %v", err)
	}
	if postType != "video" || mime != "video/x-flv" {
		t.Errorf("got (%q, %q), want (video, video/x-flv)", postType, mime)
	}
}

func TestDetectsWMV(t *testing.T) {
	header := append(append([]byte{}, asfHeaderGUID...), make([]byte, 30)...)

	postType, mime, _, err := Sniff(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("Sniff rejected an ASF header: %v", err)
	}
	if postType != "video" || mime != "video/x-ms-wmv" {
		t.Errorf("got (%q, %q), want (video, video/x-ms-wmv)", postType, mime)
	}
}

// TestExtraDetectionDoesNotWeakenTheAllowlist is the important one: adding
// container signatures must not open a path for script-capable content.
func TestExtraDetectionDoesNotWeakenTheAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"svg", `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
		{"html", "<!DOCTYPE html><html><body><script>alert(1)</script></body></html>"},
		{"ftyp with unknown brand", "\x00\x00\x00\x18ftypxxxx\x00\x00\x02\x00yyyy"},
		{"almost flv", "FLX\x01padding padding padding"},
		{"truncated ftyp", "\x00\x00\x00\x18ftyp"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := Sniff(strings.NewReader(tc.payload)); err == nil {
				t.Fatalf("Sniff accepted %s, which is not an allowed media type", tc.name)
			}
		})
	}
}

// TestIsQuickTimeRejectsMalformedBoxes exercises the size arithmetic directly,
// since a bad length there would read out of bounds.
func TestIsQuickTimeRejectsMalformedBoxes(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
	}{
		{"empty", nil},
		{"too short", []byte("\x00\x00\x00\x18ftyp")},
		{"no ftyp", []byte("\x00\x00\x00\x18abcdqt  \x00\x00\x02\x00")},
		{"box size below minimum", []byte("\x00\x00\x00\x08ftypqt \x00\x00\x00\x02\x00")},
		{"box size not a multiple of four", []byte("\x00\x00\x00\x11ftypisom\x00\x00\x02\x00qt  ")},
		{"box size larger than buffer", []byte("\xFF\xFF\xFF\xFFftypisom\x00\x00\x02\x00")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic, and must not claim QuickTime.
			if isQuickTime(tc.header) {
				t.Errorf("isQuickTime accepted a malformed header")
			}
		})
	}
}
