// Package dispatch is a tiny action registry. Handlers register themselves by
// string name (usually from init funcs) and callers invoke them by name, so
// call sites are never textually adjacent to implementations.
package dispatch

import (
	"context"
	"fmt"
	"sync"
)

// HandlerFunc handles one named action with an opaque JSON payload.
type HandlerFunc func(ctx context.Context, payload []byte) error

var (
	mu       sync.RWMutex
	handlers = map[string]HandlerFunc{}
)

// Register binds an action name to a handler. Last registration wins.
func Register(action string, h HandlerFunc) {
	mu.Lock()
	defer mu.Unlock()
	handlers[action] = h
}

// Dispatch invokes the handler registered for action.
func Dispatch(ctx context.Context, action string, payload []byte) error {
	mu.RLock()
	h, ok := handlers[action]
	mu.RUnlock()
	if !ok {
		return fmt.Errorf("dispatch: no handler for action %q", action)
	}
	return h(ctx, payload)
}
