// Package frrmgmt implements the FRR mgmtd native binary protocol.
// Wire format: [marker uint32][len uint32][payload bytes]
// Marker = 0x23232300 | versionNative (1).
// Byte order: little-endian (host byte order, Unix socket between local processes).
// See frr/lib/mgmt_msg.h and frr/lib/mgmt_msg.c for the canonical C implementation.
package frrmgmt
