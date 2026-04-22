package frrmgmt

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
)

// Session represents an authenticated mgmtd frontend session.
// The session_id returned by mgmtd is used as ReferID on all subsequent messages.
type Session struct {
	id   uint64
	conn *Conn
	disp *Dispatcher
}

// New sends SESSION_REQ and waits for SESSION_REPLY. clientName appears in
// `show mgmt clients` in vtysh (max 32 chars per MGMTD_CLIENT_NAME_MAX_LEN).
func New(ctx context.Context, conn *Conn, d *Dispatcher, clientName string) (*Session, error) {
	reqID := d.NextReqID()

	// Register expect before sending to prevent a race where the reply
	// arrives before the pending channel is registered.
	ch := d.Expect(ctx, reqID, false)

	req := SessionReqFixed{
		MsgHeader: MsgHeader{
			Code:  CodeSessionReq,
			ReqID: reqID,
			// ReferID=0 signals "create new session" to mgmtd.
		},
		NotifyFormat: FormatJSON,
	}

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, req); err != nil {
		d.Cancel(reqID)
		return nil, fmt.Errorf("frrmgmt: encoding SESSION_REQ: %w", err)
	}
	buf.Write(AppendString(clientName))

	if err := conn.Send(buf.Bytes()); err != nil {
		d.Cancel(reqID)
		return nil, err
	}

	select {
	case replyPayload, ok := <-ch:
		if !ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, ErrSessionRejected
		}
		var reply SessionReplyFixed
		if err := binary.Read(bytes.NewReader(replyPayload), binary.LittleEndian, &reply); err != nil {
			return nil, fmt.Errorf("frrmgmt: decoding SESSION_REPLY: %w", err)
		}
		if reply.Created == 0 {
			return nil, ErrSessionRejected
		}
		return &Session{
			id:   reply.ReferID,
			conn: conn,
			disp: d,
		}, nil
	case <-ctx.Done():
		d.Cancel(reqID)
		return nil, ctx.Err()
	}
}

// ID returns the session_id assigned by mgmtd.
func (s *Session) ID() uint64 {
	return s.id
}

// Close sends SESSION_REQ with refer_id=sessionID to delete the session.
// It is fire-and-forget: the socket close that follows on the caller side
// will cause mgmtd to reap the session regardless.
func (s *Session) Close(ctx context.Context) error {
	req := SessionReqFixed{
		MsgHeader: MsgHeader{
			Code:    CodeSessionReq,
			ReferID: s.id,
			ReqID:   s.disp.NextReqID(),
		},
		NotifyFormat: FormatJSON,
	}

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, req); err != nil {
		return fmt.Errorf("frrmgmt: encoding SESSION_REQ close: %w", err)
	}

	return s.conn.Send(buf.Bytes())
}
