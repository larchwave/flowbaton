package engine

import (
	"bytes"
	"path"
	"strings"
)

// Naming a screenshot after what it actually is.
//
// Both findings came out of one real simulator run. `- takeScreenshot: name`
// wrote a file with no extension at all, and the failure capture wrote JPEG
// bytes into `failure-000005.png`.
//
// The extension cannot be derived from the REQUEST: the failure path asks for a
// compressed capture, and specs/04-wire-protocols.md §3 records that iOS answers
// that with JPEG while §1 has Android's route answering PNG either way. Only the
// bytes know, so the bytes decide — one helper, used by both producers, rather
// than a per-driver rule in each of them.

// screenshotFileName gives a capture a name whose extension matches its bytes,
// leaving an extension the author already wrote alone.
func screenshotFileName(suggested string, data []byte) string {
	name := strings.TrimSpace(suggested)
	if name == "" {
		// An empty name is the artifact sink's error to report, with the name it
		// was actually handed. Inventing ".png" here would hide it.
		return suggested
	}
	extension := screenshotExtension(data)
	if strings.EqualFold(path.Ext(name), extension) {
		return name
	}
	return name + extension
}

// screenshotExtension reads the format out of the leading bytes.
//
// Unknown bytes become ".bin" rather than a hopeful ".png": a driver answering
// some third format is not this layer's to guess at, and a corrupt-looking image
// wastes the time of whoever opens it.
func screenshotExtension(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return ".png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return ".jpg"
	}
	return ".bin"
}
