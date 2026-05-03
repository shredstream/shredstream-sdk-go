package shredstream

import (
	"math"
	"sort"
	"time"
)

type payloadSource interface {
	slice() []byte
	release()
}

type poolPayload struct {
	handle *BufferHandle
}

func (p *poolPayload) slice() []byte {
	if p == nil || p.handle == nil {
		return nil
	}
	return p.handle.Bytes()
}

func (p *poolPayload) release() {
	if p == nil || p.handle == nil {
		return
	}
	p.handle.Release()
	p.handle = nil
}

type recoveredPayload struct {
	bytes []byte
}

func (r *recoveredPayload) slice() []byte {
	if r == nil {
		return nil
	}
	return r.bytes
}

func (r *recoveredPayload) release() {}

type pendingShred struct {
	source        payloadSource
	payloadEnd    uint16
	batchComplete bool
	fecSetIndex   uint32
}

func (p *pendingShred) payload() []byte {
	buf := p.source.slice()
	end := int(p.payloadEnd)
	if end < DataHeaderSize || end > len(buf) {
		return nil
	}
	return buf[DataHeaderSize:end]
}

type fecSetBuffer struct {
	fecSetIndex uint32
	numData     uint16
	numCoding   uint16
	shardSize   int
	variant     VariantKind
	hasVariant  bool
	code        map[uint16][]byte
}

func newFecSetBuffer(fecSetIndex uint32) *fecSetBuffer {
	return &fecSetBuffer{
		fecSetIndex: fecSetIndex,
		code:        make(map[uint16][]byte),
	}
}

func (f *fecSetBuffer) recordCode(position, numData, numCoding uint16, raw []byte, variant VariantKind) {
	if f.numData == 0 {
		f.numData = numData
	}
	if f.numCoding == 0 {
		f.numCoding = numCoding
	}
	if !f.hasVariant {
		f.variant = variant
		f.hasVariant = true
	}
	if f.shardSize == 0 && variant.kind == KindCodeMerkle {
		f.shardSize = ShardSize(variant.proofSize, variant.resigned)
	}
	if _, exists := f.code[position]; !exists {
		cp := make([]byte, len(raw))
		copy(cp, raw)
		f.code[position] = cp
	}
}

func (f *fecSetBuffer) canRecoverWith(dataReceived int) bool {
	return f.numData > 0 && dataReceived+len(f.code) >= int(f.numData)
}

func dataShardSlice(raw []byte, variant VariantKind) []byte {
	if variant.kind != KindDataMerkle {
		return nil
	}
	sz := ShardSize(variant.proofSize, variant.resigned)
	end := SizeOfSignature + sz
	if end > len(raw) {
		return nil
	}
	return raw[SizeOfSignature:end]
}

func codeShardSlice(raw []byte, variant VariantKind) []byte {
	if variant.kind != KindCodeMerkle {
		return nil
	}
	sz := ShardSize(variant.proofSize, variant.resigned)
	end := SizeOfCodingShredHeaders + sz
	if end > len(raw) {
		return nil
	}
	return raw[SizeOfCodingShredHeaders:end]
}

func variantAsData(v VariantKind) VariantKind {
	if v.kind == KindCodeMerkle {
		return VariantKind{kind: KindDataMerkle, proofSize: v.proofSize, resigned: v.resigned}
	}
	return v
}

type dataShredRef struct {
	pos uint16
	raw []byte
}

func (f *fecSetBuffer) tryReconstruct(dataShards []dataShredRef, cache *reedSolomonCache) ([]dataShredRef, error) {
	if f.numData == 0 || f.shardSize == 0 || !f.hasVariant {
		return nil, nil
	}
	if f.variant.kind != KindCodeMerkle {
		return nil, nil
	}
	nd := int(f.numData)
	nc := int(f.numCoding)
	total := nd + nc
	shardLen := f.shardSize

	shards := make([][]byte, total)
	present := make([]bool, total)

	for pos, raw := range f.code {
		idx := nd + int(pos)
		if idx >= total {
			continue
		}
		shard := codeShardSlice(raw, f.variant)
		if shard == nil || len(shard) != shardLen {
			continue
		}
		cp := make([]byte, shardLen)
		copy(cp, shard)
		shards[idx] = cp
		present[idx] = true
	}

	dataAlready := make(map[uint16]bool)
	for _, d := range dataShards {
		dataAlready[d.pos] = true
		p := int(d.pos)
		if p >= nd || present[p] {
			continue
		}
		shard := dataShardSlice(d.raw, variantAsData(f.variant))
		if shard == nil || len(shard) != shardLen {
			continue
		}
		cp := make([]byte, shardLen)
		copy(cp, shard)
		shards[p] = cp
		present[p] = true
	}

	have := 0
	for _, ok := range present {
		if ok {
			have++
		}
	}
	if have < nd {
		return nil, nil
	}

	rs, err := cache.GetOrBuild(f.numData, f.numCoding)
	if err != nil {
		return nil, err
	}
	for i := 0; i < total; i++ {
		if shards[i] == nil {
			shards[i] = make([]byte, shardLen)
		}
	}
	if err := rs.ReconstructData(shards, present); err != nil {
		return nil, err
	}

	out := make([]dataShredRef, 0)
	for pos := uint16(0); pos < uint16(nd); pos++ {
		if dataAlready[pos] {
			continue
		}
		shard := shards[pos]
		if len(shard) != shardLen {
			continue
		}
		raw := make([]byte, DataShredPayloadLen)
		copy(raw[SizeOfSignature:SizeOfSignature+shardLen], shard)
		out = append(out, dataShredRef{pos: pos, raw: raw})
	}
	return out, nil
}

