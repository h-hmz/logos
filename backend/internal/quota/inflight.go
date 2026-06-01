package quota

import "sync"

// InFlight tracks which users currently have a generation executing, so a
// single user can't run concurrent generations to slip past the pre-flight
// budget check. It is process-local by design (ADR 0009): across N server
// instances a user may have up to N generations in flight, which is accepted.
type InFlight struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// NewInFlight returns an empty in-flight tracker.
func NewInFlight() *InFlight {
	return &InFlight{held: make(map[string]struct{})}
}

// TryAcquire marks uid as in-flight and returns true. If uid already has a
// generation in flight it returns false without blocking, so the caller can
// reject the request (HTTP 409) rather than queue behind a slow LLM call.
func (f *InFlight) TryAcquire(uid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, held := f.held[uid]; held {
		return false
	}
	f.held[uid] = struct{}{}
	return true
}

// Release clears the in-flight mark for uid. Safe to call even if uid was not
// held. Callers should defer this immediately after a successful TryAcquire.
func (f *InFlight) Release(uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, uid)
}
