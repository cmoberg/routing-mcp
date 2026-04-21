package frrmgmt

import "context"

// Client provides the high-level operations used by the MCP layer.
// All methods are synchronous: they block until the reply arrives or ctx is cancelled.
type Client struct {
	// TODO: implement in Step 8
}

// NewClient creates a Client bound to the given session.
func NewClient(sess *Session, d *Dispatcher, conn *Conn) *Client {
	panic("not implemented")
}

// GetData queries config or state data for an xpath. Returns JSON bytes.
// datastore: DatastoreRunning, DatastoreCandidate, DatastoreOperational.
// flags: GetDataFlagConfig, GetDataFlagState, or both.
// Assembles multi-fragment TREE_DATA (more=1) internally before returning.
func (c *Client) GetData(ctx context.Context, xpath string, datastore, flags uint8) ([]byte, error) {
	panic("not implemented")
}

// Lock acquires an exclusive lock on datastore.
func (c *Client) Lock(ctx context.Context, datastore uint8) error {
	panic("not implemented")
}

// Unlock releases a previously acquired lock.
func (c *Client) Unlock(ctx context.Context, datastore uint8) error {
	panic("not implemented")
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
	panic("not implemented")
}

// Commit applies the candidate to running.
// action: CommitApply, CommitValidate, CommitAbort.
// unlock=true releases the candidate lock automatically on success.
func (c *Client) Commit(ctx context.Context, action uint8, unlock bool) error {
	panic("not implemented")
}

// EditAndCommit performs Lock → Edit → Commit(CommitApply, unlock=true) atomically.
// The lock is released even if Edit or Commit fails.
func (c *Client) EditAndCommit(ctx context.Context, xpath string, op uint8, data []byte) (*EditResult, error) {
	panic("not implemented")
}

// Notification is delivered on the channel returned by Subscribe.
type Notification struct {
	Op    uint8
	XPath string
	Data  []byte // JSON
}

// Subscribe registers xpath prefix selectors and returns a channel of notifications.
// replace=true clears any previously registered selectors for this session.
func (c *Client) Subscribe(ctx context.Context, xpaths []string, replace bool) (<-chan Notification, error) {
	panic("not implemented")
}

// RPC executes a YANG RPC or action at xpath. input is JSON-encoded (may be nil).
// Returns JSON-encoded output.
func (c *Client) RPC(ctx context.Context, xpath string, input []byte) ([]byte, error) {
	panic("not implemented")
}
