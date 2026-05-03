package shredstream

import (
	"encoding/binary"
	"fmt"
)

const maxEntryCount uint64 = 50_000

const initialBufferCapacity = 64 * 1024

type Signature [64]byte
type Pubkey [32]byte
type Hash [32]byte

type MessageHeader struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
}

type CompiledInstruction struct {
	ProgramIDIndex uint8
	Accounts       []byte
	Data           []byte
}

type MessageAddressTableLookup struct {
	AccountKey      Pubkey
	WritableIndexes []byte
	ReadonlyIndexes []byte
}

type VersionedMessage struct {
	IsV0                bool
	Header              MessageHeader
	AccountKeys         []Pubkey
	RecentBlockhash     Hash
	Instructions        []CompiledInstruction
	AddressTableLookups []MessageAddressTableLookup
}

type VersionedTransaction struct {
	Signatures []Signature
	Message    VersionedMessage
}

type StreamingDecoder struct {
	buffer           []byte
	cursor           int
	expectedCount    uint64
	expectedCountSet bool
	entriesYielded   uint64
}

func NewStreamingDecoder() *StreamingDecoder {
	return &StreamingDecoder{
		buffer: make([]byte, 0, initialBufferCapacity),
	}
}

func (d *StreamingDecoder) Reset() {
	d.buffer = d.buffer[:0]
	d.cursor = 0
	d.expectedCount = 0
	d.expectedCountSet = false
	d.entriesYielded = 0
}

func (d *StreamingDecoder) Push(payload []byte) ([]VersionedTransaction, error) {
	d.buffer = append(d.buffer, payload...)
	return d.tryDeserialize()
}

func (d *StreamingDecoder) tryDeserialize() ([]VersionedTransaction, error) {
	if !d.expectedCountSet && len(d.buffer) >= d.cursor+8 {
		count := binary.LittleEndian.Uint64(d.buffer[d.cursor : d.cursor+8])
		if count > maxEntryCount {
			return nil, &DecodeError{Msg: "invalid entry count"}
		}
		d.cursor += 8
		d.expectedCount = count
		d.expectedCountSet = true
	}

	if !d.expectedCountSet {
		return nil, nil
	}

	var txs []VersionedTransaction
	for d.entriesYielded < d.expectedCount {
		if d.cursor >= len(d.buffer) {
			break
		}
		consumed, entryTxs, complete, err := decodeEntry(d.buffer[d.cursor:])
		if err != nil {
			return nil, err
		}
		if !complete {
			break
		}
		d.cursor += consumed
		d.entriesYielded++
		txs = append(txs, entryTxs...)
	}
	return txs, nil
}

func DecodeBatch(b []byte) ([]VersionedTransaction, error) {
	if !validateVecPrefix(b) {
		return nil, nil
	}
	count := binary.LittleEndian.Uint64(b[:8])
	pos := 8
	var txs []VersionedTransaction
	for i := uint64(0); i < count; i++ {
		if pos >= len(b) {
			return nil, &DecodeError{Msg: "truncated batch"}
		}
		consumed, entryTxs, complete, err := decodeEntry(b[pos:])
		if err != nil {
			return nil, err
		}
		if !complete {
			return nil, &DecodeError{Msg: "truncated entry"}
		}
		pos += consumed
		txs = append(txs, entryTxs...)
	}
	return txs, nil
}

func DecodeConcatenated(b []byte) []VersionedTransaction {
	var out []VersionedTransaction
	offset := 0
	for offset+8 <= len(b) {
		remaining := b[offset:]
		if !validateVecPrefix(remaining) {
			break
		}
		count := binary.LittleEndian.Uint64(remaining[:8])
		pos := 8
		var batchTxs []VersionedTransaction
		ok := true
		for i := uint64(0); i < count; i++ {
			if pos >= len(remaining) {
				ok = false
				break
			}
			consumed, entryTxs, complete, err := decodeEntry(remaining[pos:])
			if err != nil || !complete {
				ok = false
				break
			}
			pos += consumed
			batchTxs = append(batchTxs, entryTxs...)
		}
		if !ok || pos == 0 {
			break
		}
		out = append(out, batchTxs...)
		offset += pos
	}
	return out
}

