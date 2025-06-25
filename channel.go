package main

import (
	"sync"
)

type Channel struct {
	sync.Mutex // Protects access to the channel's busy state
	isBusy     bool
}

// IsBusy checks if the channel is currently busy.
func (c *Channel) IsBusy() bool {
	c.Lock()
	defer c.Unlock()
	return c.isBusy
}

// SetBusy sets the channel's busy state.
func (c *Channel) SetBusy(busy bool) {
	c.Lock()
	defer c.Unlock()
	c.isBusy = busy
}
