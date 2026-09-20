package engine

import (
	"math"
)

// StudentsTQuantile returns the two-sided t quantile for probability p
// (e.g. p=0.95 gives the 95% coverage factor) with nu degrees of freedom.
// Bisection on the regularized incomplete-beta CDF; nu may be +Inf, in which
// case the standard normal quantile is returned.
func StudentsTQuantile(p float64, nu float64) float64 {
	if math.IsInf(nu, 1) || nu > 1e6 {
		return normQuantile(0.5 + p/2)
	}
	if nu <= 0 {
		return math.NaN()
	}
	target := 0.5 + p/2
	f := func(t float64) float64 {
		x := nu / (nu + t*t)
		ib := regIncBeta(nu/2, 0.5, x)
		if t >= 0 {
			return 1 - ib/2
		}
		return ib / 2
	}
	lo, hi := -50.0, 50.0
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if f(mid) < target {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-12 {
			break
		}
	}
	return (lo + hi) / 2
}

// normQuantile is the inverse standard-normal CDF (Acklam's algorithm).
func normQuantile(p float64) float64 {
	const (
		a1 = -3.969683028665376e+01
		a2 = 2.209460984245205e+02
		a3 = -2.759285104469687e+02
		a4 = 1.383577518672690e+02
		a5 = -3.066479806614716e+01
		a6 = 2.506628277459239e+00
		b1 = -5.447609879822406e+01
		b2 = 1.615858368580409e+02
		b3 = -1.556989798598866e+02
		b4 = 6.680131188771972e+01
		b5 = -1.328068155288572e+01
		c1 = -7.784894002430293e-03
		c2 = -3.223964580411365e-01
		c3 = -2.400758277161838e+00
		c4 = -2.549732539343734e+00
		c5 = 4.374664141464968e+00
		c6 = 2.938163982698783e+00
		d1 = 7.784695709041462e-03
		d2 = 3.224671290700398e-01
		d3 = 2.445134137142996e+00
		d4 = 3.754408661907416e+00
	)
	plow, phigh := 0.02425, 1-0.02425
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	case p <= phigh:
		q := p - 0.5
		r := q * q
		return (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q /
			(((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
}

// regIncBeta computes I_x(a,b), the regularized incomplete beta function,
// via Lentz's continued fraction with the symmetry reflection.
func regIncBeta(a, b, x float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	bt := math.Exp(lgamma(a+b) - lgamma(a) - lgamma(b) + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return bt * betacf(a, b, x) / a
	}
	return 1 - bt*betacf(b, a, 1-x)/b
}

func betacf(a, b, x float64) float64 {
	const maxit = 300
	const eps = 3e-14
	const fpmin = 1e-300
	qab, qap, qam := a+b, a+1.0, a-1.0
	c := 1.0
	d := 1.0 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1.0 / d
	h := d
	for m := 1; m <= maxit; m++ {
		m2 := 2 * m
		aa := float64(m) * (b - float64(m)) * x / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1.0 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1.0 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1.0 / d
		h *= d * c
		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + float64(m2)) * (qap + float64(m2)))
		d = 1.0 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1.0 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1.0 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// lgamma is math.Lgamma returning only the value.
func lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}
