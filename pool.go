package shredstream

import "sync"

const BufferSize = 2048

type bufferPool struct {
	mu       sync.Mutex
	freelist []uint16
	buffers  [][]byte
	capacity int
}

const maxPoolCapacity = 65535

func newBufferPool(capacity int) *bufferPool {
	if capacity <= 0 || capacity > maxPoolCapacity {
		panic("pool capacity out of range")
	}
	free := make([]uint16, capacity)
	bufs := make([][]byte, capacity)
	for i := 0; i < capacity; i++ {
		free[capacity-1-i] = uint16(i)
		bufs[i] = make([]byte, BufferSize)
	}
	return &bufferPool{freelist: free, buffers: bufs, capacity: capacity}
}

func (p *bufferPool) acquire() (*BufferHandle, bool) {
	p.mu.Lock()
	if len(p.freelist) == 0 {
		p.mu.Unlock()
		return nil, false
	}
	idx := p.freelist[len(p.freelist)-1]
	p.freelist = p.freelist[:len(p.freelist)-1]
	p.mu.Unlock()
	return &BufferHandle{pool: p, idx: idx, buf: p.buffers[idx][:BufferSize], length: 0}, true
}

func (p *bufferPool) release(idx uint16) {
	p.mu.Lock()
	p.freelist = append(p.freelist, idx)
	p.mu.Unlock()
}

func (p *bufferPool) available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.freelist)
}

type BufferHandle struct {
	pool   *bufferPool
	idx    uint16
	buf    []byte
	length int
}

func (h *BufferHandle) Bytes() []byte {
	if h == nil {
		return nil
	}
	return h.buf[:h.length]
}

func (h *BufferHandle) Mut() []byte {
	return h.buf
}

func (h *BufferHandle) SetLen(n int) {
	if n < 0 || n > BufferSize {
		panic("BufferHandle.SetLen out of range")
	}
	h.length = n
}

func (h *BufferHandle) Len() int {
	if h == nil {
		return 0
	}
	return h.length
}

func (h *BufferHandle) Release() {
	if h == nil || h.pool == nil {
		return
	}
	pool := h.pool
	idx := h.idx
	h.pool = nil
	pool.release(idx)
}
