package btree

import "container/list"

// pool is a fixed-size LRU buffer pool. It is the part of the model that
// makes key locality visible: the tree itself does not care where keys land,
// but a bounded pool does, because scattered writes evict each other.
type pool struct {
	capacity int
	order    *list.List // most recent at the front
	entries  map[int]*list.Element
}

type frame struct {
	page  int
	dirty bool
}

func newPool(capacity int) *pool {
	if capacity < 4 {
		capacity = 4
	}
	return &pool{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[int]*list.Element, capacity),
	}
}

// touch records an access. It reports whether the page was already resident
// and whether admitting it forced a dirty page out, which is one write.
func (p *pool) touch(page int, dirty bool) (hit, evictedDirty bool) {
	if el, ok := p.entries[page]; ok {
		p.order.MoveToFront(el)
		f := el.Value.(*frame)
		f.dirty = f.dirty || dirty
		return true, false
	}

	if p.order.Len() >= p.capacity {
		last := p.order.Back()
		p.order.Remove(last)
		victim := last.Value.(*frame)
		delete(p.entries, victim.page)
		evictedDirty = victim.dirty
	}

	p.entries[page] = p.order.PushFront(&frame{page: page, dirty: dirty})
	return false, evictedDirty
}

// dirtyCount is how many resident pages would still have to be written on a
// final flush.
func (p *pool) dirtyCount() int {
	n := 0
	for el := p.order.Front(); el != nil; el = el.Next() {
		if el.Value.(*frame).dirty {
			n++
		}
	}
	return n
}
