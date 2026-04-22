package frrmgmt

import (
	"encoding/binary"
	"testing"
)

// TestUnitMsgHeaderSize verifies MsgHeader serialises to exactly 24 bytes,
// matching the C _Static_assert(sizeof(struct mgmt_msg_header) == 3*8).
func TestUnitMsgHeaderSize(t *testing.T) {
	if got := binary.Size(MsgHeader{}); got != 24 {
		t.Fatalf("MsgHeader: binary.Size = %d, want 24", got)
	}
}

// TestUnitAllFixedStructSizes verifies every per-message fixed struct
// serialises to exactly 32 bytes (24-byte MsgHeader + 8 bytes of fields).
// This mirrors the C pattern:
//
//	_Static_assert(sizeof(msg) == offsetof(msg, variable_field))
//
// which confirms no trailing padding before the variable-length data.
func TestUnitAllFixedStructSizes(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"SessionReqFixed", SessionReqFixed{}},
		{"SessionReplyFixed", SessionReplyFixed{}},
		{"GetDataFixed", GetDataFixed{}},
		{"TreeDataFixed", TreeDataFixed{}},
		{"EditFixed", EditFixed{}},
		{"EditReplyFixed", EditReplyFixed{}},
		{"LockFixed", LockFixed{}},
		{"LockReplyFixed", LockReplyFixed{}},
		{"CommitFixed", CommitFixed{}},
		{"CommitReplyFixed", CommitReplyFixed{}},
		{"NotifySelectFixed", NotifySelectFixed{}},
		{"NotifyDataFixed", NotifyDataFixed{}},
		{"ErrorFixed", ErrorFixed{}},
		{"RPCFixed", RPCFixed{}},
		{"RPCReplyFixed", RPCReplyFixed{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := binary.Size(tc.val); got != 32 {
				t.Fatalf("%s: binary.Size = %d, want 32", tc.name, got)
			}
		})
	}
}

// TestUnitConstantValues spot-checks that Go constants match the values in
// frr/lib/mgmt_msg_native.h. Cited line numbers are from the FRR 10.7.0-dev source.
func TestUnitConstantValues(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16 // raw value from C header
	}{
		// Message codes — mgmt_msg_native.h:184-208
		{"CodeError", CodeError, 0},
		{"CodeTreeData", CodeTreeData, 2},
		{"CodeGetData", CodeGetData, 3},
		{"CodeNotify", CodeNotify, 4},
		{"CodeEdit", CodeEdit, 5},
		{"CodeEditReply", CodeEditReply, 6},
		{"CodeRPC", CodeRPC, 7},
		{"CodeRPCReply", CodeRPCReply, 8},
		{"CodeNotifySelect", CodeNotifySelect, 9},
		{"CodeSessionReq", CodeSessionReq, 10},
		{"CodeSessionReply", CodeSessionReply, 11},
		{"CodeLock", CodeLock, 19},
		{"CodeLockReply", CodeLockReply, 20},
		{"CodeCommit", CodeCommit, 21},
		{"CodeCommitReply", CodeCommitReply, 22},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (check frr/lib/mgmt_msg_native.h)", tc.name, tc.got, tc.want)
		}
	}

	// Datastores — mgmt_msg_native.h:213-216
	checkU8 := func(name string, got, want uint8) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d (check frr/lib/mgmt_msg_native.h)", name, got, want)
		}
	}
	checkU8("DatastoreNone", DatastoreNone, 0)
	checkU8("DatastoreRunning", DatastoreRunning, 1)
	checkU8("DatastoreCandidate", DatastoreCandidate, 2)
	checkU8("DatastoreOperational", DatastoreOperational, 3)

	// Formats — mgmt_msg_native.h:223-225
	checkU8("FormatXML", FormatXML, 1)
	checkU8("FormatJSON", FormatJSON, 2)
	checkU8("FormatBinary", FormatBinary, 3)

	// Edit operations — mgmt_msg_native.h:389-393
	checkU8("EditOpCreate", EditOpCreate, 0)
	checkU8("EditOpMerge", EditOpMerge, 2)
	checkU8("EditOpRemove", EditOpRemove, 3)
	checkU8("EditOpDelete", EditOpDelete, 4)
	checkU8("EditOpReplace", EditOpReplace, 5)

	// Commit actions — mgmt_msg_native.h:581-583
	checkU8("CommitApply", CommitApply, 0)
	checkU8("CommitAbort", CommitAbort, 1)
	checkU8("CommitValidate", CommitValidate, 2)

	// GetData flags — mgmt_msg_native.h:319-321
	checkU8("GetDataFlagState", GetDataFlagState, 0x01)
	checkU8("GetDataFlagConfig", GetDataFlagConfig, 0x02)
	checkU8("GetDataFlagExact", GetDataFlagExact, 0x04)

	// Notify ops — mgmt_msg_native.h:355-359
	checkU8("NotifyOpNotification", NotifyOpNotification, 0)
	checkU8("NotifyOpDSReplace", NotifyOpDSReplace, 1)
	checkU8("NotifyOpDSDelete", NotifyOpDSDelete, 2)
	checkU8("NotifyOpDSPatch", NotifyOpDSPatch, 3)
	checkU8("NotifyOpDSGetSync", NotifyOpDSGetSync, 4)
}
