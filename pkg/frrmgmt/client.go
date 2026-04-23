package frrmgmt

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
)

// Client provides the high-level operations used by the MCP layer.
// All methods are synchronous: they block until the reply arrives or ctx is cancelled.
type Client struct {
	sess *Session
	disp *Dispatcher
	conn *Conn
}

// NewClient creates a Client bound to the given session.
func NewClient(sess *Session, d *Dispatcher, conn *Conn) *Client {
	return &Client{sess: sess, disp: d, conn: conn}
}

// encodeFixed serialises a fixed-size struct to bytes using binary.LittleEndian.
// Writes to a bytes.Buffer so it never returns an error.
func encodeFixed(v any) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, v) //nolint:errcheck
	return buf.Bytes()
}

// checkError inspects the reply Code. If it is CodeError it decodes and returns
// a descriptive error; otherwise it returns nil.
func checkError(payload []byte) error {
	if len(payload) < 2 || binary.LittleEndian.Uint16(payload[0:2]) != CodeError {
		return nil
	}
	if len(payload) < 32 {
		return fmt.Errorf("frrmgmt: mgmtd returned error (short payload)")
	}
	var hdr ErrorFixed
	binary.Read(bytes.NewReader(payload), binary.LittleEndian, &hdr) //nolint:errcheck
	if len(payload) > 32 {
		if msg := string(bytes.TrimRight(payload[32:], "\x00")); msg != "" {
			return fmt.Errorf("frrmgmt: mgmtd error %d: %s", hdr.Error, msg)
		}
	}
	return fmt.Errorf("frrmgmt: mgmtd error %d", hdr.Error)
}

// roundtrip sends a single-reply request (payload already fully encoded, reqID
// at bytes 16:24) and returns the raw reply payload.
func (c *Client) roundtrip(ctx context.Context, payload []byte) ([]byte, error) {
	reqID := binary.LittleEndian.Uint64(payload[16:24])
	ch := c.disp.Expect(ctx, reqID, false)
	if err := c.conn.Send(payload); err != nil {
		c.disp.Cancel(reqID)
		return nil, err
	}
	select {
	case reply, ok := <-ch:
		if !ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("frrmgmt: connection closed before reply")
		}
		return reply, nil
	case <-ctx.Done():
		c.disp.Cancel(reqID)
		return nil, ctx.Err()
	}
}

// GetData queries config or state data for an xpath. Returns JSON bytes.
// datastore: DatastoreRunning, DatastoreCandidate, DatastoreOperational.
// flags: GetDataFlagConfig, GetDataFlagState, or both.
// Assembles multi-fragment TREE_DATA (more=1) internally before returning.
func (c *Client) GetData(ctx context.Context, xpath string, datastore, flags uint8) ([]byte, error) {
	vsplit, varData := EncodeXpathData(xpath, nil)
	reqID := c.disp.NextReqID()

	req := GetDataFixed{
		MsgHeader: MsgHeader{
			Code:    CodeGetData,
			ReferID: c.sess.ID(),
			ReqID:   reqID,
			VSplit:  vsplit,
		},
		ResultType: FormatJSON,
		Flags:      flags,
		Datastore:  datastore,
	}

	ch := c.disp.Expect(ctx, reqID, true) // multi: collect until more=0
	if err := c.conn.Send(append(encodeFixed(req), varData...)); err != nil {
		c.disp.Cancel(reqID)
		return nil, err
	}

	var result []byte
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return result, nil
			}
			if err := checkError(raw); err != nil {
				return nil, err
			}
			if len(raw) > 32 {
				var hdr TreeDataFixed
				binary.Read(bytes.NewReader(raw), binary.LittleEndian, &hdr) //nolint:errcheck
				if hdr.VSplit > 0 {
					_, jsonData, _ := DecodeXpathData(hdr.VSplit, raw[32:])
					result = append(result, jsonData...)
				} else {
					result = append(result, raw[32:]...)
				}
			}
		case <-ctx.Done():
			c.disp.Cancel(reqID)
			return nil, ctx.Err()
		}
	}
}

// Lock acquires an exclusive lock on datastore.
func (c *Client) Lock(ctx context.Context, datastore uint8) error {
	req := LockFixed{
		MsgHeader: MsgHeader{
			Code:    CodeLock,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
		},
		Datastore: datastore,
		Lock:      1,
	}
	reply, err := c.roundtrip(ctx, encodeFixed(req))
	if err != nil {
		return err
	}
	return checkError(reply)
}

// Unlock releases a previously acquired lock.
func (c *Client) Unlock(ctx context.Context, datastore uint8) error {
	req := LockFixed{
		MsgHeader: MsgHeader{
			Code:    CodeLock,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
		},
		Datastore: datastore,
		Lock:      0,
	}
	reply, err := c.roundtrip(ctx, encodeFixed(req))
	if err != nil {
		return err
	}
	return checkError(reply)
}

// EditResult is returned by Edit and EditAndCommit.
type EditResult struct {
	Changed bool
	Created bool
	XPath   string // canonical xpath of the created/modified node
}

