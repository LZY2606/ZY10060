package engine

import (
	"math"
)

func tQuantile975(nu float64) float64 {
	if nu <= 0 || math.IsInf(nu, 1) {
		return 1.959963984540054
	}
	if nu < 1 {
		nu = 1
	}
	lo, hi := 0.01, 100.0
	for i := 0; i < 80; i++ {
		mid := (lo + hi) / 2
		if tCDF(mid, nu) < 0.975 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func tCDF(t, nu float64) float64 {
	x := nu / (nu + t*t)
	ib := incompleteBeta(x, nu/2, 0.5)
	if t >= 0 {
		return 1 - 0.5*ib
	}
	return 0.5 * ib
}

func incompleteBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	bt := math.Exp(lgamma(a+b) - lgamma(a) - lgamma(b) + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return bt * betaCF(x, a, b) / a
	}
	return 1 - bt*betaCF(1-x, b, a)/b
}

func betaCF(x, a, b float64) float64 {
	eps := 1e-14
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < 1e-30 {
		d = 1e-30
	}
	d = 1 / d
	h := d
	for m := 1; m <= 300; m++ {
		m2 := 2 * m
		aa := float64(m) * ((b - float64(m)) * x) / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		h *= d * c
		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + float64(m2)) * (qap + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < 1e-30 {
			d = 1e-30
		}
		c = 1 + aa/c
		if math.Abs(c) < 1e-30 {
			c = 1e-30
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

func lgamma(x float64) float64 { v, _ := math.Lgamma(x); return v }
