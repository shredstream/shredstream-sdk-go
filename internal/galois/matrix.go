package galois

import "errors"

var ErrSingular = errors.New("galois: singular matrix")

type Matrix [][]byte

func NewMatrix(rows, cols int) Matrix {
	m := make(Matrix, rows)
	buf := make([]byte, rows*cols)
	for i := 0; i < rows; i++ {
		m[i] = buf[i*cols : (i+1)*cols : (i+1)*cols]
	}
	return m
}

func Identity(size int) Matrix {
	m := NewMatrix(size, size)
	for i := 0; i < size; i++ {
		m[i][i] = 1
	}
	return m
}

func NewVandermonde(rows, cols int) Matrix {
	m := NewMatrix(rows, cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			m[r][c] = Exp(byte(r), c)
		}
	}
	return m
}

func (m Matrix) Rows() int { return len(m) }
func (m Matrix) Cols() int {
	if len(m) == 0 {
		return 0
	}
	return len(m[0])
}

func (m Matrix) Clone() Matrix {
	rows, cols := m.Rows(), m.Cols()
	out := NewMatrix(rows, cols)
	for r := 0; r < rows; r++ {
		copy(out[r], m[r])
	}
	return out
}

func (m Matrix) Multiply(rhs Matrix) Matrix {
	if m.Cols() != rhs.Rows() {
		panic("galois: matrix dim mismatch")
	}
	rows, cols, inner := m.Rows(), rhs.Cols(), m.Cols()
	out := NewMatrix(rows, cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			var v byte
			for i := 0; i < inner; i++ {
				v ^= Mul(m[r][i], rhs[i][c])
			}
			out[r][c] = v
		}
	}
	return out
}

func (m Matrix) Augment(rhs Matrix) Matrix {
	if m.Rows() != rhs.Rows() {
		panic("galois: row mismatch in augment")
	}
	rows := m.Rows()
	out := NewMatrix(rows, m.Cols()+rhs.Cols())
	for r := 0; r < rows; r++ {
		copy(out[r][:m.Cols()], m[r])
		copy(out[r][m.Cols():], rhs[r])
	}
	return out
}

func (m Matrix) SubMatrix(r0, c0, r1, c1 int) Matrix {
	out := NewMatrix(r1-r0, c1-c0)
	for r := r0; r < r1; r++ {
		copy(out[r-r0], m[r][c0:c1])
	}
	return out
}

func (m Matrix) SwapRows(r1, r2 int) {
	if r1 == r2 {
		return
	}
	m[r1], m[r2] = m[r2], m[r1]
}

func (m Matrix) GaussianElim() error {
	rows, cols := m.Rows(), m.Cols()
	for r := 0; r < rows; r++ {
		if m[r][r] == 0 {
			for rb := r + 1; rb < rows; rb++ {
				if m[rb][r] != 0 {
					m.SwapRows(r, rb)
					break
				}
			}
		}
		if m[r][r] == 0 {
			return ErrSingular
		}
		if m[r][r] != 1 {
			scale := Div(1, m[r][r])
			for c := 0; c < cols; c++ {
				m[r][c] = Mul(scale, m[r][c])
			}
		}
		for rb := r + 1; rb < rows; rb++ {
			if m[rb][r] != 0 {
				scale := m[rb][r]
				for c := 0; c < cols; c++ {
					m[rb][c] ^= Mul(scale, m[r][c])
				}
			}
		}
	}
	for d := 0; d < rows; d++ {
		for ra := 0; ra < d; ra++ {
			if m[ra][d] != 0 {
				scale := m[ra][d]
				for c := 0; c < cols; c++ {
					m[ra][c] ^= Mul(scale, m[d][c])
				}
			}
		}
	}
	return nil
}

func (m Matrix) Inverse() (Matrix, error) {
	if m.Rows() != m.Cols() {
		panic("galois: cannot invert non-square matrix")
	}
	n := m.Rows()
	work := m.Augment(Identity(n))
	if err := work.GaussianElim(); err != nil {
		return nil, err
	}
	return work.SubMatrix(0, n, n, 2*n), nil
}
