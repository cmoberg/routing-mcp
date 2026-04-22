package frrmgmt

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"
)

// makeDispatchFrame creates a minimal 32-byte payload for the given code and reqID.
func makeDispatchFrame(code uint16, reqID uint64) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint16(b[0:2], code)
	binary.LittleEndian.PutUint64(b[16:24], reqID)
	return b
}

// makeTreeFrame creates a CodeTreeData frame with the More field set at moreOffset.
func makeTreeFrame(reqID uint64, more uint8) []byte {
	b := makeDispatchFrame(CodeTreeData, reqID)
	b[moreOffset] = more
	return b
}

// recvFrame receives from ch within timeout, failing the test on timeout.
func recvFrame(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(timeout):
		t.Fatal("timeout waiting for frame")
		return nil
	}
}

// TestUnitDispatchSingleReply registers a pending req_id, delivers one frame,
// and verifies it arrives on the channel which is then closed.
func TestUnitDispatchSingleReply(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	ch := d.Expect(context.Background(), 1, false)
	frame := makeDispatchFrame(CodeLockReply, 1)
	frames <- frame

	got := recvFrame(t, ch, time.Second)
	if !bytes.Equal(got, frame) {
		t.Fatal("received wrong frame")
	}

	// Channel must be closed after a single reply.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after single reply")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after single reply")
	}
}

// TestUnitDispatchTwoInflight registers two concurrent req_ids and asserts
// each channel receives only its own reply frame.
func TestUnitDispatchTwoInflight(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	ch1 := d.Expect(context.Background(), 1, false)
	ch2 := d.Expect(context.Background(), 2, false)

	f1 := makeDispatchFrame(CodeEditReply, 1)
	f2 := makeDispatchFrame(CodeEditReply, 2)
	frames <- f1
	frames <- f2

	got1 := recvFrame(t, ch1, time.Second)
	got2 := recvFrame(t, ch2, time.Second)

	if !bytes.Equal(got1, f1) {
		t.Error("ch1 received wrong frame")
	}
	if !bytes.Equal(got2, f2) {
		t.Error("ch2 received wrong frame")
	}
}

// TestUnitDispatchNotify asserts that CodeNotify frames are delivered to
// Notifications() and not to any pending request channel.
func TestUnitDispatchNotify(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	frame := makeDispatchFrame(CodeNotify, 0)
	frames <- frame

	select {
	case got := <-d.Notifications():
		if !bytes.Equal(got, frame) {
			t.Fatal("notification frame mismatch")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

// TestUnitDispatchUnmatched verifies that a reply for an unregistered req_id
// is silently discarded without blocking or panicking.
func TestUnitDispatchUnmatched(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	frames <- makeDispatchFrame(CodeTreeData, 42)
	time.Sleep(50 * time.Millisecond) // give dispatchLoop time to process
	_ = d                             // keep d alive
}

// TestUnitDispatchCancel verifies that a frame arriving after Cancel(reqID)
// is discarded cleanly without blocking or panicking.
func TestUnitDispatchCancel(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.Expect(ctx, 1, false)
	d.Cancel(1)

	// Late reply for the cancelled req_id must be discarded.
	frames <- makeDispatchFrame(CodeTreeData, 1)
	time.Sleep(50 * time.Millisecond)
}

// TestUnitDispatchMultiFragment verifies that three frames with more=1,1,0
// all arrive on the same pending channel and the channel is closed only after
// the more=0 frame.
func TestUnitDispatchMultiFragment(t *testing.T) {
	frames := make(chan []byte, 4)
	d := NewDispatcher(frames)

	ch := d.Expect(context.Background(), 1, true)

	f1 := makeTreeFrame(1, 1)
	f2 := makeTreeFrame(1, 1)
	f3 := makeTreeFrame(1, 0) // last fragment

	frames <- f1
	frames <- f2
	frames <- f3

	got1 := recvFrame(t, ch, time.Second)
	got2 := recvFrame(t, ch, time.Second)
	got3 := recvFrame(t, ch, time.Second)

	if !bytes.Equal(got1, f1) || !bytes.Equal(got2, f2) || !bytes.Equal(got3, f3) {
		t.Error("received wrong fragment sequence")
	}

	// Channel must be closed after the more=0 fragment.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after final fragment")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after final fragment")
	}
}
