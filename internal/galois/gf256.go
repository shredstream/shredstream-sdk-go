package galois

const PrimitivePoly = 0x11D

const primitivePolyReduction = byte(PrimitivePoly & 0xFF)

var (
	expTable [510]byte
	logTable [256]byte
	mulTable [256][256]byte
)

func init() {
	b := 1
	for log := 0; log < 255; log++ {
		logTable[b] = byte(log)
		expTable[log] = byte(b)
		expTable[log+255] = byte(b)
		b <<= 1
		if b >= 256 {
			b ^= int(PrimitivePoly)
			b &= 0xFF
		}
	}
	for a := 0; a < 256; a++ {
		for c := 0; c < 256; c++ {
			if a == 0 || c == 0 {
				mulTable[a][c] = 0
			} else {
				mulTable[a][c] = expTable[int(logTable[a])+int(logTable[c])]
			}
		}
	}
}

func MulTable(a byte) *[256]byte { return &mulTable[a] }

func Add(a, b byte) byte { return a ^ b }

func Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return expTable[int(logTable[a])+int(logTable[b])]
}

func Div(a, b byte) byte {
	if a == 0 {
		return 0
	}
	if b == 0 {
		panic("galois: division by zero")
	}
	r := int(logTable[a]) - int(logTable[b])
	if r < 0 {
		r += 255
	}
	return expTable[r]
}

func Inv(a byte) byte {
	if a == 0 {
		panic("galois: inverse of zero")
	}
	return expTable[255-int(logTable[a])]
}

func Exp(a byte, n int) byte {
	if n == 0 {
		return 1
	}
	if a == 0 {
		return 0
	}
	r := int(logTable[a]) * n
	r %= 255
	return expTable[r]
}

func ExpTable() *[510]byte { return &expTable }
func LogTable() *[256]byte { return &logTable }
