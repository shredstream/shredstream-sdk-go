package shredstream

import "math"

func saturatingAddU64(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

func saturatingSubU64(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

func saturatingAddU32(a, b uint32) uint32 {
	if a > math.MaxUint32-b {
		return math.MaxUint32
	}
	return a + b
}

func saturatingSubU32(a, b uint32) uint32 {
	if a < b {
		return 0
	}
	return a - b
}
