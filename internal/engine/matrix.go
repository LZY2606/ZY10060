package engine

import (
	"math"
	"sort"
)

// sym stores a symmetric n*n matrix row-major.
type sym struct {
	n int
	a []float64
}

func newSym(n int) sym { return sym{n: n, a: make([]float64, n*n)} }
func (m sym) at(i, j int) float64 {
	if i > j {
		i, j = j, i
	}
	return m.a[i*m.n+j]
}
func (m sym) set(i, j int, v float64) {
	m.a[i*m.n+j] = v
	if i != j {
		m.a[j*m.n+i] = v
	}
}

// psdFailure describes a positive-definiteness violation.
type psdFailure struct {
	index   int
	value   float64 // smallest eigenvalue
	members []int   // dominant eigenvector components
}

// checkPSD performs a cyclic Jacobi eigenvalue decomposition of the real
// symmetric matrix and returns the most negative eigenvalue (if any).
func checkPSD(m sym, tol float64) *psdFailure {
	n := m.n
	a := make([]float64, len(m.a))
	copy(a, m.a)
	v := make([]float64, n*n)
	for i := 0; i < n; i++ {
		v[i*n+i] = 1
	}
	for sweep := 0; sweep < 200; sweep++ {
		maxOff := 0.0
		for pp := 0; pp < n; pp++ {
			for qq := pp + 1; qq < n; qq++ {
				if o := math.Abs(a[pp*n+qq]); o > maxOff {
					maxOff = o
				}
			}
		}
		if maxOff < 1e-14 {
			break
		}
		for pp := 0; pp < n; pp++ {
			for qq := pp + 1; qq < n; qq++ {
				apq := a[pp*n+qq]
				if math.Abs(apq) < 1e-300 {
					continue
				}
				app, aqq := a[pp*n+pp], a[qq*n+qq]
				phi := 0.5 * math.Atan2(2*apq, aqq-app)
				co, si := math.Cos(phi), math.Sin(phi)
				for k := 0; k < n; k++ {
					if k == pp || k == qq {
						continue
					}
					akp, akq := a[k*n+pp], a[k*n+qq]
					nkp := co*akp - si*akq
					nkq := si*akp + co*akq
					a[k*n+pp], a[pp*n+k] = nkp, nkp
					a[k*n+qq], a[qq*n+k] = nkq, nkq
				}
				npp := co*co*app - 2*si*co*apq + si*si*aqq
				nqq := si*si*app + 2*si*co*apq + co*co*aqq
				a[pp*n+pp], a[qq*n+qq] = npp, nqq
				a[pp*n+qq], a[qq*n+pp] = 0, 0
				for k := 0; k < n; k++ {
					vkp, vkq := v[k*n+pp], v[k*n+qq]
					v[k*n+pp] = co*vkp - si*vkq
					v[k*n+qq] = si*vkp + co*vkq
				}
			}
		}
	}
	minEig := math.Inf(1)
	minIdx := 0
	for i := 0; i < n; i++ {
		if a[i*n+i] < minEig {
			minEig = a[i*n+i]
			minIdx = i
		}
	}
	if minEig >= -tol {
		return nil
	}
	type mi struct {
		idx int
		abs float64
	}
	var ms []mi
	for r := 0; r < n; r++ {
		ms = append(ms, mi{r, math.Abs(v[r*n+minIdx])})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].abs > ms[j].abs })
	var members []int
	for k := 0; k < len(ms) && k < 4; k++ {
		if ms[k].abs > 0.1 || k < 2 {
			members = append(members, ms[k].idx)
		}
	}
	return &psdFailure{index: minIdx, value: minEig, members: members}
}

// matVecSym computes y = A*x for symmetric A.
func matVecSym(m sym, x []float64) []float64 {
	y := make([]float64, m.n)
	for i := 0; i < m.n; i++ {
		s := 0.0
		for j := 0; j < m.n; j++ {
			s += m.at(i, j) * x[j]
		}
		y[i] = s
	}
	return y
}
