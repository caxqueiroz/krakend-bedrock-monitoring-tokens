package parser

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"

	"krakendBedRockPlugin/internal/usage"
)

const (
	eventStreamPreludeLen = 12
	eventStreamMinLen     = eventStreamPreludeLen + 4
)

type Frame struct {
	Headers map[string]string
	Payload []byte
}

type EventStreamDecoder struct {
	buf     []byte
	onFrame func(Frame) error
	err     error
}

func NewEventStreamDecoder(onFrame func(Frame) error) *EventStreamDecoder {
	return &EventStreamDecoder{onFrame: onFrame}
}

func (d *EventStreamDecoder) Feed(b []byte) {
	if d.err != nil || len(b) == 0 {
		return
	}
	d.buf = append(d.buf, b...)
	for {
		if len(d.buf) < eventStreamPreludeLen {
			return
		}

		totalLen := binary.BigEndian.Uint32(d.buf[0:4])
		headersLen := binary.BigEndian.Uint32(d.buf[4:8])
		if totalLen < eventStreamMinLen || totalLen > math.MaxInt32 || headersLen > totalLen-eventStreamMinLen {
			d.err = fmt.Errorf("%w: invalid lengths", usage.ErrTruncatedEventStream)
			return
		}
		if len(d.buf) < int(totalLen) {
			return
		}

		frameBytes := d.buf[:totalLen]
		if !validCRC(frameBytes[:8], binary.BigEndian.Uint32(frameBytes[8:12])) {
			d.err = fmt.Errorf("%w: prelude", usage.ErrEventStreamCRC)
			return
		}
		if !validCRC(frameBytes[:totalLen-4], binary.BigEndian.Uint32(frameBytes[totalLen-4:totalLen])) {
			d.err = fmt.Errorf("%w: message", usage.ErrEventStreamCRC)
			return
		}

		headerStart := eventStreamPreludeLen
		headerEnd := headerStart + int(headersLen)
		headers, err := decodeHeaders(frameBytes[headerStart:headerEnd])
		if err != nil {
			d.err = err
			return
		}
		payload := append([]byte(nil), frameBytes[headerEnd:totalLen-4]...)
		if d.onFrame != nil {
			if err := d.onFrame(Frame{Headers: headers, Payload: payload}); err != nil {
				d.err = err
				return
			}
		}

		d.buf = d.buf[totalLen:]
	}
}

func (d *EventStreamDecoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if len(d.buf) > 0 {
		return usage.ErrTruncatedEventStream
	}
	return nil
}

func validCRC(b []byte, want uint32) bool {
	return crc32.ChecksumIEEE(b) == want
}

func decodeHeaders(b []byte) (map[string]string, error) {
	headers := make(map[string]string)
	for len(b) > 0 {
		nameLen := int(b[0])
		b = b[1:]
		if len(b) < nameLen+1 {
			return nil, fmt.Errorf("%w: header name", usage.ErrTruncatedEventStream)
		}
		name := string(b[:nameLen])
		headerType := b[nameLen]
		b = b[nameLen+1:]

		value, rest, err := decodeHeaderValue(headerType, b)
		if err != nil {
			return nil, err
		}
		headers[name] = value
		b = rest
	}
	return headers, nil
}

func decodeHeaderValue(headerType byte, b []byte) (string, []byte, error) {
	switch headerType {
	case 0:
		return "true", b, nil
	case 1:
		return "false", b, nil
	case 2:
		if len(b) < 1 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return fmt.Sprintf("%d", int8(b[0])), b[1:], nil
	case 3:
		if len(b) < 2 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(b[:2]))), b[2:], nil
	case 4:
		if len(b) < 4 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(b[:4]))), b[4:], nil
	case 5, 8:
		if len(b) < 8 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return fmt.Sprintf("%d", int64(binary.BigEndian.Uint64(b[:8]))), b[8:], nil
	case 6, 7:
		if len(b) < 2 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		n := int(binary.BigEndian.Uint16(b[:2]))
		if len(b) < 2+n {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return string(b[2 : 2+n]), b[2+n:], nil
	case 9:
		if len(b) < 16 {
			return "", nil, usage.ErrTruncatedEventStream
		}
		return fmt.Sprintf("%x", b[:16]), b[16:], nil
	default:
		return "", nil, fmt.Errorf("%w: unsupported header type %d", usage.ErrTruncatedEventStream, headerType)
	}
}
