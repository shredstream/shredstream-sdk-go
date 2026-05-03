package shredstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	maxPlausibleSlot         uint64 = 1 << 40
	maxSlotJumpForward       uint64 = 1_000_000
	maxBootstrapSlot         uint64 = 10_000_000_000
	blacklistExtraSlots      uint64 = 50
	completedRetentionSlots  uint64 = 1
)

type slotBatch struct {
	slot uint64
	txs  []VersionedTransaction
}

type Listener struct {
	mu sync.Mutex

	socket net.PacketConn
	pool   *bufferPool

	slots             map[uint64]*slotAccumulator
	recentlySeenSlots map[uint64]struct{}
	pendingBatches    []slotBatch

	maxAge                 uint64
	lastSlot               uint64
	enableFEC              bool
	disableSalvageDelivery bool
	accumulatorCfg         AccumulatorConfig

	rsCache *reedSolomonCache

	busyPollActive bool
	lastIOErrKind  string

	poolExhausted                        atomic.Uint64
	dataShredCount                       atomic.Uint64
	codeShredCount                       atomic.Uint64
	bytesReceived                        atomic.Uint64
	unparseableTooShort                  atomic.Uint64
	unparseableVariant                   atomic.Uint64
	unparseablePayload                   atomic.Uint64
	unparseableSlotRange                 atomic.Uint64
	droppedKnownSlots                    atomic.Uint64
	decodeErrorsTotal                    atomic.Uint64
	fecRecoveriesTotal                   atomic.Uint64
	fecRecoveryFailuresTotal             atomic.Uint64
	batchesSkippedTotal                  atomic.Uint64
	batchesDecodedStreamingTotal         atomic.Uint64
	batchesDecodedFallbackTotal          atomic.Uint64
	slotsCompletedTotal                  atomic.Uint64
	slotsEvictedByAge                    atomic.Uint64
	harvestedBatchesTotal                atomic.Uint64
	salvagedTailTxTotal                  atomic.Uint64
	fecSetsDiscardedUnusedTotal          atomic.Uint64
	fecSetsEvictedEarlyTotal             atomic.Uint64
	batchesForceFinalizedCorruptedTotal  atomic.Uint64
	batchesForceFinalizedTimeoutTotal    atomic.Uint64
}

func Bind(port int) (*Listener, error) {
	return BindWithOptions(port, DefaultListenerOptions())
}

func BindWithOptions(port int, opts ListenerOptions) (*Listener, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	listener := newListener(opts)
	listener.socket = conn

	if rawConn, errCtl := conn.SyscallConn(); errCtl == nil {
		busyOK := false
		_ = rawConn.Control(func(fd uintptr) {
			if opts.RecvBuf > 0 {
				_ = setRecvBuf(fd, opts.RecvBuf)
			}
			if opts.BusyPollMicros > 0 {
				if err := setBusyPoll(fd, opts.BusyPollMicros); err == nil {
					busyOK = true
				}
			}
		})
		listener.busyPollActive = busyOK
	}
	return listener, nil
}

func Offline() *Listener {
	return OfflineWithOptions(DefaultListenerOptions())
}

func OfflineWithOptions(opts ListenerOptions) *Listener {
	return newListener(opts)
}

func FromConn(conn net.PacketConn, opts ListenerOptions) *Listener {
	listener := newListener(opts)
	listener.socket = conn
	return listener
}

func newListener(opts ListenerOptions) *Listener {
	fmt.Fprintf(os.Stderr, "\x1b[2m⚡ShredStream.com SDK v%s initiated\x1b[0m\n", Version)
	pool := newBufferPool(opts.PoolSize)
	return &Listener{
		pool:                   pool,
		slots:                  make(map[uint64]*slotAccumulator),
		recentlySeenSlots:      make(map[uint64]struct{}),
		pendingBatches:         make([]slotBatch, 0, 32),
		maxAge:                 uint64(opts.MaxAge),
		enableFEC:              opts.EnableFEC,
		disableSalvageDelivery: opts.DisableSalvageDelivery,
		accumulatorCfg:         opts.Accumulator,
		rsCache:                newReedSolomonCache(),
	}
}

