package frrmgmt

import (
	"bytes"
	"errors"
	"testing"
)

// TestUnitEncodeXpathDataRoundTrip encodes then decodes and asserts identity.
func TestUnitEncodeXpathDataRoundTrip(t *testing.T) {
	cases := []struct {
		xpath string
		data  []byte
	}{
		{"/frr-staticd:lib/route-list[prefix='10.0.0.0/8']", []byte(`{"metric":1}`)},
		{"/frr-ripd:ripd/instance", []byte(`{"timers":{"update":30}}`)},
		{"/", []byte(`{}`)},
	}
	for _, tc := range cases {
		vsplit, payload := EncodeXpathData(tc.xpath, tc.data)
		gotXpath, gotData, err := DecodeXpathData(vsplit, payload)
		if err != nil {
			t.Errorf("xpath=%q: unexpected error: %v", tc.xpath, err)
			continue
		}
		if gotXpath != tc.xpath {
			t.Errorf("xpath mismatch: got %q, want %q", gotXpath, tc.xpath)
		}
		if !bytes.Equal(gotData, tc.data) {
			t.Errorf("data mismatch: got %q, want %q", gotData, tc.data)
		}
	}
}

// TestUnitEncodeXpathDataVsplit asserts vsplit == len(xpath)+1 exactly.
func TestUnitEncodeXpathDataVsplit(t *testing.T) {
	cases := []string{
		"/frr-staticd:lib",
		"/",
		"",
	}
	for _, xpath := range cases {
		vsplit, _ := EncodeXpathData(xpath, nil)
		want := uint32(len(xpath) + 1)
		if vsplit != want {
			t.Errorf("xpath=%q: vsplit=%d, want %d", xpath, vsplit, want)
		}
	}
}

// TestUnitEncodeXpathDataEmptyData asserts nil data encodes and decodes back
// as nil — no secondary data section in the payload.
func TestUnitEncodeXpathDataEmptyData(t *testing.T) {
	xpath := "/frr-staticd:lib"
	vsplit, payload := EncodeXpathData(xpath, nil)

	// Payload should be exactly len(xpath)+1 bytes (xpath + NUL, no data section).
	if len(payload) != len(xpath)+1 {
		t.Fatalf("payload length: got %d, want %d", len(payload), len(xpath)+1)
	}

	gotXpath, gotData, err := DecodeXpathData(vsplit, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotXpath != xpath {
		t.Errorf("xpath mismatch: got %q, want %q", gotXpath, xpath)
	}
	if gotData != nil {
		t.Errorf("data: got %q, want nil", gotData)
	}
}

// TestUnitDecodeXpathDataCorrupt asserts DecodeXpathData returns
// errMalformedPayload for each category of corrupt input.
func TestUnitDecodeXpathDataCorrupt(t *testing.T) {
	cases := []struct {
		name    string
		vsplit  uint32
		payload []byte
	}{
		{
			name:    "vsplit zero",
			vsplit:  0,
			payload: []byte("/frr-staticd:lib\x00"),
		},
		{
			name:    "vsplit past end of payload",
			vsplit:  100,
			payload: []byte("/frr-staticd:lib\x00"),
		},
		{
			name:    "byte at vsplit-1 is not NUL",
			vsplit:  5,
			payload: []byte("abcde"), // no NUL at position 4
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeXpathData(tc.vsplit, tc.payload)
			if !errors.Is(err, errMalformedPayload) {
				t.Fatalf("expected errMalformedPayload, got %v", err)
			}
		})
	}
}

// TestUnitNulStringsRoundTrip asserts EncodeNulStrings → DecodeNulStrings
// is identity for zero, one, and five strings.
func TestUnitNulStringsRoundTrip(t *testing.T) {
	cases := [][]string{
		{},
		{"/frr-staticd:lib"},
		{
			"/frr-staticd:lib",
			"/frr-ripd:ripd",
			"/frr-zebra:zebra",
			"/frr-interface:lib",
			"/frr-vrf:lib",
		},
	}
	for _, strs := range cases {
		encoded := EncodeNulStrings(strs)
		got := DecodeNulStrings(encoded)
		if len(got) != len(strs) {
			t.Errorf("len mismatch: got %d, want %d (input %v)", len(got), len(strs), strs)
			continue
		}
		for i := range strs {
			if got[i] != strs[i] {
				t.Errorf("[%d]: got %q, want %q", i, got[i], strs[i])
			}
		}
	}
}

// TestUnitNulStringsEmptyInput asserts DecodeNulStrings(nil) returns an empty
// non-nil slice, not a panic.
func TestUnitNulStringsEmptyInput(t *testing.T) {
	got := DecodeNulStrings(nil)
	if got == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}
