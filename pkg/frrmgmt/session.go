package frrmgmt

import "context"

// Session represents an authenticated mgmtd frontend session.
// The session_id returned by mgmtd is used as ReferID on all subsequent messages.
type Session struct {
	// TODO: implement in Step 7
}

// New sends SESSION_REQ and waits for SESSION_REPLY. clientName appears in
// `show mgmt clients` in vtysh (max 32 chars per MGMTD_CLIENT_NAME_MAX_LEN).
func New(ctx context.Context, conn *Conn, d *Dispatcher, clientName string) (*Session, error) {
	panic("not implemented")
}

// ID returns the session_id assigned by mgmtd.
func (s *Session) ID() uint64 {
	panic("not implemented")
}

// Close sends SESSION_REQ with refer_id=sessionID to delete the session.
func (s *Session) Close(ctx context.Context) error {
	panic("not implemented")
}
