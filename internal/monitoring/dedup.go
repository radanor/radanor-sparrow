package monitoring

import (
	"container/list"
	"sync"
	"time"
)

type dedupEntry struct {
	key  string
	seen time.Time
}
type deduplicator struct {
	mu       sync.Mutex
	window   time.Duration
	capacity int
	entries  map[string]*list.Element
	order    *list.List
}

func newDeduplicator(window time.Duration, capacity int) *deduplicator {
	return &deduplicator{window: window, capacity: capacity, entries: make(map[string]*list.Element), order: list.New()}
}
func (d *deduplicator) duplicate(e Event, now time.Time) bool {
	if d.window <= 0 || d.capacity <= 0 || e.ID == "" {
		return false
	}
	key := e.Source + "\x00" + e.ID
	d.mu.Lock()
	defer d.mu.Unlock()
	for item := d.order.Back(); item != nil; {
		prev := item.Prev()
		entry := item.Value.(dedupEntry)
		if now.Sub(entry.seen) <= d.window {
			break
		}
		delete(d.entries, entry.key)
		d.order.Remove(item)
		item = prev
	}
	if item, ok := d.entries[key]; ok {
		entry := item.Value.(dedupEntry)
		if now.Sub(entry.seen) <= d.window {
			return true
		}
		delete(d.entries, key)
		d.order.Remove(item)
	}
	item := d.order.PushFront(dedupEntry{key: key, seen: now})
	d.entries[key] = item
	if d.order.Len() > d.capacity {
		oldest := d.order.Back()
		delete(d.entries, oldest.Value.(dedupEntry).key)
		d.order.Remove(oldest)
	}
	return false
}