func validateVecPrefix(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	count := binary.LittleEndian.Uint64(b[:8])
	if count > maxEntryCount {
		return false
	}
	if count > 0 && count*48 > uint64(len(b)) {
		return false
	}
	return true
}

func decodeEntry(b []byte) (int, []VersionedTransaction, bool, error) {
	if len(b) < 8+32+8 {
		return 0, nil, false, nil
	}
	pos := 0
	pos += 8
	pos += 32
	txCount := binary.LittleEndian.Uint64(b[pos : pos+8])
	pos += 8
	remaining := uint64(len(b) - pos)
	if txCount > remaining {
		return 0, nil, false, &DecodeError{Msg: fmt.Sprintf("invalid tx count: %d", txCount)}
	}
	txs := make([]VersionedTransaction, 0, txCount)
	for i := uint64(0); i < txCount; i++ {
		consumed, tx, complete, err := decodeTransaction(b[pos:])
		if err != nil {
			return 0, nil, false, err
		}
		if !complete {
			return 0, nil, false, nil
		}
		pos += consumed
		txs = append(txs, tx)
	}
	return pos, txs, true, nil
}

func decodeTransaction(b []byte) (int, VersionedTransaction, bool, error) {
	pos := 0
	sigCount, n := decodeCompactU16(b, pos)
	if n < 0 {
		return 0, VersionedTransaction{}, false, nil
	}
	pos += n
	if sigCount > 256 {
		return 0, VersionedTransaction{}, false, &DecodeError{Msg: "sig count too large"}
	}
	if pos+sigCount*64 > len(b) {
		return 0, VersionedTransaction{}, false, nil
	}
	sigs := make([]Signature, sigCount)
	for i := 0; i < sigCount; i++ {
		copy(sigs[i][:], b[pos:pos+64])
		pos += 64
	}
	consumed, msg, complete, err := decodeMessage(b[pos:])
	if err != nil {
		return 0, VersionedTransaction{}, false, err
	}
	if !complete {
		return 0, VersionedTransaction{}, false, nil
	}
	pos += consumed
	return pos, VersionedTransaction{Signatures: sigs, Message: msg}, true, nil
}

