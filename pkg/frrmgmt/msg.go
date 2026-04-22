package frrmgmt

// PUBLIC message codes (frr/lib/mgmt_msg_native.h).
// Non-public BE-only codes (12–18) are omitted.
const (
	CodeError        uint16 = 0
	CodeTreeData     uint16 = 2
	CodeGetData      uint16 = 3
	CodeNotify       uint16 = 4
	CodeEdit         uint16 = 5
	CodeEditReply    uint16 = 6
	CodeRPC          uint16 = 7
	CodeRPCReply     uint16 = 8
	CodeNotifySelect uint16 = 9
	CodeSessionReq   uint16 = 10
	CodeSessionReply uint16 = 11
	CodeLock         uint16 = 19
	CodeLockReply    uint16 = 20
	CodeCommit       uint16 = 21
	CodeCommitReply  uint16 = 22
)

// Datastores.
const (
	DatastoreNone        uint8 = 0
	DatastoreRunning     uint8 = 1
	DatastoreCandidate   uint8 = 2
	DatastoreOperational uint8 = 3
)

// Data formats (map directly to libyang LYD_FORMAT values).
const (
	FormatXML    uint8 = 1
	FormatJSON   uint8 = 2
	FormatBinary uint8 = 3
)

// Edit operations.
const (
	EditOpCreate  uint8 = 0
	EditOpMerge   uint8 = 2
	EditOpRemove  uint8 = 3
	EditOpDelete  uint8 = 4
	EditOpReplace uint8 = 5
)

// Commit actions.
const (
	CommitApply    uint8 = 0
	CommitAbort    uint8 = 1
	CommitValidate uint8 = 2
)

// GetData flags (combinable).
const (
	GetDataFlagState  uint8 = 0x01
	GetDataFlagConfig uint8 = 0x02
	GetDataFlagExact  uint8 = 0x04
)

// GetData with-defaults modes (RFC 6243).
const (
	DefaultsExplicit  uint8 = 0
	DefaultsTrim      uint8 = 1
	DefaultsAll       uint8 = 2
	DefaultsAllTagged uint8 = 3
)

// Notify operation types.
const (
	NotifyOpNotification uint8 = 0
	NotifyOpDSReplace    uint8 = 1
	NotifyOpDSDelete     uint8 = 2
	NotifyOpDSPatch      uint8 = 3
	NotifyOpDSGetSync    uint8 = 4
)

// MsgHeader is the common 24-byte header for all native messages.
// Matches C struct mgmt_msg_header; _Static_assert confirms sizeof == 3*8.
type MsgHeader struct {
	Code    uint16
	Resv    uint16
	VSplit  uint32 // split point in variable data: len(xpath)+1 for xpath+data messages
	ReferID uint64 // session_id on all FE messages
	ReqID   uint64 // correlates request → reply
}

// All fixed message structs are exactly 32 bytes: 24-byte header + 8 bytes.
// This matches the C _Static_assert(sizeof(msg) == offsetof(msg, variable_field)).

type SessionReqFixed struct {
	MsgHeader
	NotifyFormat uint8
	Resv2        [7]byte
}

type SessionReplyFixed struct {
	MsgHeader
	Created uint8
	Resv2   [7]byte
}

type GetDataFixed struct {
	MsgHeader
	ResultType uint8
	Flags      uint8
	Defaults   uint8
	Datastore  uint8
	Resv2      [4]byte
}

type TreeDataFixed struct {
	MsgHeader
	PartialError int8
	ResultType   uint8
	More         uint8
	Resv2        [5]byte
}

type EditFixed struct {
	MsgHeader
	RequestType uint8
	Flags       uint8
	Datastore   uint8
	Operation   uint8
	Resv2       [4]byte
}

type EditReplyFixed struct {
	MsgHeader
	Changed uint8
	Created uint8
	Resv2   [6]byte
}

type LockFixed struct {
	MsgHeader
	Datastore uint8
	Lock      uint8
	Resv2     [6]byte
}

type LockReplyFixed struct {
	MsgHeader
	Datastore uint8
	Lock      uint8
	Resv2     [6]byte
}

type CommitFixed struct {
	MsgHeader
	Source uint8
	Target uint8
	Action uint8
	Unlock uint8
	Resv2  [4]byte
}

type CommitReplyFixed struct {
	MsgHeader
	Source uint8
	Target uint8
	Action uint8
	Unlock uint8
	Resv2  [4]byte
}

type NotifySelectFixed struct {
	MsgHeader
	Replace     uint8
	GetOnly     uint8
	Subscribing uint8
	Resv2       [5]byte
}

type NotifyDataFixed struct {
	MsgHeader
	ResultType uint8
	Op         uint8
	Resv2      [6]byte
}

type ErrorFixed struct {
	MsgHeader
	Error int16
	Resv2 [6]byte
}

type RPCFixed struct {
	MsgHeader
	RequestType uint8
	Resv2       [7]byte
}

type RPCReplyFixed struct {
	MsgHeader
	ResultType uint8
	Resv2      [7]byte
}