func (l *Listener) Close() error {
	if l.socket == nil {
		return nil
	}
	return l.socket.Close()
}

func (l *Listener) LocalAddr() (net.Addr, error) {
	if l.socket == nil {
		return nil, errors.New("listener: offline mode, no socket")
	}
	return l.socket.LocalAddr(), nil
}

func (l *Listener) BusyPollActive() bool { return l.busyPollActive }

func (l *Listener) LastIOErrorKind() string { return l.lastIOErrKind }

func (l *Listener) HandlePacket(raw []byte) (uint64, []VersionedTransaction, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handlePacketLocked(raw)
}

func (l *Listener) handlePacketLocked(raw []byte) (uint64, []VersionedTransaction, bool) {
	handle, ok := l.acquireBufferWithFallback()
	if !ok {
		return 0, nil, false
	}
	n := len(raw)
	if n > BufferSize {
		n = BufferSize
	}
	copy(handle.Mut()[:n], raw[:n])
	handle.SetLen(n)
	return l.handlePacketOwned(handle)
}

func (l *Listener) handlePacketOwned(handle *BufferHandle) (uint64, []VersionedTransaction, bool) {
	l.bytesReceived.Add(uint64(handle.Len()))

	kind, err := ParseKind(handle.Bytes())
	if err != nil {
		l.bumpParseError(err)
		handle.Release()
		return 0, nil, false
	}
	return l.handlePacketParsed(handle, kind)
}

func (l *Listener) handlePacketParsed(handle *BufferHandle, kind ShredKind) (uint64, []VersionedTransaction, bool) {
	if d, isData := kind.(DataShred); isData {
		slot := d.Slot
		index := d.Index
		fecSetIndex := d.FecSetIndex
		payloadEnd := uint16(DataHeaderSize + len(d.Payload))
		batchComplete := d.BatchComplete
		lastInSlot := d.LastInSlot

		if !l.acceptSlot(slot) {
			handle.Release()
			return 0, nil, false
		}
		if _, blacklisted := l.recentlySeenSlots[slot]; blacklisted {
			l.droppedKnownSlots.Add(1)
			handle.Release()
			return 0, nil, false
		}
		l.dataShredCount.Add(1)
		if slot > l.lastSlot {
			l.lastSlot = slot
			l.evictOldSlots()
		}
		acc := l.slots[slot]
		if acc == nil {
			acc = newSlotAccumulator(l.accumulatorCfg)
			acc.disableSalvageDelivery = l.disableSalvageDelivery
			l.slots[slot] = acc
		}
		txs := acc.pushData(index, fecSetIndex, handle, payloadEnd, batchComplete, lastInSlot, l.rsCache)
		if len(txs) == 0 {
			return 0, nil, false
		}
		return slot, txs, true
	}

	c, isCode := kind.(CodeShred)
	if !isCode {
		handle.Release()
		return 0, nil, false
	}
	slot := c.Slot
	if !l.acceptSlot(slot) {
		handle.Release()
		return 0, nil, false
	}
	if _, blacklisted := l.recentlySeenSlots[slot]; blacklisted {
		l.droppedKnownSlots.Add(1)
		handle.Release()
		return 0, nil, false
	}
	l.codeShredCount.Add(1)
	if !l.enableFEC {
		handle.Release()
		return 0, nil, false
	}
	if slot > l.lastSlot {
		l.lastSlot = slot
		l.evictOldSlots()
	}
	acc := l.slots[slot]
	if acc == nil {
		acc = newSlotAccumulator(l.accumulatorCfg)
		acc.disableSalvageDelivery = l.disableSalvageDelivery
		l.slots[slot] = acc
	}
	txs := acc.pushCode(c.FecSetIndex, c.NumDataShreds, c.NumCodingShreds, c.Position, c.Coded, c.Variant, l.rsCache)
	handle.Release()
	if len(txs) == 0 {
		return 0, nil, false
	}
	return slot, txs, true
}

func (l *Listener) bumpParseError(err error) {
	if errors.Is(err, ErrTooShort) {
		l.unparseableTooShort.Add(1)
		return
	}
	if errors.Is(err, ErrUnknownVariant) {
		l.unparseableVariant.Add(1)
		return
	}
	if errors.Is(err, ErrPayloadInvalid) {
		l.unparseablePayload.Add(1)
		return
	}
	l.unparseablePayload.Add(1)
}

