// Package frrmgmt implements the FRR mgmtd native binary protocol.
//
// Wire frame format (all fields little-endian, confirmed in frr/lib/mgmt_msg.c:357-366):
//
//	[marker uint32][total_len uint32][payload: total_len-8 bytes]
//
// marker    = 0x23232300 | versionNative (1)
// total_len = 8 + len(payload)  — includes the frame header itself (mgmt_msg.c:319)
//
// All message struct fields (MsgHeader and per-message fixed fields) are also
// little-endian: they are written with memcpy() directly from C structs, no htonl().
package frrmgmt

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	markerPrefix   = uint32(0x23232300)
	versionNative  = uint8(1)
	frameHeaderLen = uint32(8)

	// maxFramePayload caps incoming payload size at 256 KB.
	// FRR defines MGMTD_FE_MAX_MSG_LEN = 64 KB; 4× gives headroom against
	// legitimate large responses while bounding memory use from corrupt frames.
	maxFramePayload = uint32(256 * 1024)
)

// ReadFrame reads one framed native message from r and returns the payload
// (frame header stripped). Returns an error if the marker is invalid, the
// payload exceeds maxFramePayload, or r closes before the full frame arrives.
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	marker := binary.LittleEndian.Uint32(hdr[0:4])
	if marker&0xFFFFFF00 != markerPrefix {
		return nil, fmt.Errorf("%w: got 0x%08x", ErrBadMarker, marker)
	}
	if version := uint8(marker); version != versionNative {
		return nil, fmt.Errorf("frrmgmt: unsupported protocol version %d", version)
	}

	totalLen := binary.LittleEndian.Uint32(hdr[4:8])
	if totalLen < frameHeaderLen {
		return nil, fmt.Errorf("frrmgmt: total_len %d less than frame header size", totalLen)
	}

	payloadLen := totalLen - frameHeaderLen
	if payloadLen > maxFramePayload {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("frrmgmt: reading frame payload: %w", err)
	}
	return payload, nil
}

// WriteFrame writes payload as a single framed native message to w.
// The frame header is prepended; payload may be nil or empty.
func WriteFrame(w io.Writer, payload []byte) error {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], markerPrefix|uint32(versionNative))
	binary.LittleEndian.PutUint32(hdr[4:8], frameHeaderLen+uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}
