package shredstream

import (
	"errors"
	"fmt"

	"github.com/shredstream/shredstream-sdk-go/v2/internal/galois"
)

const (
	SizeOfSignature          = 64
	SizeOfCodingShredHeaders = 89
	SizeOfMerkleRoot         = 32
	SizeOfMerkleProofEntry   = 20
	DataShredPayloadLen      = 1203
	CodeShredPayloadLen      = 1228

	cacheCapacity = 32
)

func ShardSize(proofSize uint8, resigned bool) int {
	n := CodeShredPayloadLen - SizeOfCodingShredHeaders - SizeOfMerkleRoot - int(proofSize)*SizeOfMerkleProofEntry
	if resigned {
		n -= SizeOfSignature
	}
	return n
}

var (
	ErrTooFewShards     = errors.New("fec: too few shards present")
	ErrShardSizeMismatch = errors.New("fec: shard size mismatch")
	ErrShardCount       = errors.New("fec: wrong number of shards")
)

type reedSolomonCache struct {
	keys    [][2]uint16
	values  []*reedSolomon
	capacity int
}

func newReedSolomonCache() *reedSolomonCache {
	return &reedSolomonCache{capacity: cacheCapacity}
}

func (c *reedSolomonCache) GetOrBuild(numData, numCoding uint16) (*reedSolomon, error) {
	key := [2]uint16{numData, numCoding}
	for i, k := range c.keys {
		if k == key {
			rs := c.values[i]
			c.keys = append(append(c.keys[:i], c.keys[i+1:]...), key)
			c.values = append(append(c.values[:i], c.values[i+1:]...), rs)
			return rs, nil
		}
	}
	rs, err := newReedSolomon(int(numData), int(numCoding))
	if err != nil {
		return nil, err
	}
	if len(c.keys) >= c.capacity {
		c.keys = c.keys[1:]
		c.values = c.values[1:]
	}
	c.keys = append(c.keys, key)
	c.values = append(c.values, rs)
	return rs, nil
}

type reedSolomon struct {
	dataShards  int
	totalShards int
	matrix      galois.Matrix
	parityRows  [][]byte
}

func newReedSolomon(data, coding int) (*reedSolomon, error) {
	if data <= 0 || coding < 0 {
		return nil, fmt.Errorf("fec: invalid shard counts")
	}
	total := data + coding
	if total > 256 {
		return nil, fmt.Errorf("fec: too many shards")
	}
	vand := galois.NewVandermonde(total, data)
	top := vand.SubMatrix(0, 0, data, data)
	topInv, err := top.Inverse()
	if err != nil {
		return nil, err
	}
	m := vand.Multiply(topInv)
	parity := make([][]byte, coding)
	for i := 0; i < coding; i++ {
		parity[i] = m[data+i]
	}
	return &reedSolomon{
		dataShards:  data,
		totalShards: total,
		matrix:      m,
		parityRows:  parity,
	}, nil
}

func (rs *reedSolomon) DataShards() int  { return rs.dataShards }
func (rs *reedSolomon) TotalShards() int { return rs.totalShards }

func (rs *reedSolomon) Encode(shards [][]byte) error {
	if len(shards) != rs.totalShards {
		return ErrShardCount
	}
	shardLen := len(shards[0])
	for i, s := range shards {
		if len(s) != shardLen {
			return ErrShardSizeMismatch
		}
		if i >= rs.dataShards && len(s) == 0 {
			shards[i] = make([]byte, shardLen)
		}
	}
	codeSomeSlices(rs.parityRows, shards[:rs.dataShards], shards[rs.dataShards:])
	return nil
}

func (rs *reedSolomon) Reconstruct(shards [][]byte, present []bool) error {
	return rs.reconstructInternal(shards, present, false)
}

func (rs *reedSolomon) ReconstructData(shards [][]byte, present []bool) error {
	return rs.reconstructInternal(shards, present, true)
}

