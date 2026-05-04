package parser

import (
	"errors"
	"testing"

	"krakendBedRockPlugin/internal/usage"
)

func TestEventStreamDecoder(t *testing.T) {
	t.Parallel()

	first := BuildEventStreamFrameForTest(t, map[string]string{":event-type": "chunk"}, []byte(`{"a":1}`))
	second := BuildEventStreamFrameForTest(t, map[string]string{":event-type": "metadata"}, []byte(`{"b":2}`))

	var got []Frame
	d := NewEventStreamDecoder(func(f Frame) error {
		got = append(got, f)
		return nil
	})

	combined := append(first, second...)
	d.Feed(combined[:5])
	d.Feed(combined[5:17])
	d.Feed(combined[17:])
	if err := d.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decoded %d frames, want 2", len(got))
	}
	if got[0].Headers[":event-type"] != "chunk" {
		t.Fatalf("first event type = %q", got[0].Headers[":event-type"])
	}
	if string(got[1].Payload) != `{"b":2}` {
		t.Fatalf("second payload = %q", got[1].Payload)
	}
}

func TestEventStreamDecoderErrors(t *testing.T) {
	t.Parallel()

	t.Run("truncated", func(t *testing.T) {
		t.Parallel()

		frame := BuildEventStreamFrameForTest(t, map[string]string{":event-type": "metadata"}, []byte(`{}`))
		d := NewEventStreamDecoder(func(Frame) error { return nil })
		d.Feed(frame[:len(frame)-2])
		if err := d.Close(); !errors.Is(err, usage.ErrTruncatedEventStream) {
			t.Fatalf("Close() error = %v, want %v", err, usage.ErrTruncatedEventStream)
		}
	})

	t.Run("crc mismatch", func(t *testing.T) {
		t.Parallel()

		frame := BuildEventStreamFrameForTest(t, map[string]string{":event-type": "metadata"}, []byte(`{}`))
		frame[len(frame)-1] ^= 0xff
		d := NewEventStreamDecoder(func(Frame) error { return nil })
		d.Feed(frame)
		if err := d.Close(); !errors.Is(err, usage.ErrEventStreamCRC) {
			t.Fatalf("Close() error = %v, want %v", err, usage.ErrEventStreamCRC)
		}
	})
}
