package frrmgmt

// AppendString encodes a single NUL-terminated string for variable-length fields
// (e.g. GET_DATA xpath, SESSION_REQ client_name).
func AppendString(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// EncodeXpathData encodes the "xpath + secondary data" variable-data pattern
// used by EDIT, NOTIFY, and EDIT_REPLY messages.
// vsplit = len(xpath)+1; payload = xpath\x00 + data.
// Set msg.VSplit = vsplit before sending.
func EncodeXpathData(xpath string, data []byte) (vsplit uint32, payload []byte) {
	xlen := len(xpath) + 1
	vsplit = uint32(xlen)
	payload = make([]byte, xlen+len(data))
	copy(payload, xpath)
	copy(payload[xlen:], data)
	return vsplit, payload
}

// DecodeXpathData splits a variable-data payload at vsplit into xpath and
// secondary data. Returns an error if the payload is too short or malformed.
func DecodeXpathData(vsplit uint32, payload []byte) (xpath string, data []byte, err error) {
	if vsplit == 0 || int(vsplit) > len(payload) || payload[vsplit-1] != 0 {
		return "", nil, errMalformedPayload
	}
	xpath = string(payload[:vsplit-1])
	if int(vsplit) < len(payload) {
		data = payload[vsplit:]
	}
	return xpath, data, nil
}

// EncodeNulStrings encodes a string list as NUL-separated bytes, used by
// NOTIFY_SELECT selectors.
func EncodeNulStrings(strs []string) []byte {
	total := 0
	for _, s := range strs {
		total += len(s) + 1
	}
	b := make([]byte, 0, total)
	for _, s := range strs {
		b = append(b, s...)
		b = append(b, 0)
	}
	return b
}

// DecodeNulStrings splits a NUL-delimited byte slice into strings, dropping
// any empty trailing entry.
func DecodeNulStrings(b []byte) []string {
	if len(b) == 0 {
		return []string{}
	}
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}