func (rs *reedSolomon) reconstructInternal(shards [][]byte, present []bool, dataOnly bool) error {
	if len(shards) != rs.totalShards || len(present) != rs.totalShards {
		return ErrShardCount
	}
	shardLen := 0
	numPresent := 0
	for i, s := range shards {
		if present[i] {
			if len(s) == 0 {
				return errors.New("fec: empty shard marked present")
			}
			if shardLen == 0 {
				shardLen = len(s)
			} else if len(s) != shardLen {
				return ErrShardSizeMismatch
			}
			numPresent++
		}
	}
	if numPresent == rs.totalShards {
		return nil
	}
	if numPresent < rs.dataShards {
		return ErrTooFewShards
	}

	validIdx := make([]int, 0, rs.dataShards)
	invalidIdx := make([]int, 0)
	for i := 0; i < rs.totalShards; i++ {
		if present[i] && len(validIdx) < rs.dataShards {
			validIdx = append(validIdx, i)
		} else if !present[i] {
			invalidIdx = append(invalidIdx, i)
		}
	}

	subMatrix := galois.NewMatrix(rs.dataShards, rs.dataShards)
	for r, vi := range validIdx {
		for c := 0; c < rs.dataShards; c++ {
			subMatrix[r][c] = rs.matrix[vi][c]
		}
	}
	decodeMatrix, err := subMatrix.Inverse()
	if err != nil {
		return err
	}

	subShards := make([][]byte, rs.dataShards)
	for r, vi := range validIdx {
		subShards[r] = shards[vi]
	}

	missingDataRows := make([][]byte, 0)
	missingDataSlices := make([][]byte, 0)
	for _, mi := range invalidIdx {
		if mi >= rs.dataShards {
			break
		}
		if shards[mi] == nil || len(shards[mi]) != shardLen {
			shards[mi] = make([]byte, shardLen)
		} else {
			for j := range shards[mi] {
				shards[mi][j] = 0
			}
		}
		missingDataRows = append(missingDataRows, decodeMatrix[mi])
		missingDataSlices = append(missingDataSlices, shards[mi])
	}
	if len(missingDataSlices) > 0 {
		codeSomeSlices(missingDataRows, subShards, missingDataSlices)
	}

	if dataOnly {
		for i := 0; i < rs.dataShards; i++ {
			present[i] = true
		}
		return nil
	}

	missingParityRows := make([][]byte, 0)
	missingParitySlices := make([][]byte, 0)
	for _, mi := range invalidIdx {
		if mi < rs.dataShards {
			continue
		}
		if shards[mi] == nil || len(shards[mi]) != shardLen {
			shards[mi] = make([]byte, shardLen)
		} else {
			for j := range shards[mi] {
				shards[mi][j] = 0
			}
		}
		missingParityRows = append(missingParityRows, rs.parityRows[mi-rs.dataShards])
		missingParitySlices = append(missingParitySlices, shards[mi])
	}
	if len(missingParitySlices) > 0 {
		codeSomeSlices(missingParityRows, shards[:rs.dataShards], missingParitySlices)
	}

	for i := 0; i < rs.totalShards; i++ {
		present[i] = true
	}
	return nil
}

func codeSomeSlices(matrixRows [][]byte, inputs, outputs [][]byte) {
	for iRow := 0; iRow < len(outputs); iRow++ {
		out := outputs[iRow]
		row := matrixRows[iRow]
		for iIn := 0; iIn < len(inputs); iIn++ {
			c := row[iIn]
			in := inputs[iIn]
			if iIn == 0 {
				mulSlice(c, in, out)
			} else {
				mulSliceXor(c, in, out)
			}
		}
	}
}

func mulSlice(c byte, in, out []byte) {
	if c == 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}
	if c == 1 {
		copy(out, in)
		return
	}
	t := galois.MulTable(c)
	for i, b := range in {
		out[i] = t[b]
	}
}

func mulSliceXor(c byte, in, out []byte) {
	if c == 0 {
		return
	}
	if c == 1 {
		for i, b := range in {
			out[i] ^= b
		}
		return
	}
	t := galois.MulTable(c)
	for i, b := range in {
		out[i] ^= t[b]
	}
}
