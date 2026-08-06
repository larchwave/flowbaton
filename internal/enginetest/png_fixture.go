package enginetest

// PNGFixture is a REAL PNG signature followed by a marker that keeps two
// fixtures distinguishable.
//
// A real PNG signature lets tests catch extension and content-type mistakes
// while the marker keeps fixtures distinguishable.
func PNGFixture(marker string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, marker...)
}