const maxLiveSlots = 64

func (l *Listener) acceptSlot(slot uint64) bool {
	if slot > maxPlausibleSlot {
		l.unparseableSlotRange.Add(1)
		return false
	}
	if l.lastSlot == 0 && slot > maxBootstrapSlot {
		l.unparseableSlotRange.Add(1)
		return false
	}
	if l.lastSlot > 0 && slot > l.lastSlot && slot-l.lastSlot > maxSlotJumpForward {
		l.unparseableSlotRange.Add(1)
		return false
	}
	if _, alreadyKnown := l.slots[slot]; !alreadyKnown && len(l.slots) >= maxLiveSlots {
		l.unparseableSlotRange.Add(1)
		return false
	}
	return true
}

func (l *Listener) acquireBufferWithFallback() (*BufferHandle, bool) {
	if h, ok := l.pool.acquire(); ok {
		return h, true
	}
	l.evictOldSlots()
	if h, ok := l.pool.acquire(); ok {
		return h, true
	}
	l.forceEvictOldestSlot()
	if h, ok := l.pool.acquire(); ok {
		return h, true
	}
	l.poolExhausted.Add(1)
	return nil, false
}

func (l *Listener) evictOldSlots() {
	last := l.lastSlot
	maxAge := l.maxAge
	toEvict := make([]uint64, 0)
	for s, acc := range l.slots {
		threshold := maxAge
		if acc.slotComplete {
			threshold = completedRetentionSlots
		}
		if last > saturatingAddU64(s, threshold) {
			toEvict = append(toEvict, s)
		}
	}
	for _, s := range toEvict {
		acc := l.slots[s]
		if acc == nil {
			continue
		}
		delete(l.slots, s)
		preSalvage := acc.salvagedTailTx
		tail := acc.tryHarvestTail(l.rsCache)
		harvested := len(tail) > 0 || acc.salvagedTailTx > preSalvage
		if harvested {
			l.harvestedBatchesTotal.Add(1)
		}
		if len(tail) > 0 {
			l.pendingBatches = append(l.pendingBatches, slotBatch{slot: s, txs: tail})
		}
		if acc.slotComplete {
			l.slotsCompletedTotal.Add(1)
		} else {
			l.slotsEvictedByAge.Add(1)
		}
		l.rollupAccumulator(acc)
		acc.release()
		l.recentlySeenSlots[s] = struct{}{}
	}

	floor := saturatingSubU64(l.lastSlot, saturatingAddU64(l.maxAge, blacklistExtraSlots))
	if len(l.recentlySeenSlots) > 0 {
		for s := range l.recentlySeenSlots {
			if s < floor {
				delete(l.recentlySeenSlots, s)
			}
		}
	}
}

func (l *Listener) forceEvictOldestSlot() {
	first := true
	var oldest uint64
	for s := range l.slots {
		if first || s < oldest {
			oldest = s
			first = false
		}
	}
	if first {
		return
	}
	acc := l.slots[oldest]
	if acc == nil {
		return
	}
	delete(l.slots, oldest)
	preSalvage := acc.salvagedTailTx
	tail := acc.tryHarvestTail(l.rsCache)
	harvested := len(tail) > 0 || acc.salvagedTailTx > preSalvage
	if harvested {
		l.harvestedBatchesTotal.Add(1)
	}
	if len(tail) > 0 {
		l.pendingBatches = append(l.pendingBatches, slotBatch{slot: oldest, txs: tail})
	}
	if acc.slotComplete {
		l.slotsCompletedTotal.Add(1)
	} else {
		l.slotsEvictedByAge.Add(1)
	}
	l.rollupAccumulator(acc)
	acc.release()
	l.recentlySeenSlots[oldest] = struct{}{}
}

