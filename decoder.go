package shredstream

import (
	"encoding/binary"
	"fmt"
	"math/big"
)

const (
	maxTxPerEntry      = 10_000
	maxSignaturesPerTx = 64
)

type Transaction struct {
	Signatures [][]byte
	Raw        []byte
}

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func encodeBase58(b []byte) string {
	x := new(big.Int).SetBytes(b)
	mod := new(big.Int)
	zero := big.NewInt(0)
	base := big.NewInt(58)

	var result []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}

	for _, v := range b {
		if v != 0 {
			break
		}
		result = append(result, '1')
	}

	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

func (tx *Transaction) Signature() string {
	if len(tx.Signatures) == 0 {
		return ""
	}
	return encodeBase58(tx.Signatures[0])
}

type BatchDecoder struct {
	buffer         []byte
	expectedCount  *uint64
	entriesYielded uint64
	cursor         int
}

func NewBatchDecoder() *BatchDecoder {
	return &BatchDecoder{}
}

func (d *BatchDecoder) Reset() {
	d.buffer = d.buffer[:0]
	d.expectedCount = nil
	d.entriesYielded = 0
	d.cursor = 0
}

func (d *BatchDecoder) Push(payload []byte) (txs []Transaction, err error) {
	defer func() {
		if r := recover(); r != nil {
			txs = nil
			err = fmt.Errorf("decoder panic: %v", r)
		}
	}()

	d.buffer = append(d.buffer, payload...)

	if d.expectedCount == nil {
		if len(d.buffer) < 8 {
			return nil, nil
		}
		count := binary.LittleEndian.Uint64(d.buffer[0:8])
		if count > 100_000 {
			return nil, fmt.Errorf("corrupt entry_count: %d exceeds limit", count)
		}
		d.expectedCount = &count
		d.cursor = 8
	}

	for d.entriesYielded < *d.expectedCount {
		entryTxs, err := d.tryDecodeEntry()
		if err != nil {
			return txs, err
		}
		if entryTxs == nil {
			break
		}
		txs = append(txs, entryTxs...)
		d.entriesYielded++
	}

	return txs, nil
}

func (d *BatchDecoder) tryDecodeEntry() ([]Transaction, error) {
	pos := d.cursor
	buf := d.buffer

	if pos+48 > len(buf) {
		return nil, nil
	}

	pos += 8 + 32
	txCount := binary.LittleEndian.Uint64(buf[pos:])
	if txCount > maxTxPerEntry {
		return nil, fmt.Errorf("corrupt tx_count: %d exceeds limit", txCount)
	}
	pos += 8

	txs := make([]Transaction, 0, txCount)

	for i := uint64(0); i < txCount; i++ {
		txStart := pos

		txLen, sigs, err := parseTransaction(buf, pos)
		if err != nil {
			return nil, fmt.Errorf("parsing tx %d: %w", i, err)
		}
		if txLen < 0 {
			return nil, nil
		}

		txEnd := pos + txLen
		txRaw := make([]byte, txLen)
		copy(txRaw, buf[txStart:txEnd])

		txs = append(txs, Transaction{
			Signatures: sigs,
			Raw:        txRaw,
		})

		pos = txEnd
	}

	d.cursor = pos
	return txs, nil
}

func parseTransaction(buf []byte, pos int) (int, [][]byte, error) {
	start := pos

	if pos >= len(buf) {
		return -1, nil, nil
	}
	sigCount, n := decodeCompactU16(buf, pos)
	if n < 0 {
		return -1, nil, nil
	}
	if sigCount > maxSignaturesPerTx {
		return -1, nil, fmt.Errorf("corrupt sig_count: %d exceeds limit", sigCount)
	}
	pos += n

	sigsEnd := pos + sigCount*64
	if sigsEnd > len(buf) {
		return -1, nil, nil
	}

	sigs := make([][]byte, sigCount)
	for i := 0; i < sigCount; i++ {
		sig := make([]byte, 64)
		copy(sig, buf[pos:pos+64])
		sigs[i] = sig
		pos += 64
	}

	if pos >= len(buf) {
		return -1, nil, nil
	}
	msgFirst := buf[pos]
	isV0 := msgFirst >= 0x80

	if isV0 {
		pos++
	}

	pos += 3
	if pos > len(buf) {
		return -1, nil, nil
	}

	if pos >= len(buf) {
		return -1, nil, nil
	}
	acctCount, n := decodeCompactU16(buf, pos)
	if n < 0 {
		return -1, nil, nil
	}
	pos += n
	pos += acctCount * 32
	if pos > len(buf) {
		return -1, nil, nil
	}

	pos += 32
	if pos > len(buf) {
		return -1, nil, nil
	}

	if pos >= len(buf) {
		return -1, nil, nil
	}
	ixCount, n := decodeCompactU16(buf, pos)
	if n < 0 {
		return -1, nil, nil
	}
	pos += n

	for ix := 0; ix < ixCount; ix++ {
		pos++
		if pos > len(buf) {
			return -1, nil, nil
		}

		if pos >= len(buf) {
			return -1, nil, nil
		}
		acctLen, n := decodeCompactU16(buf, pos)
		if n < 0 {
			return -1, nil, nil
		}
		pos += n
		pos += acctLen
		if pos > len(buf) {
			return -1, nil, nil
		}

		if pos >= len(buf) {
			return -1, nil, nil
		}
		dataLen, n := decodeCompactU16(buf, pos)
		if n < 0 {
			return -1, nil, nil
		}
		pos += n
		pos += dataLen
		if pos > len(buf) {
			return -1, nil, nil
		}
	}

	if isV0 {
		if pos >= len(buf) {
			return -1, nil, nil
		}
		atlCount, n := decodeCompactU16(buf, pos)
		if n < 0 {
			return -1, nil, nil
		}
		pos += n

		for atl := 0; atl < atlCount; atl++ {
			pos += 32
			if pos > len(buf) {
				return -1, nil, nil
			}

			if pos >= len(buf) {
				return -1, nil, nil
			}
			wLen, n := decodeCompactU16(buf, pos)
			if n < 0 {
				return -1, nil, nil
			}
			pos += n
			pos += wLen
			if pos > len(buf) {
				return -1, nil, nil
			}

			if pos >= len(buf) {
				return -1, nil, nil
			}
			rLen, n := decodeCompactU16(buf, pos)
			if n < 0 {
				return -1, nil, nil
			}
			pos += n
			pos += rLen
			if pos > len(buf) {
				return -1, nil, nil
			}
		}
	}

	return pos - start, sigs, nil
}

func decodeCompactU16(buf []byte, pos int) (int, int) {
	if pos >= len(buf) {
		return 0, -1
	}

	b0 := buf[pos]
	if b0 < 0x80 {
		return int(b0), 1
	}

	if pos+1 >= len(buf) {
		return 0, -1
	}
	b1 := buf[pos+1]
	if b1 < 0x80 {
		return int(b0&0x7F) | (int(b1) << 7), 2
	}

	if pos+2 >= len(buf) {
		return 0, -1
	}
	b2 := buf[pos+2]
	return int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2) << 14), 3
}
