package parser

import (
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func BuildEventStreamFrameForTest(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()

	var headerBytes []byte
	for name, value := range headers {
		if len(name) > 255 {
			t.Fatalf("header name too long: %s", name)
		}
		if len(value) > 65535 {
			t.Fatalf("header value too long: %s", name)
		}
		headerBytes = append(headerBytes, byte(len(name)))
		headerBytes = append(headerBytes, name...)
		headerBytes = append(headerBytes, 7)
		headerBytes = binary.BigEndian.AppendUint16(headerBytes, uint16(len(value)))
		headerBytes = append(headerBytes, value...)
	}

	totalLen := uint32(eventStreamPreludeLen + len(headerBytes) + len(payload) + 4)
	frame := make([]byte, 0, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headerBytes)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame[:8]))
	frame = append(frame, headerBytes...)
	frame = append(frame, payload...)
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	return frame
}