func decodeMessage(b []byte) (int, VersionedMessage, bool, error) {
	if len(b) < 1 {
		return 0, VersionedMessage{}, false, nil
	}
	pos := 0
	first := b[pos]
	isV0 := first&0x80 != 0
	if isV0 {
		if first&0x7F != 0 {
			return 0, VersionedMessage{}, false, &DecodeError{Msg: "unknown message version"}
		}
		pos++
	}
	if pos+3 > len(b) {
		return 0, VersionedMessage{}, false, nil
	}
	header := MessageHeader{
		NumRequiredSignatures:       b[pos],
		NumReadonlySignedAccounts:   b[pos+1],
		NumReadonlyUnsignedAccounts: b[pos+2],
	}
	pos += 3

	acctCount, n := decodeCompactU16(b, pos)
	if n < 0 {
		return 0, VersionedMessage{}, false, nil
	}
	pos += n
	if pos+acctCount*32 > len(b) {
		return 0, VersionedMessage{}, false, nil
	}
	accountKeys := make([]Pubkey, acctCount)
	for i := 0; i < acctCount; i++ {
		copy(accountKeys[i][:], b[pos:pos+32])
		pos += 32
	}

	if pos+32 > len(b) {
		return 0, VersionedMessage{}, false, nil
	}
	var blockhash Hash
	copy(blockhash[:], b[pos:pos+32])
	pos += 32

	ixCount, n := decodeCompactU16(b, pos)
	if n < 0 {
		return 0, VersionedMessage{}, false, nil
	}
	pos += n
	instructions := make([]CompiledInstruction, 0, ixCount)
	for i := 0; i < ixCount; i++ {
		if pos+1 > len(b) {
			return 0, VersionedMessage{}, false, nil
		}
		programID := b[pos]
		pos++
		acctLen, n := decodeCompactU16(b, pos)
		if n < 0 {
			return 0, VersionedMessage{}, false, nil
		}
		pos += n
		if pos+acctLen > len(b) {
			return 0, VersionedMessage{}, false, nil
		}
		accounts := make([]byte, acctLen)
		copy(accounts, b[pos:pos+acctLen])
		pos += acctLen

		dataLen, n := decodeCompactU16(b, pos)
		if n < 0 {
			return 0, VersionedMessage{}, false, nil
		}
		pos += n
		if pos+dataLen > len(b) {
			return 0, VersionedMessage{}, false, nil
		}
		data := make([]byte, dataLen)
		copy(data, b[pos:pos+dataLen])
		pos += dataLen

		instructions = append(instructions, CompiledInstruction{
			ProgramIDIndex: programID,
			Accounts:       accounts,
			Data:           data,
		})
	}

	var atls []MessageAddressTableLookup
	if isV0 {
		atlCount, n := decodeCompactU16(b, pos)
		if n < 0 {
			return 0, VersionedMessage{}, false, nil
		}
		pos += n
		atls = make([]MessageAddressTableLookup, 0, atlCount)
		for i := 0; i < atlCount; i++ {
			if pos+32 > len(b) {
				return 0, VersionedMessage{}, false, nil
			}
			var key Pubkey
			copy(key[:], b[pos:pos+32])
			pos += 32

			wLen, n := decodeCompactU16(b, pos)
			if n < 0 {
				return 0, VersionedMessage{}, false, nil
			}
			pos += n
			if pos+wLen > len(b) {
				return 0, VersionedMessage{}, false, nil
			}
			writable := make([]byte, wLen)
			copy(writable, b[pos:pos+wLen])
			pos += wLen

			rLen, n := decodeCompactU16(b, pos)
			if n < 0 {
				return 0, VersionedMessage{}, false, nil
			}
			pos += n
			if pos+rLen > len(b) {
				return 0, VersionedMessage{}, false, nil
			}
			readonly := make([]byte, rLen)
			copy(readonly, b[pos:pos+rLen])
			pos += rLen

			atls = append(atls, MessageAddressTableLookup{
				AccountKey:      key,
				WritableIndexes: writable,
				ReadonlyIndexes: readonly,
			})
		}
	}

	return pos, VersionedMessage{
		IsV0:                isV0,
		Header:              header,
		AccountKeys:         accountKeys,
		RecentBlockhash:     blockhash,
		Instructions:        instructions,
		AddressTableLookups: atls,
	}, true, nil
}

func decodeCompactU16(b []byte, pos int) (int, int) {
	if pos >= len(b) {
		return 0, -1
	}
	b0 := b[pos]
	if b0 < 0x80 {
		return int(b0), 1
	}
	if pos+1 >= len(b) {
		return 0, -1
	}
	b1 := b[pos+1]
	if b1 < 0x80 {
		return int(b0&0x7F) | (int(b1) << 7), 2
	}
	if pos+2 >= len(b) {
		return 0, -1
	}
	b2 := b[pos+2]
	if b2 > 0x03 {
		return 0, -1
	}
	return int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2&0x03) << 14), 3
}

func encodeCompactU16(v int) []byte {
	if v < 0x80 {
		return []byte{byte(v)}
	}
	if v < 0x4000 {
		return []byte{byte(v&0x7F) | 0x80, byte((v >> 7) & 0xFF)}
	}
	return []byte{byte(v&0x7F) | 0x80, byte(((v >> 7) & 0x7F) | 0x80), byte((v >> 14) & 0xFF)}
}
