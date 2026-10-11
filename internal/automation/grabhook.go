package automation

import "context"

// Requests are told when a release is grabbed for a library item, so an open request
// view moves from "Searching" to "Downloading" within seconds (requests.NudgeItem). A
// direct call rather than a bus subscription, like the attention kick: requests imports
// this package, and the bus may drop events.

// SetGrabbed installs the grab listener; nil removes it.
func (c *Coordinator) SetGrabbed(fn func(ctx context.Context, mediaType string, itemID int64)) {
	if fn == nil {
		c.onGrabbed.Store(nil)
		return
	}
	c.onGrabbed.Store(&fn)
}

// grabbed tells the listener a grab for mediaType's itemID was recorded.
func (c *Coordinator) grabbed(ctx context.Context, mediaType string, itemID int64) {
	if fn := c.onGrabbed.Load(); fn != nil && itemID > 0 {
		(*fn)(ctx, mediaType, itemID)
	}
}