type slotAccumulator struct {
	cfg AccumulatorConfig

	pending             map[uint32]*pendingShred
	fecSets             map[uint32]*fecSetBuffer
	batchCompleteIdx   map[uint32]time.Time

	streamingCursor  uint32
	streamingDecoder *StreamingDecoder
	yieldedSigs      map[Signature]struct{}

	batchStart   uint32
	slotComplete bool

	disableSalvageDelivery bool

	decodeErrors                    uint32
	recoveriesDone                  uint64
	recoveryFailures                uint64
	batchesDecodedStreaming         uint64
	batchesDecodedFallback          uint64
	batchesSkipped                  uint64
	fecSetsDiscardedUnused          uint64
	fecSetsEvictedEarly             uint64
	salvagedTailTx                  uint64
	batchesForceFinalizedCorrupted  uint64
	batchesForceFinalizedTimeout    uint64

	combinedScratch []byte
}

func newSlotAccumulator(cfg AccumulatorConfig) *slotAccumulator {
	return &slotAccumulator{
		cfg:              cfg,
		pending:          make(map[uint32]*pendingShred),
		fecSets:          make(map[uint32]*fecSetBuffer),
		batchCompleteIdx: make(map[uint32]time.Time),
		streamingDecoder: NewStreamingDecoder(),
		yieldedSigs:      make(map[Signature]struct{}, 512),
		combinedScratch:  make([]byte, 0, 24*1024),
	}
}

func (a *slotAccumulator) pushData(
	index uint32,
	fecSetIndex uint32,
	handle *BufferHandle,
	payloadEnd uint16,
	batchComplete bool,
	lastInSlot bool,
	rsCache *reedSolomonCache,
) []VersionedTransaction {
	if lastInSlot {
		a.slotComplete = true
		if handle != nil {
			handle.Release()
		}
		var out []VersionedTransaction
		a.tryFlushBatches(rsCache, &out)
		return out
	}
	if index < a.batchStart {
		if handle != nil {
			handle.Release()
		}
		return nil
	}
	if _, exists := a.pending[index]; exists {
		if handle != nil {
			handle.Release()
		}
		return nil
	}
	a.pending[index] = &pendingShred{
		source:        &poolPayload{handle: handle},
		payloadEnd:    payloadEnd,
		batchComplete: batchComplete,
		fecSetIndex:   fecSetIndex,
	}
	if batchComplete {
		if _, ok := a.batchCompleteIdx[index]; !ok {
			a.batchCompleteIdx[index] = time.Now()
		}
	}

	var out []VersionedTransaction
	a.advanceStreaming(&out)

	if a.streamingCursor <= index && a.tryFecFill(fecSetIndex, rsCache) {
		a.advanceStreaming(&out)
	}

	a.tryFlushBatches(rsCache, &out)
	return out
}

func (a *slotAccumulator) pushCode(
	fecSetIndex uint32,
	numData uint16,
	numCoding uint16,
	position uint16,
	coded []byte,
	variant VariantKind,
	rsCache *reedSolomonCache,
) []VersionedTransaction {
	if len(a.fecSets) >= a.cfg.MaxFECSetsPerSlot {
		if _, exists := a.fecSets[fecSetIndex]; !exists {
			oldest, ok := a.smallestFecSetKey()
			if ok && oldest < fecSetIndex {
				delete(a.fecSets, oldest)
				a.fecSetsEvictedEarly++
			}
		}
	}
	entry, ok := a.fecSets[fecSetIndex]
	if !ok {
		entry = newFecSetBuffer(fecSetIndex)
		a.fecSets[fecSetIndex] = entry
	}
	entry.recordCode(position, numData, numCoding, coded, variant)

	var out []VersionedTransaction
	if a.tryFecFill(fecSetIndex, rsCache) {
		a.advanceStreaming(&out)
	}
	a.tryFlushBatches(rsCache, &out)
	return out
}

