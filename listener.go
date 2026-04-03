package shredstream

import (
	"context"
	"fmt"
	"net"
	"sync"
)

const (
	defaultRecvBuf = 25 * 1024 * 1024
	defaultMaxAge  = 10
	maxPacketSize  = 1500
)

type ShredListener struct {
	port    int
	recvBuf int
	maxAge  uint64
	conn    *net.UDPConn
	slots    map[uint64]*SlotAccumulator
	lastSlot uint64
	onTx    func(uint64, []Transaction)
	onShred func(uint64, uint32, []byte)
	mu      sync.Mutex
	cancel  context.CancelFunc
}

type ListenerOption func(*ShredListener)

func WithRecvBuf(size int) ListenerOption {
	return func(l *ShredListener) {
		l.recvBuf = size
	}
}

func WithMaxAge(slots uint64) ListenerOption {
	return func(l *ShredListener) {
		l.maxAge = slots
	}
}

func NewListener(port int) (*ShredListener, error) {
	return NewListenerWithOptions(port)
}

func NewListenerWithOptions(port int, opts ...ListenerOption) (*ShredListener, error) {
	l := &ShredListener{
		port:    port,
		recvBuf: defaultRecvBuf,
		maxAge:  defaultMaxAge,
		slots:   make(map[uint64]*SlotAccumulator),
	}

	for _, opt := range opts {
		opt(l)
	}

	addr := &net.UDPAddr{Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp :%d: %w", port, err)
	}

	if err := conn.SetReadBuffer(l.recvBuf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set read buffer: %w", err)
	}

	l.conn = conn
	return l, nil
}

func (l *ShredListener) OnTransactions(fn func(uint64, []Transaction)) {
	l.onTx = fn
}

func (l *ShredListener) OnShred(fn func(uint64, uint32, []byte)) {
	l.onShred = fn
}

func (l *ShredListener) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	defer cancel()

	buf := make([]byte, maxPacketSize)

	for {
		select {
		case <-ctx.Done():
			l.conn.Close()
			return ctx.Err()
		default:
		}

		n, err := l.conn.Read(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				continue
			}
		}

		l.handlePacket(buf[:n])
	}
}

func (l *ShredListener) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
}

func (l *ShredListener) handlePacket(raw []byte) {
	shred := ParseShred(raw)
	if shred == nil {
		return
	}

	if l.onShred != nil {
		l.onShred(shred.Slot, shred.Index, shred.Payload)
	}

	l.mu.Lock()

	if shred.Slot > l.lastSlot {
		l.lastSlot = shred.Slot
	}

	l.evictOldSlots()

	acc, ok := l.slots[shred.Slot]
	if !ok {
		acc = NewSlotAccumulator()
		l.slots[shred.Slot] = acc
	}

	txs, err := acc.Push(shred.Index, shred.Payload, shred.BatchComplete, shred.LastInSlot)
	if err != nil {
		delete(l.slots, shred.Slot)
		l.mu.Unlock()
		if len(txs) > 0 {
			if l.onTx != nil {
				l.onTx(shred.Slot, txs)
			}
		}
		return
	}

	if acc.SlotComplete {
		delete(l.slots, shred.Slot)
	}

	l.mu.Unlock()

	if len(txs) > 0 {
		if l.onTx != nil {
			l.onTx(shred.Slot, txs)
		}
	}
}

func (l *ShredListener) evictOldSlots() {
	if l.lastSlot <= l.maxAge {
		return
	}
	cutoff := l.lastSlot - l.maxAge
	for slot := range l.slots {
		if slot < cutoff {
			delete(l.slots, slot)
		}
	}
}
