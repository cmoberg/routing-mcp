// Package mcp wraps frrmgmt.Client operations as MCP tools and resources.
//
// Tools:
//
//	get_config       - GetData(xpath, Running, FlagConfig)
//	get_state        - GetData(xpath, Operational, FlagState)
//	set_config       - EditAndCommit(xpath, Merge, data)
//	delete_config    - EditAndCommit(xpath, Delete, nil)
//	validate_config  - Lock → Edit → Commit(Validate) → Unlock
//	get_notifications - drain Subscribe channel (polling)
//	run_rpc          - RPC(xpath, input)
//
// Resources:
//
//	frr://yang/index        - list of available YANG modules
//	frr://yang/{module}     - contents of a specific .yang file
package mcp
