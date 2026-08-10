package iosdevice

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"os"
)

// aviWriter assembles JPEG frames into an MJPEG AVI. The container is plain
// RIFF: every mainstream player decodes MJPG, and writing it needs no codec
// dependency — the frames stay the JPEGs the screenshot service produced.
type aviWriter struct {
	file          *os.File
	frameRate     int
	width, height int
	frames        int
	moviStart     int64
	index         []aviIndexEntry
}

type aviIndexEntry struct {
	offset, size uint32
}

const (
	aviHeaderListSize = 4 + (8 + 56) + (8 + 4 + (8 + 56) + (8 + 40))
	aviKeyframeFlag   = 0x10
)

// newAVIWriter writes the container headers with placeholder counts; close
// patches them once the frame count is known.
func newAVIWriter(file *os.File, width, height, frameRate int) (*aviWriter, error) {
	if width <= 0 || height <= 0 || frameRate <= 0 {
		return nil, fmt.Errorf("AVI dimensions and rate must be positive, got %dx%d @%d", width, height, frameRate)
	}
	writer := &aviWriter{file: file, frameRate: frameRate, width: width, height: height}
	if err := writer.writeHeaders(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (writer *aviWriter) writeHeaders() error {
	var header bytes.Buffer
	writeFourCC := func(code string) { header.WriteString(code) }
	writeUint32 := func(value uint32) { _ = binary.Write(&header, binary.LittleEndian, value) }

	writeFourCC("RIFF")
	writeUint32(0) // patched by close
	writeFourCC("AVI ")

	writeFourCC("LIST")
	writeUint32(aviHeaderListSize)
	writeFourCC("hdrl")

	writeFourCC("avih")
	writeUint32(56)
	writeUint32(uint32(1_000_000 / writer.frameRate)) // microseconds per frame
	writeUint32(0)                                    // max bytes per second (unconstrained)
	writeUint32(0)                                    // padding granularity
	writeUint32(aviKeyframeFlag)                      // AVIF_HASINDEX
	writeUint32(0)                                    // total frames, patched by close
	writeUint32(0)                                    // initial frames
	writeUint32(1)                                    // one stream
	writeUint32(0)                                    // suggested buffer size
	writeUint32(uint32(writer.width))
	writeUint32(uint32(writer.height))
	for range 4 {
		writeUint32(0)
	}

	writeFourCC("LIST")
	writeUint32(4 + (8 + 56) + (8 + 40))
	writeFourCC("strl")

	writeFourCC("strh")
	writeUint32(56)
	writeFourCC("vids")
	writeFourCC("MJPG")
	writeUint32(0)                        // flags
	writeUint32(0)                        // priority + language
	writeUint32(0)                        // initial frames
	writeUint32(1)                        // scale
	writeUint32(uint32(writer.frameRate)) // rate: rate/scale = fps
	writeUint32(0)                        // start
	writeUint32(0)                        // length in frames, patched by close
	writeUint32(0)                        // suggested buffer size
	writeUint32(0xFFFFFFFF)               // default quality
	writeUint32(0)                        // sample size
	writeUint16 := func(value uint16) { _ = binary.Write(&header, binary.LittleEndian, value) }
	writeUint16(0)                     // rcFrame left
	writeUint16(0)                     // rcFrame top
	writeUint16(uint16(writer.width))  // rcFrame right
	writeUint16(uint16(writer.height)) // rcFrame bottom

	writeFourCC("strf")
	writeUint32(40)
	writeUint32(40) // BITMAPINFOHEADER size
	writeUint32(uint32(writer.width))
	writeUint32(uint32(writer.height))
	_ = binary.Write(&header, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(&header, binary.LittleEndian, uint16(24)) // bit count
	writeFourCC("MJPG")
	writeUint32(uint32(writer.width * writer.height * 3)) // image size
	for range 4 {
		writeUint32(0)
	}

	writeFourCC("LIST")
	writeUint32(0) // movi size, patched by close
	writeFourCC("movi")

	if _, err := writer.file.Write(header.Bytes()); err != nil {
		return err
	}
	writer.moviStart = int64(header.Len()) - 4 // offset of "movi"
	return nil
}

// writeFrame appends one JPEG as a video chunk.
func (writer *aviWriter) writeFrame(frame []byte) error {
	position, err := writer.file.Seek(0, 2)
	if err != nil {
		return err
	}
	padded := frame
	if len(frame)%2 == 1 {
		padded = append(append([]byte{}, frame...), 0)
	}
	var chunk bytes.Buffer
	chunk.WriteString("00dc")
	_ = binary.Write(&chunk, binary.LittleEndian, uint32(len(frame)))
	chunk.Write(padded)
	if _, err := writer.file.Write(chunk.Bytes()); err != nil {
		return err
	}
	writer.index = append(writer.index, aviIndexEntry{
		// idx1 offsets count from the start of the "movi" fourcc.
		offset: uint32(position - writer.moviStart),
		size:   uint32(len(frame)),
	})
	writer.frames++
	return nil
}

// close writes the index and patches every placeholder size and count.
func (writer *aviWriter) close() error {
	moviEnd, err := writer.file.Seek(0, 2)
	if err != nil {
		return err
	}
	var index bytes.Buffer
	index.WriteString("idx1")
	_ = binary.Write(&index, binary.LittleEndian, uint32(len(writer.index)*16))
	for _, entry := range writer.index {
		index.WriteString("00dc")
		_ = binary.Write(&index, binary.LittleEndian, uint32(aviKeyframeFlag))
		_ = binary.Write(&index, binary.LittleEndian, entry.offset)
		_ = binary.Write(&index, binary.LittleEndian, entry.size)
	}
	if _, err := writer.file.Write(index.Bytes()); err != nil {
		return err
	}
	fileEnd := moviEnd + int64(index.Len())

	patch := func(offset int64, value uint32) error {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], value)
		_, err := writer.file.WriteAt(encoded[:], offset)
		return err
	}
	if err := patch(4, uint32(fileEnd-8)); err != nil { // RIFF size
		return err
	}
	if err := patch(48, uint32(writer.frames)); err != nil { // avih total frames
		return err
	}
	if err := patch(140, uint32(writer.frames)); err != nil { // strh length
		return err
	}
	if err := patch(writer.moviStart-4, uint32(moviEnd-writer.moviStart)); err != nil { // movi LIST size
		return err
	}
	return writer.file.Close()
}

// jpegFrame normalizes one screenshot into a JPEG for the MJPEG stream. The
// instruments service hands back PNG on modern devices; JPEG passes through.
func jpegFrame(screenshot []byte) ([]byte, int, int, error) {
	if len(screenshot) >= 2 && screenshot[0] == 0xFF && screenshot[1] == 0xD8 {
		config, err := jpeg.DecodeConfig(bytes.NewReader(screenshot))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("decode screenshot: %w", err)
		}
		return screenshot, config.Width, config.Height, nil
	}
	decoded, err := decodeScreenshotImage(screenshot)
	if err != nil {
		return nil, 0, 0, err
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: 80}); err != nil {
		return nil, 0, 0, fmt.Errorf("encode screenshot frame: %w", err)
	}
	bounds := decoded.Bounds()
	return encoded.Bytes(), bounds.Dx(), bounds.Dy(), nil
}