func (l *Listener) rollupAccumulator(acc *slotAccumulator) {
	l.decodeErrorsTotal.Add(uint64(acc.decodeErrors))
	l.fecRecoveriesTotal.Add(acc.recoveriesDone)
	l.fecRecoveryFailuresTotal.Add(acc.recoveryFailures)
	l.batchesSkippedTotal.Add(acc.batchesSkipped)
	l.batchesDecodedStreamingTotal.Add(acc.batchesDecodedStreaming)
	l.batchesDecodedFallbackTotal.Add(acc.batchesDecodedFallback)
	l.salvagedTailTxTotal.Add(acc.salvagedTailTx)
	l.fecSetsDiscardedUnusedTotal.Add(acc.fecSetsDiscardedUnused)
	l.fecSetsEvictedEarlyTotal.Add(acc.fecSetsEvictedEarly)
	l.batchesForceFinalizedCorruptedTotal.Add(acc.batchesForceFinalizedCorrupted)
	l.batchesForceFinalizedTimeoutTotal.Add(acc.batchesForceFinalizedTimeout)
}

func (l *Listener) Transactions(ctx context.Context) *TransactionIter {
	return &TransactionIter{listener: l, ctx: ctx}
}

func (l *Listener) Shreds(ctx context.Context) *ShredIter {
	return &ShredIter{listener: l, ctx: ctx, buf: make([]byte, BufferSize)}
}

func (l *Listener) recvOne() (uint64, []VersionedTransaction, bool, error) {
	l.mu.Lock()
	if len(l.pendingBatches) > 0 {
		b := l.pendingBatches[0]
		l.pendingBatches = l.pendingBatches[1:]
		l.mu.Unlock()
		return b.slot, b.txs, true, nil
	}
	if l.socket == nil {
		l.mu.Unlock()
		return 0, nil, false, errors.New("listener: offline mode, no socket")
	}
	handle, ok := l.acquireBufferWithFallback()
	socket := l.socket
	l.mu.Unlock()
	if !ok {
		dummy := make([]byte, BufferSize)
		_, _, _ = socket.ReadFrom(dummy)
		return 0, nil, false, nil
	}
	n, _, err := socket.ReadFrom(handle.Mut())
	if err != nil {
		handle.Release()
		return 0, nil, false, err
	}
	handle.SetLen(n)

	l.bytesReceived.Add(uint64(handle.Len()))
	kind, parseErr := ParseKind(handle.Bytes())
	if parseErr != nil {
		l.bumpParseError(parseErr)
		handle.Release()
		return 0, nil, false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	slot, txs, hit := l.handlePacketParsed(handle, kind)
	return slot, txs, hit, nil
}

type TransactionIter struct {
	listener *Listener
	ctx      context.Context
	slot     uint64
	txs      []VersionedTransaction
	err      error
	done     bool
}

func (it *TransactionIter) Next() bool {
	if it.done {
		return false
	}
	for {
		if it.ctx != nil {
			select {
			case <-it.ctx.Done():
				it.done = true
				return false
			default:
			}
		}
		slot, txs, ok, err := it.listener.recvOne()
		if err != nil {
			if isRecoverableIOError(err) {
				continue
			}
			it.listener.mu.Lock()
			it.listener.lastIOErrKind = err.Error()
			it.listener.mu.Unlock()
			it.err = err
			it.done = true
			return false
		}
		if !ok {
			continue
		}
		it.slot = slot
		it.txs = txs
		return true
	}
}

func (it *TransactionIter) Slot() uint64 { return it.slot }

func (it *TransactionIter) Txs() []VersionedTransaction { return it.txs }

func (it *TransactionIter) Err() error { return it.err }

func (it *TransactionIter) Listener() *Listener { return it.listener }

type ShredIter struct {
	listener *Listener
	ctx      context.Context
	buf      []byte
	parsed   ParsedShred
	err      error
	done     bool
}

type RawShred struct {
	Slot       uint64
	Index      uint32
	PayloadLen int
}

func (it *ShredIter) Next() bool {
	if it.done {
		return false
	}
	for {
		if it.ctx != nil {
			select {
			case <-it.ctx.Done():
				it.done = true
				return false
			default:
			}
		}
		if it.listener.socket == nil {
			it.err = errors.New("listener: offline mode, no socket")
			it.done = true
			return false
		}
		n, _, err := it.listener.socket.ReadFrom(it.buf)
		if err != nil {
			if isRecoverableIOError(err) {
				continue
			}
			it.listener.mu.Lock()
			it.listener.lastIOErrKind = err.Error()
			it.listener.mu.Unlock()
			it.err = err
			it.done = true
			return false
		}
		ps, ok := ParseShred(it.buf[:n])
		if !ok {
			continue
		}
		it.parsed = *ps
		return true
	}
}

func (it *ShredIter) Shred() RawShred {
	return RawShred{
		Slot:       it.parsed.Slot,
		Index:      it.parsed.Index,
		PayloadLen: len(it.parsed.Payload),
	}
}

func (it *ShredIter) Err() error { return it.err }

func isRecoverableIOError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
		return true
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "would block") || strings.Contains(msg, "interrupted") || strings.Contains(msg, "timed out") {
		return true
	}
	return false
}