func (a *slotAccumulator) smallestFecSetKey() (uint32, bool) {
	first := true
	var min uint32
	for k := range a.fecSets {
		if first || k < min {
			min = k
			first = false
		}
	}
	return min, !first
}

func (a *slotAccumulator) advanceStreaming(out *[]VersionedTransaction) {
	for {
		ps, ok := a.pending[a.streamingCursor]
		if !ok {
			return
		}
		if _, isRecovered := ps.source.(*recoveredPayload); isRecovered {
			return
		}
		batchCompleteFlag := ps.batchComplete
		payload := ps.payload()
		if payload == nil {
			droppedIdx := a.streamingCursor
			ps.source.release()
			delete(a.pending, droppedIdx)
			if batchCompleteFlag {
				delete(a.batchCompleteIdx, droppedIdx)
			}
			a.streamingCursor++
			a.decodeErrors++

			if batchCompleteFlag && droppedIdx > a.batchStart {
				salvaged := a.salvageContiguousRunsIn(a.batchStart, droppedIdx-1, out)
				if salvaged > 0 {
					a.batchesDecodedFallback++
				} else {
					a.batchesSkipped++
				}
				a.batchesForceFinalizedCorrupted++
				a.completeBatch(droppedIdx)
			}
			continue
		}
		txs, err := a.streamingDecoder.Push(payload)
		a.streamingCursor++
		if err != nil {
			a.decodeErrors++
			a.streamingDecoder.Reset()
			return
		}
		for _, tx := range txs {
			if a.recordSig(tx) {
				*out = append(*out, tx)
			}
		}
		if batchCompleteFlag {
			return
		}
	}
}

func (a *slotAccumulator) tryFecFill(hint uint32, rsCache *reedSolomonCache) bool {
	setIdx, ok := a.selectRecoverableSet(hint)
	if !ok {
		return false
	}
	fec := a.fecSets[setIdx]
	if fec == nil {
		return false
	}
	nd := int(fec.numData)
	if nd == 0 {
		return false
	}

	dataRefs := make([]dataShredRef, 0)
	for idx, ps := range a.pending {
		if idx >= setIdx && idx-setIdx < uint32(nd) && ps.fecSetIndex == setIdx {
			dataRefs = append(dataRefs, dataShredRef{pos: uint16(idx - setIdx), raw: ps.source.slice()})
		}
	}
	recovered, err := fec.tryReconstruct(dataRefs, rsCache)
	if err != nil {
		a.recoveryFailures++
		return false
	}
	if len(recovered) == 0 {
		return false
	}

	any := false
	for _, r := range recovered {
		globalIdx := setIdx + uint32(r.pos)
		if globalIdx < a.batchStart {
			continue
		}
		if _, exists := a.pending[globalIdx]; exists {
			continue
		}
		kind, parseErr := ParseKind(r.raw)
		if parseErr != nil {
			continue
		}
		d, isData := kind.(DataShred)
		if !isData {
			continue
		}
		if d.LastInSlot {
			a.slotComplete = true
			continue
		}
		payloadEnd := uint16(DataHeaderSize + len(d.Payload))
		a.pending[globalIdx] = &pendingShred{
			source:        &recoveredPayload{bytes: r.raw},
			payloadEnd:    payloadEnd,
			batchComplete: d.BatchComplete,
			fecSetIndex:   setIdx,
		}
		if d.BatchComplete {
			if _, ok := a.batchCompleteIdx[globalIdx]; !ok {
				a.batchCompleteIdx[globalIdx] = time.Now()
			}
		}
		any = true
	}
	if any {
		a.recoveriesDone++
		delete(a.fecSets, setIdx)
	}
	return any
}

