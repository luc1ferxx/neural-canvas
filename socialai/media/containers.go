package media

import (
	"bytes"
	"encoding/binary"
)

// This file adds container formats that Go's http.DetectContentType does not
// recognise. Its sniff table covers MP4, WebM and AVI but not QuickTime, FLV or
// ASF/WMV, so those uploads fell through to application/octet-stream and were
// rejected.
//
// Detection is still by content. The filename extension stays untrusted.

// asfHeaderGUID is the 16-byte ASF Header Object GUID that starts every WMV/WMA
// file. Stored little-endian on disk, which is how it is written here.
var asfHeaderGUID = []byte{
	0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11,
	0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C,
}

// flvSignature is "FLV" followed by the version byte, which is 1 in practice.
var flvSignature = []byte{'F', 'L', 'V', 0x01}

// quickTimeBrands are ISO base media file format brands that mean QuickTime
// rather than MP4. A .mov typically carries major brand "qt  ".
var quickTimeBrands = [][]byte{
	[]byte("qt  "),
	[]byte("moov"),
}

// detectExtra returns a MIME type for containers Go cannot identify, or "" when
// it recognises nothing.
func detectExtra(header []byte) string {
	if bytes.HasPrefix(header, flvSignature) {
		return "video/x-flv"
	}
	if bytes.HasPrefix(header, asfHeaderGUID) {
		// ASF carries both audio and video. Only video is offered here; an
		// audio-only .wma would be stored as a video post, which is a better
		// outcome than rejecting a legitimate .wmv.
		return "video/x-ms-wmv"
	}
	if isQuickTime(header) {
		return "video/quicktime"
	}
	return ""
}

// isQuickTime reports whether header is an ISO base media file whose brands mark
// it as QuickTime.
//
// Layout: a 4-byte big-endian box size, the literal "ftyp", a 4-byte major
// brand, a 4-byte minor version, then zero or more 4-byte compatible brands.
// Go's own sniffer looks for an "mp4" brand in the same structure and returns
// video/mp4; anything else, including "qt  ", falls through unrecognised.
func isQuickTime(header []byte) bool {
	// Need at least size + "ftyp" + major brand.
	if len(header) < 12 {
		return false
	}
	if !bytes.Equal(header[4:8], []byte("ftyp")) {
		return false
	}

	boxSize := int(binary.BigEndian.Uint32(header[0:4]))
	// The brand list must be a whole number of 4-byte entries, and a box smaller
	// than its own fixed fields is malformed.
	if boxSize < 16 || boxSize%4 != 0 {
		return false
	}
	if boxSize > len(header) {
		boxSize = len(header) - (len(header) % 4)
	}

	// Major brand at [8:12], then compatible brands every 4 bytes from 16.
	if matchesQuickTimeBrand(header[8:12]) {
		return true
	}
	for off := 16; off+4 <= boxSize; off += 4 {
		if matchesQuickTimeBrand(header[off : off+4]) {
			return true
		}
	}
	return false
}

func matchesQuickTimeBrand(brand []byte) bool {
	for _, want := range quickTimeBrands {
		if bytes.Equal(brand, want) {
			return true
		}
	}
	return false
}
