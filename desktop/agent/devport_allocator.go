package main

import (
	"fmt"
	"net"
	"sync"
)

// DevPortAllocator hands out (Metro, ExpoWeb) port pairs to user
// sessions on a shared box so two simultaneous users can run their
// own dev servers without colliding on the canonical 8081/19006.
//
// Ranges (chosen to not collide with the typical dev ecosystem):
//
//   Metro    : 8081 + offset       — base 8081, range 8081..8090
//   ExpoWeb  : 19006 + offset      — base 19006, range 19006..19015
//
// In single-user mode the allocator is unused; the legacy singleton
// path keeps using 8081 / 19006. In multi-user mode each UserSession
// reserves a slot on first dev-server start and holds it until the
// session is stopped or evicted.
type DevPortAllocator struct {
	mu       sync.Mutex
	metroBase int
	webBase   int
	maxSlots  int
	taken     map[int]string // slot index → userID that owns it
}

// NewDevPortAllocator creates an allocator with the default ranges.
func NewDevPortAllocator() *DevPortAllocator {
	return &DevPortAllocator{
		metroBase: 8081,
		webBase:   19006,
		maxSlots:  10,
		taken:     make(map[int]string),
	}
}

// DevPortPair holds the two ports allocated to one user session.
type DevPortPair struct {
	Slot     int
	MetroPort int
	WebPort  int
}

// Reserve picks the lowest free slot, marks it owned by userID,
// and returns the corresponding (Metro, ExpoWeb) ports. Returns
// the existing slot if userID already holds one.
func (a *DevPortAllocator) Reserve(userID string) (DevPortPair, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for slot, owner := range a.taken {
		if owner == userID {
			return DevPortPair{
				Slot:      slot,
				MetroPort: a.metroBase + slot,
				WebPort:   a.webBase + slot,
			}, nil
		}
	}
	for slot := 0; slot < a.maxSlots; slot++ {
		if _, used := a.taken[slot]; used {
			continue
		}
		metro := a.metroBase + slot
		web := a.webBase + slot
		// Probe the OS — slot may be free in our table but a stale
		// process is squatting the port. Skip in that case.
		if portBusy(metro) || portBusy(web) {
			continue
		}
		a.taken[slot] = userID
		return DevPortPair{
			Slot:      slot,
			MetroPort: metro,
			WebPort:   web,
		}, nil
	}
	return DevPortPair{}, fmt.Errorf("no free dev-server port slot (limit=%d)", a.maxSlots)
}

// Release frees the slot held by userID, if any. Idempotent.
func (a *DevPortAllocator) Release(userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for slot, owner := range a.taken {
		if owner == userID {
			delete(a.taken, slot)
			return
		}
	}
}

// Snapshot returns a copy of the current allocations for /info-style
// reporting. Slots without an owner are omitted.
func (a *DevPortAllocator) Snapshot() []DevPortPair {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]DevPortPair, 0, len(a.taken))
	for slot := range a.taken {
		out = append(out, DevPortPair{
			Slot:      slot,
			MetroPort: a.metroBase + slot,
			WebPort:   a.webBase + slot,
		})
	}
	return out
}

func portBusy(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}