func (a *slotAccumulator) selectRecoverableSet(hint uint32) (uint32, bool) {
	cursor := a.streamingCursor
	dataCountFor := func(setIdx uint32, fec *fecSetBuffer) int {
		nd := uint32(fec.numData)
		if nd == 0 {
			return 0
		}
		count := 0
		for idx, ps := range a.pending {
			if idx >= setIdx && idx < setIdx+nd && ps.fecSetIndex == setIdx {
				count++
			}
		}
		return count
	}
	if fec, ok := a.fecSets[hint]; ok {
		if fec.canRecoverWith(dataCountFor(hint, fec)) {
			return hint, true
		}
	}
	keys := make([]uint32, 0, len(a.fecSets))
	for k := range a.fecSets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, setIdx := range keys {
		fec := a.fecSets[setIdx]
		nd := uint32(fec.numData)
		if nd == 0 {
			continue
		}
		if setIdx <= cursor && cursor < setIdx+nd && fec.canRecoverWith(dataCountFor(setIdx, fec)) {
			return setIdx, true
		}
	}
	return 0, false
}

func (a *slotAccumulator) tryFlushBatches(rsCache *reedSolomonCache, out *[]VersionedTransaction) {
	for {
		bcIdx, firstSeen, ok := a.smallestBatchCompleteIdxFrom(a.batchStart)
		if !ok {
			return
		}
		start := a.batchStart
		expected := int(bcIdx-start) + 1

		present := a.countPendingInRange(start, bcIdx)
		if present < expected {
			hints := make([]uint32, 0)
			for f, fec := range a.fecSets {
				nd := uint32(fec.numData)
				if nd != 0 && f+nd > start && f <= bcIdx {
					hints = append(hints, f)
				}
			}
			for _, h := range hints {
				a.tryFecFill(h, rsCache)
			}
			present = a.countPendingInRange(start, bcIdx)
		}

		if present < expected {
			if time.Since(firstSeen) >= a.cfg.StuckBatchTimeout {
				salvaged := a.salvageContiguousRunsIn(start, bcIdx, out)
				if salvaged > 0 {
					a.batchesDecodedFallback++
				} else {
					a.batchesSkipped++
				}
				a.batchesForceFinalizedTimeout++
				a.completeBatch(bcIdx)
				continue
			}
			return
		}

		a.combinedScratch = a.combinedScratch[:0]
		for i := start; i <= bcIdx; i++ {
			ps := a.pending[i]
			if ps == nil {
				continue
			}
			p := ps.payload()
			if p != nil {
				a.combinedScratch = append(a.combinedScratch, p...)
			}
		}
		txs, err := DecodeBatch(a.combinedScratch)
		if err != nil || txs == nil {
			a.decodeErrors++
			a.batchesSkipped++
		} else {
			anyNew := false
			for _, tx := range txs {
				if a.recordSig(tx) {
					*out = append(*out, tx)
					anyNew = true
				}
			}
			if anyNew {
				a.batchesDecodedFallback++
			} else {
				a.batchesDecodedStreaming++
			}
		}
		a.completeBatch(bcIdx)
	}
}

func (a *slotAccumulator) countPendingInRange(start, end uint32) int {
	count := 0
	for i := start; i <= end; i++ {
		if _, ok := a.pending[i]; ok {
			count++
		}
		if i == end {
			break
		}
	}
	return count
}

func (a *slotAccumulator) smallestBatchCompleteIdxFrom(start uint32) (uint32, time.Time, bool) {
	first := true
	var minIdx uint32
	var minTime time.Time
	for idx, t := range a.batchCompleteIdx {
		if idx < start {
			continue
		}
		if first || idx < minIdx {
			minIdx = idx
			minTime = t
			first = false
		}
	}
	if first {
		return 0, time.Time{}, false
	}
	return minIdx, minTime, true
}

func (a *slotAccumulator) completeBatch(bcIdx uint32) {
	nextStart := saturatingAddU32(bcIdx, 1)
	for k, ps := range a.pending {
		if k < nextStart {
			ps.source.release()
			delete(a.pending, k)
		}
	}
	for k := range a.batchCompleteIdx {
		if k < nextStart {
			delete(a.batchCompleteIdx, k)
		}
	}
	for setIdx, fec := range a.fecSets {
		nd := uint32(fec.numData)
		if nd > 0 && setIdx+nd <= nextStart {
			delete(a.fecSets, setIdx)
		}
	}
	a.batchStart = nextStart
	if a.streamingCursor < nextStart {
		a.streamingCursor = nextStart
	}
	a.streamingDecoder.Reset()
	clear(a.yieldedSigs)
}

