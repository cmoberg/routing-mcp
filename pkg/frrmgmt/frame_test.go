package frrmgmt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// TestUnitFrameRoundTrip encodes then decodes a payload and asserts identity.
func TestUnitFrameRoundTrip(t *testing.T) {
	payload := []byte("hello mgmtd native protocol")
	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q, want %q", got, payload)
	}
}

// TestUnitFrameMarkerBytes pins the exact wire bytes of the frame marker.
// Marker = 0x23232301 (0x23232300 | version 1) in little-endian = [0x01, 0x23, 0x23, 0x23].
// This encodes the byte-order decision confirmed in frr/lib/mgmt_msg.c:357-360
// (struct fields assigned directly, no htonl/stream_putl).
func TestUnitFrameMarkerBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, []byte{0xAA}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	wire := buf.Bytes()
	want := []byte{0x01, 0x23, 0x23, 0x23}
	if !bytes.Equal(wire[:4], want) {
		t.Fatalf("marker bytes: got %#v, want %#v — byte order mismatch", wire[:4], want)
	}
}

// TestUnitFrameBadMarker asserts ReadFrame returns ErrBadMarker for a corrupt marker.
func TestUnitFrameBadMarker(t *testing.T) {
	// Valid-length header with a wrong marker prefix.
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 0xDEADBEEF)
	binary.LittleEndian.PutUint32(hdr[4:8], frameHeaderLen)
	_, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrBadMarker) {
		t.Fatalf("expected ErrBadMarker, got %v", err)
	}
}

// TestUnitFrameOversized asserts ReadFrame returns ErrFrameTooLarge when
// total_len implies a payload exceeding maxFramePayload.
func TestUnitFrameOversized(t *testing.T) {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], markerPrefix|uint32(versionNative))
	binary.LittleEndian.PutUint32(hdr[4:8], frameHeaderLen+maxFramePayload+1)
	_, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

// TestUnitFrameTruncated asserts ReadFrame returns an error (not a panic)
// when the reader closes before the full payload is available.
func TestUnitFrameTruncated(t *testing.T) {
	// Header claims 100 bytes of payload; supply only 10.
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], markerPrefix|uint32(versionNative))
	binary.LittleEndian.PutUint32(hdr[4:8], frameHeaderLen+100)
	r := io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader(make([]byte, 10)))
	_, err := ReadFrame(r)
	if err == nil {
		t.Fatal("expected error on truncated payload, got nil")
	}
}

// TestUnitFrameEmptyPayload asserts that a zero-length payload round-trips cleanly.
func TestUnitFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, nil); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	// Wire should be exactly 8 bytes (header only).
	if buf.Len() != 8 {
		t.Fatalf("expected 8 wire bytes, got %d", buf.Len())
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(got))
	}
}
