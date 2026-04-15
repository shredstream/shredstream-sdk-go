package shredstream

import "errors"

const (
	gapSkipThreshold   = 5
	maxAwaitingSkipped = 64
)

var errResync = errors.New("shredstream: resync needed")

type pendingShred struct {
	payload       []byte
	batchComplete bool
	lastInSlot    bool
}

type SlotAccumulator struct {
	pending            map[uint32]pendingShred
	decoder            *BatchDecoder
	nextDrain          uint32
	stallCount         uint32
	awaitingBatchStart bool
	awaitingSkipped    uint32
	SlotComplete       bool
}

func NewSlotAccumulator() *SlotAccumulator {
	return &SlotAccumulator{
		pending: make(map[uint32]pendingShred),
		decoder: NewBatchDecoder(),
	}
}

func (a *SlotAccumulator) Push(index uint32, payload []byte, batchComplete, lastInSlot bool) ([]Transaction, error) {
	if index < a.nextDrain {
		return nil, nil
	}
	if _, exists := a.pending[index]; exists {
		return nil, nil
	}

	ownedPayload := make([]byte, len(payload))
	copy(ownedPayload, payload)

	a.pending[index] = pendingShred{
		payload:       ownedPayload,
		batchComplete: batchComplete,
		lastInSlot:    lastInSlot,
	}

	allTxs, drained, err := a.drainConsecutive()
	if err != nil {
		return allTxs, err
	}

	if drained {
		a.stallCount = 0
	} else {
		a.stallCount++
		if a.stallCount >= gapSkipThreshold && len(a.pending) > 0 {
			minIdx := a.findMinPending()
			a.nextDrain = minIdx
			a.stallCount = 0
			a.decoder.Reset()
			a.awaitingBatchStart = true
			a.awaitingSkipped = 0

			moreTxs, _, err := a.drainConsecutive()
			if err != nil {
				return append(allTxs, moreTxs...), err
			}
			allTxs = append(allTxs, moreTxs...)
		}
	}

	return allTxs, nil
}

func (a *SlotAccumulator) drainConsecutive() ([]Transaction, bool, error) {
	var allTxs []Transaction
	drained := false

	for {
		shred, ok := a.pending[a.nextDrain]
		if !ok {
			break
		}

		delete(a.pending, a.nextDrain)
		a.nextDrain++
		drained = true

		if a.awaitingBatchStart {
			a.awaitingSkipped++
			if shred.batchComplete {
				a.awaitingBatchStart = false
				a.awaitingSkipped = 0
			} else if a.awaitingSkipped >= maxAwaitingSkipped {
				return allTxs, drained, errResync
			}
			continue
		}

		txs, err := a.decoder.Push(shred.payload)
		if err != nil {
			return allTxs, drained, err
		}
		allTxs = append(allTxs, txs...)

		if shred.batchComplete {
			a.decoder.Reset()
		}
		if shred.lastInSlot {
			a.SlotComplete = true
		}
	}

	return allTxs, drained, nil
}

func (a *SlotAccumulator) findMinPending() uint32 {
	var min uint32
	first := true
	for k := range a.pending {
		if first || k < min {
			min = k
			first = false
		}
	}
	return min
}