func (a *slotAccumulator) tryHarvestTail(rsCache *reedSolomonCache) []VersionedTransaction {
	var out []VersionedTransaction

	hints := make([]uint32, 0, len(a.fecSets))
	for k := range a.fecSets {
		hints = append(hints, k)
	}
	for _, h := range hints {
		_ = a.tryFecFill(h, rsCache)
	}

	boundaries := make([]uint32, 0)
	for idx, ps := range a.pending {
		if ps.batchComplete && idx >= a.batchStart {
			boundaries = append(boundaries, idx)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })

	for _, bcIdx := range boundaries {
		start := a.batchStart
		if bcIdx < start {
			continue
		}
		contiguous := true
		for i := start; i <= bcIdx; i++ {
			if _, ok := a.pending[i]; !ok {
				contiguous = false
				break
			}
			if i == bcIdx {
				break
			}
		}
		if contiguous {
			a.combinedScratch = a.combinedScratch[:0]
			for i := start; i <= bcIdx; i++ {
				ps := a.pending[i]
				if ps == nil {
					continue
				}
				p := ps.payload()
				if p != nil {
					a.combinedScratch = append(a.combinedScratch, p...)
				}
			}
			txs, err := DecodeBatch(a.combinedScratch)
			if err != nil || txs == nil {
				a.decodeErrors++
				a.batchesSkipped++
			} else {
				anyNew := false
				for _, tx := range txs {
					if a.recordSig(tx) {
						out = append(out, tx)
						anyNew = true
					}
				}
				if anyNew {
					a.batchesDecodedFallback++
				} else {
					a.batchesDecodedStreaming++
				}
			}
		} else {
			salvaged := a.salvageContiguousRunsIn(start, bcIdx, &out)
			if salvaged > 0 {
				a.batchesDecodedFallback++
			} else {
				a.batchesSkipped++
			}
		}
		a.completeBatch(bcIdx)
	}

	if len(a.pending) > 0 {
		indices := make([]uint32, 0, len(a.pending))
		for k := range a.pending {
			indices = append(indices, k)
		}
		sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

		i := 0
		for i < len(indices) {
			runStart := indices[i]
			a.combinedScratch = a.combinedScratch[:0]
			expected := runStart
			for i < len(indices) && indices[i] == expected {
				ps := a.pending[indices[i]]
				if ps != nil {
					if p := ps.payload(); p != nil {
						a.combinedScratch = append(a.combinedScratch, p...)
					}
				}
				expected = indices[i] + 1
				i++
			}
			if len(a.combinedScratch) > 0 {
				for _, tx := range DecodeConcatenated(a.combinedScratch) {
					if a.recordSig(tx) {
						out = append(out, tx)
					}
				}
			}
		}
	}

	a.fecSetsDiscardedUnused += uint64(len(a.fecSets))
	a.salvagedTailTx += uint64(len(out))

	if a.disableSalvageDelivery {
		return nil
	}
	return out
}

func (a *slotAccumulator) salvageContiguousRunsIn(start, end uint32, out *[]VersionedTransaction) int {
	salvaged := 0
	cursor := start
	advance := func(c uint32) (uint32, bool) {
		if c == math.MaxUint32 {
			return c, true
		}
		return c + 1, false
	}
	for cursor <= end {
		for cursor <= end {
			if _, ok := a.pending[cursor]; ok {
				break
			}
			next, hit := advance(cursor)
			if hit {
				return salvaged
			}
			cursor = next
		}
		if cursor > end {
			break
		}
		a.combinedScratch = a.combinedScratch[:0]
		runDone := false
		for cursor <= end {
			ps, ok := a.pending[cursor]
			if !ok {
				break
			}
			if p := ps.payload(); p != nil {
				a.combinedScratch = append(a.combinedScratch, p...)
			}
			next, hit := advance(cursor)
			if hit {
				runDone = true
				break
			}
			cursor = next
		}
		if len(a.combinedScratch) > 0 {
			for _, tx := range DecodeConcatenated(a.combinedScratch) {
				if a.recordSig(tx) {
					*out = append(*out, tx)
					salvaged++
				}
			}
		}
		if runDone {
			return salvaged
		}
	}
	return salvaged
}

func (a *slotAccumulator) recordSig(tx VersionedTransaction) bool {
	if len(tx.Signatures) == 0 {
		return false
	}
	sig := tx.Signatures[0]
	if _, ok := a.yieldedSigs[sig]; ok {
		return false
	}
	a.yieldedSigs[sig] = struct{}{}
	return true
}

func (a *slotAccumulator) release() {
	for _, ps := range a.pending {
		ps.source.release()
	}
	a.pending = make(map[uint32]*pendingShred)
	a.fecSets = make(map[uint32]*fecSetBuffer)
	a.batchCompleteIdx = make(map[uint32]time.Time)
}