// Edit stages a config change in the candidate datastore.
// op: EditOpCreate, EditOpMerge, EditOpReplace, EditOpDelete, EditOpRemove.
// data: JSON-encoded YANG tree for the node at xpath (nil for delete ops).
func (c *Client) Edit(ctx context.Context, xpath string, op uint8, data []byte) (*EditResult, error) {
	vsplit, varData := EncodeXpathData(xpath, data)
	req := EditFixed{
		MsgHeader: MsgHeader{
			Code:    CodeEdit,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
			VSplit:  vsplit,
		},
		RequestType: FormatJSON,
		Datastore:   DatastoreCandidate,
		Operation:   op,
	}
	reply, err := c.roundtrip(ctx, append(encodeFixed(req), varData...))
	if err != nil {
		return nil, err
	}
	if err := checkError(reply); err != nil {
		return nil, err
	}
	var hdr EditReplyFixed
	binary.Read(bytes.NewReader(reply), binary.LittleEndian, &hdr) //nolint:errcheck
	result := &EditResult{
		Changed: hdr.Changed != 0,
		Created: hdr.Created != 0,
	}
	if len(reply) > 32 {
		if strs := DecodeNulStrings(reply[32:]); len(strs) > 0 {
			result.XPath = strs[0]
		}
	}
	return result, nil
}

// Commit applies the candidate to running.
// action: CommitApply, CommitValidate, CommitAbort.
// unlock=true releases the candidate lock automatically on success.
func (c *Client) Commit(ctx context.Context, action uint8, unlock bool) error {
	unlockByte := uint8(0)
	if unlock {
		unlockByte = 1
	}
	req := CommitFixed{
		MsgHeader: MsgHeader{
			Code:    CodeCommit,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
		},
		Source: DatastoreCandidate,
		Target: DatastoreRunning,
		Action: action,
		Unlock: unlockByte,
	}
	reply, err := c.roundtrip(ctx, encodeFixed(req))
	if err != nil {
		return err
	}
	return checkError(reply)
}

// EditAndCommit applies a config change atomically using EDIT_FLAG_IMPLICIT_COMMIT.
// mgmtd acquires locks on candidate and running, edits, commits, and releases
// all locks — no separate Lock/Commit/Unlock calls are needed.
func (c *Client) EditAndCommit(ctx context.Context, xpath string, op uint8, data []byte) (*EditResult, error) {
	vsplit, varData := EncodeXpathData(xpath, data)
	req := EditFixed{
		MsgHeader: MsgHeader{
			Code:    CodeEdit,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
			VSplit:  vsplit,
		},
		RequestType: FormatJSON,
		Flags:       EditFlagImplicitCommit,
		Datastore:   DatastoreCandidate,
		Operation:   op,
	}
	reply, err := c.roundtrip(ctx, append(encodeFixed(req), varData...))
	if err != nil {
		return nil, err
	}
	if err := checkError(reply); err != nil {
		return nil, err
	}
	var hdr EditReplyFixed
	binary.Read(bytes.NewReader(reply), binary.LittleEndian, &hdr) //nolint:errcheck
	result := &EditResult{
		Changed: hdr.Changed != 0,
		Created: hdr.Created != 0,
	}
	if len(reply) > 32 {
		if strs := DecodeNulStrings(reply[32:]); len(strs) > 0 {
			result.XPath = strs[0]
		}
	}
	return result, nil
}

// Notification is delivered on the channel returned by Subscribe.
type Notification struct {
	Op    uint8
	XPath string
	Data  []byte // JSON; nil for delete-type operations
}

// Subscribe registers xpath prefix selectors and returns a channel of notifications.
// replace=true clears any previously registered selectors for this session.
// The channel is closed when ctx is cancelled.
func (c *Client) Subscribe(ctx context.Context, xpaths []string, replace bool) (<-chan Notification, error) {
	replaceVal := uint8(0)
	if replace {
		replaceVal = 1
	}
	req := NotifySelectFixed{
		MsgHeader: MsgHeader{
			Code:    CodeNotifySelect,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
		},
		Replace:     replaceVal,
		Subscribing: 1,
	}
	if err := c.conn.Send(append(encodeFixed(req), EncodeNulStrings(xpaths)...)); err != nil {
		return nil, err
	}

	ch := make(chan Notification, notifyBufSize)
	go func() {
		defer close(ch)
		src := c.disp.Notifications()
		for {
			select {
			case raw, ok := <-src:
				if !ok {
					return
				}
				n, err := decodeNotification(raw)
				if err != nil {
					continue
				}
				select {
				case ch <- n:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// RPC executes a YANG RPC or action at xpath. input is JSON-encoded (may be nil).
// Returns JSON-encoded output.
func (c *Client) RPC(ctx context.Context, xpath string, input []byte) ([]byte, error) {
	vsplit, varData := EncodeXpathData(xpath, input)
	req := RPCFixed{
		MsgHeader: MsgHeader{
			Code:    CodeRPC,
			ReferID: c.sess.ID(),
			ReqID:   c.disp.NextReqID(),
			VSplit:  vsplit,
		},
		RequestType: FormatJSON,
	}
	reply, err := c.roundtrip(ctx, append(encodeFixed(req), varData...))
	if err != nil {
		return nil, err
	}
	if err := checkError(reply); err != nil {
		return nil, err
	}
	if len(reply) <= 32 {
		return nil, nil
	}
	var hdr RPCReplyFixed
	binary.Read(bytes.NewReader(reply), binary.LittleEndian, &hdr) //nolint:errcheck
	_, outputData, err := DecodeXpathData(hdr.VSplit, reply[32:])
	if err != nil {
		return nil, fmt.Errorf("frrmgmt: decoding RPC reply: %w", err)
	}
	return outputData, nil
}

// decodeNotification parses a raw CodeNotify payload into a Notification.
func decodeNotification(payload []byte) (Notification, error) {
	if len(payload) < 32 {
		return Notification{}, errMalformedPayload
	}
	var hdr NotifyDataFixed
	binary.Read(bytes.NewReader(payload), binary.LittleEndian, &hdr) //nolint:errcheck
	xpath, data, err := DecodeXpathData(hdr.VSplit, payload[32:])
	if err != nil {
		return Notification{}, err
	}
	return Notification{Op: hdr.Op, XPath: xpath, Data: data}, nil
}