func (l *Listener) SetReadDeadline(t time.Time) error {
	if l.socket == nil {
		return errors.New("listener: offline mode, no socket")
	}
	return l.socket.SetReadDeadline(t)
}


func (l *Listener) DataShredCountTotal() uint64 { return l.dataShredCount.Load() }

func (l *Listener) CodeShredCountTotal() uint64 { return l.codeShredCount.Load() }

func (l *Listener) BytesReceived() uint64 { return l.bytesReceived.Load() }

func (l *Listener) SlotCount() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.slots) }

func (l *Listener) PoolExhaustedCount() uint64 { return l.poolExhausted.Load() }

func (l *Listener) UnparseableTooShort() uint64 { return l.unparseableTooShort.Load() }

func (l *Listener) UnparseableVariant() uint64 { return l.unparseableVariant.Load() }

func (l *Listener) UnparseablePayload() uint64 { return l.unparseablePayload.Load() }

func (l *Listener) UnparseableSlotRange() uint64 { return l.unparseableSlotRange.Load() }

func (l *Listener) UnparseablePackets() uint64 {
	return l.unparseableTooShort.Load() + l.unparseableVariant.Load() +
		l.unparseablePayload.Load() + l.unparseableSlotRange.Load()
}

func (l *Listener) DroppedKnownSlots() uint64 { return l.droppedKnownSlots.Load() }

func (l *Listener) DecodeErrorsTotal() uint64 {
	t := l.decodeErrorsTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += uint64(a.decodeErrors)
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) FECRecoveriesTotal() uint64 {
	t := l.fecRecoveriesTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.recoveriesDone
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) FECRecoveryFailuresTotal() uint64 {
	t := l.fecRecoveryFailuresTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.recoveryFailures
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) BatchesSkippedTotal() uint64 {
	t := l.batchesSkippedTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.batchesSkipped
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) BatchesDecodedStreamingTotal() uint64 {
	t := l.batchesDecodedStreamingTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.batchesDecodedStreaming
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) BatchesDecodedFallbackTotal() uint64 {
	t := l.batchesDecodedFallbackTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.batchesDecodedFallback
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) SlotsCompletedTotal() uint64 { return l.slotsCompletedTotal.Load() }

func (l *Listener) SlotsEvictedByAge() uint64 { return l.slotsEvictedByAge.Load() }

func (l *Listener) HarvestedBatchesTotal() uint64 { return l.harvestedBatchesTotal.Load() }

func (l *Listener) SalvagedTailTxTotal() uint64 {
	t := l.salvagedTailTxTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.salvagedTailTx
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) FECSetsDiscardedUnusedTotal() uint64 {
	t := l.fecSetsDiscardedUnusedTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.fecSetsDiscardedUnused
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) FECSetsEvictedEarlyTotal() uint64 {
	t := l.fecSetsEvictedEarlyTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.fecSetsEvictedEarly
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) BatchesForceFinalizedCorruptedTotal() uint64 {
	t := l.batchesForceFinalizedCorruptedTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.batchesForceFinalizedCorrupted
	}
	l.mu.Unlock()
	return t
}

func (l *Listener) BatchesForceFinalizedTimeoutTotal() uint64 {
	t := l.batchesForceFinalizedTimeoutTotal.Load()
	l.mu.Lock()
	for _, a := range l.slots {
		t += a.batchesForceFinalizedTimeout
	}
	l.mu.Unlock()
	return t
}
