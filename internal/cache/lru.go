package cache

import "container/list"

// lruList tracks eviction order using a doubly-linked list.
// The front element is the most recently used; the back element is oldest.
// All methods must be called with the caller's lock held.
type lruList struct {
	l    *list.List
	keys map[string]*list.Element
}

func newLRUList() *lruList {
	return &lruList{
		l:    list.New(),
		keys: make(map[string]*list.Element),
	}
}

// touch moves key to the front (most recently used).
func (r *lruList) touch(key string) {
	if el, ok := r.keys[key]; ok {
		r.l.MoveToFront(el)
	}
}

// add inserts key at the front. key must not already be present.
func (r *lruList) add(key string) {
	el := r.l.PushFront(key)
	r.keys[key] = el
}

// remove deletes key from the list.
func (r *lruList) remove(key string) {
	if el, ok := r.keys[key]; ok {
		r.l.Remove(el)
		delete(r.keys, key)
	}
}

// evictOldest removes and returns the oldest key (back of the list).
// Returns "" if the list is empty.
func (r *lruList) evictOldest() string {
	back := r.l.Back()
	if back == nil {
		return ""
	}
	key := back.Value.(string)
	r.l.Remove(back)
	delete(r.keys, key)
	return key
}

// len returns the number of entries tracked.
func (r *lruList) len() int { return r.l.Len() }
