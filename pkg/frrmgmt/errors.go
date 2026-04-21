package frrmgmt

import "errors"

var (
	errMalformedPayload = errors.New("frrmgmt: malformed variable-data payload")
	ErrNotConnected     = errors.New("frrmgmt: not connected to mgmtd")
	ErrBadMarker        = errors.New("frrmgmt: bad frame marker")
	ErrFrameTooLarge    = errors.New("frrmgmt: frame exceeds maximum size")
	ErrSessionRejected  = errors.New("frrmgmt: session creation rejected by mgmtd")
)
