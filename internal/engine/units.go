package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Dim is the 7-entry SI base-dimension vector:
// mass, length, time, electric current, thermodynamic temperature,
// amount of substance, luminous intensity.
type Dim [7]int

var (
	DimOne         = Dim{}
	DimMass        = Dim{1, 0, 0, 0, 0, 0, 0}
	DimLength      = Dim{0, 1, 0, 0, 0, 0, 0}
	DimTime        = Dim{0, 0, 1, 0, 0, 0, 0}
	DimCurrent     = Dim{0, 0, 0, 1, 0, 0, 0}
	DimTemperature = Dim{0, 0, 0, 0, 1, 0, 0}
	DimAmount      = Dim{0, 0, 0, 0, 0, 1, 0}
	DimLuminous    = Dim{0, 0, 0, 0, 0, 0, 1}
)

func (d Dim) equal(o Dim) bool { return d == o }

func dimMul(a, b Dim, e int) Dim {
	var r Dim
	for i := range r {
		r[i] = a[i] + b[i]*e
	}
	return r
}

// NamedUnit defines a named (possibly prefixed) unit and its linear mapping to
// base coherent SI units: baseValue = slope*value + intercept.
type NamedUnit struct {
	Name      string
	Dim       Dim
	Slope     float64
	Intercept float64
}

var baseUnits = map[string]Dim{
	"kg": DimMass, "m": DimLength, "s": DimTime, "A": DimCurrent,
	"K": DimTemperature, "mol": DimAmount, "cd": DimLuminous,
	"1": DimOne,
}

// namedUnits are units defined without SI prefixes. Prefixed variants are
// generated automatically for every entry marked prefixable=true.
var namedUnits = []struct {
	name       string
	dim        Dim
	slope      float64
	intercept  float64
	prefixable bool
}{
	{"1", DimOne, 1, 0, false},
	{"kg", DimMass, 1, 0, false},
	{"g", DimMass, 1e-3, 0, true},
	{"m", DimLength, 1, 0, true},
	{"s", DimTime, 1, 0, true},
	{"min", DimTime, 60, 0, false},
	{"h", DimTime, 3600, 0, false},
	{"A", DimCurrent, 1, 0, true},
	{"K", DimTemperature, 1, 0, true},
	{"mol", DimAmount, 1, 0, true},
	{"cd", DimLuminous, 1, 0, true},
	{"rad", DimOne, 1, 0, false},
	{"deg", DimOne, math.Pi / 180, 0, false},
	{"Hz", dimMul(DimOne, DimTime, -1), 1, 0, true},
	{"N", Dim{1, 1, -2, 0, 0, 0, 0}, 1, 0, true},
	{"Pa", Dim{1, -1, -2, 0, 0, 0, 0}, 1, 0, true},
	{"bar", Dim{1, -1, -2, 0, 0, 0, 0}, 1e5, 0, false},
	{"J", Dim{1, 2, -2, 0, 0, 0, 0}, 1, 0, true},
	{"W", Dim{1, 2, -3, 0, 0, 0, 0}, 1, 0, true},
	{"V", Dim{1, 2, -3, -1, 0, 0, 0}, 1, 0, true},
	{"ohm", Dim{1, 2, -3, -2, 0, 0, 0}, 1, 0, true},
	{"C", Dim{0, 0, 1, 1, 0, 0, 0}, 1, 0, true},
	{"F", Dim{-1, -2, 4, 2, 0, 0, 0}, 1, 0, true},
	{"T", Dim{1, 0, -2, -1, 0, 0, 0}, 1, 0, true},
	{"L", dimMul(DimLength, DimLength, 3), 1e-3, 0, true},
	{"l", dimMul(DimLength, DimLength, 3), 1e-3, 0, false},
	{"degC", DimTemperature, 1, 273.15, false},
	{"degF", DimTemperature, 5.0 / 9.0, 459.67 * 5.0 / 9.0, false},
	{"percent", DimOne, 1e-2, 0, false},
	{"%", DimOne, 1e-2, 0, false},
	{"ppm", DimOne, 1e-6, 0, false},
	{"eV", Dim{1, 2, -2, 0, 0, 0, 0}, 1.602176634e-19, 0, true},
}

var siPrefixes = []struct {
	symbol string
	scale  float64
}{
	{"Q", 1e30}, {"R", 1e27}, {"Y", 1e24}, {"Z", 1e21},
	{"E", 1e18}, {"P", 1e15}, {"T", 1e12}, {"G", 1e9},
	{"M", 1e6}, {"k", 1e3}, {"h", 1e2}, {"da", 1e1},
	{"d", 1e-1}, {"c", 1e-2}, {"m", 1e-3}, {"u", 1e-6},
	{"µ", 1e-6}, {"n", 1e-9}, {"p", 1e-12}, {"f", 1e-15},
	{"a", 1e-18}, {"z", 1e-21}, {"y", 1e-24}, {"r", 1e-27},
	{"q", 1e-30},
}

// unitTable is built at init from namedUnits + prefixes.
var unitTable = map[string]NamedUnit{}

func init() {
	for _, u := range namedUnits {
		unitTable[u.name] = NamedUnit{Name: u.name, Dim: u.dim, Slope: u.slope, Intercept: u.intercept}
		if u.prefixable && u.name != "kg" {
			for _, p := range siPrefixes {
				nu := NamedUnit{Name: p.symbol + u.name, Dim: u.dim, Slope: p.scale * u.slope, Intercept: u.intercept}
				unitTable[nu.Name] = nu
			}
		}
	}
	// kilogram: prefixes attach to "g".
	unitTable["kg"] = NamedUnit{Name: "kg", Dim: DimMass, Slope: 1, Intercept: 0}
}

// Factor is one named unit raised to an integer exponent.
type Factor struct {
	Unit string
	Exp  int
}

// ParsedUnit is a product of named factors times a numeric scale.
type ParsedUnit struct {
	Text    string
	Factors []Factor
	Dim     Dim
}

// parseUnit accepts forms such as "kg", "m/s", "m.s^-2", "m/s^2",
// "N*m", "1/s", "1", "ug/mL". Exponents are signed integers; "." (or "*")
// joins factors and "/" starts a denominator factor.
func parseUnit(text string) (ParsedUnit, error) {
	t := strings.TrimSpace(text)
	pu := ParsedUnit{Text: t}
	if t == "" || t == "1" {
		return pu, nil
	}
	t = strings.ReplaceAll(t, "*", ".")
	// Normalize "a/b^2.c" style: split keeping operators.
	type token struct {
		f     string
		e     int
		denom bool
	}
	var toks []token
	flush := func(seg string, denom bool) error {
		if seg == "" {
			return nil
		}
		name, exp := seg, 1
		if ix := strings.IndexByte(seg, '^'); ix >= 0 {
			name = seg[:ix]
			n := 0
			if _, err := fmt.Sscanf(seg[ix+1:], "%d", &n); err != nil || n == 0 {
				return fmt.Errorf("bad exponent in unit %q", text)
			}
			exp = n
		}
		toks = append(toks, token{f: name, e: exp, denom: denom})
		return nil
	}
	denom := false
	cur := ""
	for i := 0; i < len(t); i++ {
		ch := t[i]
		switch ch {
		case '.':
			if err := flush(cur, denom); err != nil {
				return pu, err
			}
			cur = ""
		case '/':
			if err := flush(cur, denom); err != nil {
				return pu, err
			}
			cur = ""
			denom = true
		default:
			cur += string(ch)
		}
	}
	if err := flush(cur, denom); err != nil {
		return pu, err
	}
	for _, o := range toks {
		u, ok := unitTable[o.f]
		if !ok {
			return pu, fmt.Errorf("unknown unit %q (inside %q)", o.f, text)
		}
		e := o.e
		if o.denom {
			e = -e
		}
		pu.Factors = append(pu.Factors, Factor{Unit: o.f, Exp: e})
		for i := range pu.Dim {
			pu.Dim[i] += u.Dim[i] * e
		}
	}
	return pu, nil
}

// ConversionStep documents one elementary operation of a conversion path.
type ConversionStep struct {
	Op        string  `json:"op"`
	Unit      string  `json:"unit,omitempty"`
	Factor    float64 `json:"factor,omitempty"`
	Intercept float64 `json:"intercept,omitempty"`
	Exp       int     `json:"exp,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// ConversionPath converts a value expressed in `from` into coherent base units.
// For affine units (degC, degF) the path is value-specific; the returned
// slope/intercept satisfy base = slope*value + intercept.
type ConversionPath struct {
	From      string           `json:"from"`
	To        string           `json:"to"`
	Steps     []ConversionStep `json:"steps"`
	Slope     float64          `json:"slope"`
	Intercept float64          `json:"intercept"`
	Linear    bool             `json:"linear"`
	Dim       Dim              `json:"-"`
}

func dimName(d Dim) string {
	if d == DimOne {
		return "1"
	}
	names := []string{"kg", "m", "s", "A", "K", "mol", "cd"}
	var ss []string
	for i, n := range names {
		if d[i] != 0 {
			if d[i] == 1 {
				ss = append(ss, n)
			} else {
				ss = append(ss, fmt.Sprintf("%s^%d", n, d[i]))
			}
		}
	}
	return strings.Join(ss, ".")
}

// conversionPath builds a fully documented path from any parseable unit to
// coherent SI base units. Compound units may only use linear (zero-intercept)
// factors; affine factors are restricted to stand-alone temperature units.
func conversionPath(text string) (ConversionPath, error) {
	pu, err := parseUnit(text)
	if err != nil {
		return ConversionPath{}, err
	}
	cp := ConversionPath{From: text, To: dimName(pu.Dim), Slope: 1, Linear: true, Dim: pu.Dim}
	if len(pu.Factors) == 0 {
		return cp, nil
	}
	for _, f := range pu.Factors {
		u := unitTable[f.Unit]
		switch {
		case len(pu.Factors) == 1 && f.Exp == 1:
			if u.Intercept != 0 {
				cp.Linear = false
			}
			cp.Steps = append(cp.Steps,
				ConversionStep{Op: "named_unit", Unit: u.Name, Factor: u.Slope, Intercept: u.Intercept,
					Note: fmt.Sprintf("base = %g*%s + %g", u.Slope, u.Name, u.Intercept)})
		default:
			if u.Intercept != 0 {
				return cp, fmt.Errorf("affine unit %q cannot appear inside a compound unit %q", u.Name, text)
			}
			note := fmt.Sprintf("factor %s", u.Name)
			if f.Exp != 1 {
				note = fmt.Sprintf("factor %s^%d", u.Name, f.Exp)
			}
			cp.Steps = append(cp.Steps, ConversionStep{Op: "named_unit_power", Unit: u.Name, Exp: f.Exp, Factor: math.Pow(u.Slope, float64(f.Exp)), Note: note})
		}
	}
	// aggregate slope/intercept
	mul, add := 1.0, 0.0
	for _, f := range pu.Factors {
		u := unitTable[f.Unit]
		if len(pu.Factors) == 1 && f.Exp == 1 {
			add = u.Intercept
			mul *= u.Slope
			continue
		}
		mul *= math.Pow(u.Slope, float64(f.Exp))
	}
	cp.Slope, cp.Intercept = mul, add
	return cp, nil
}

// applyPath converts x to base units.
func (cp ConversionPath) apply(x float64) float64 { return cp.Slope*x + cp.Intercept }

// sameDim reports whether two parsed units share the same dimension vector.
func sameDim(a, b string) (Dim, bool, error) {
	pa, err := parseUnit(a)
	if err != nil {
		return Dim{}, false, err
	}
	pb, err := parseUnit(b)
	if err != nil {
		return Dim{}, false, err
	}
	return pa.Dim, pa.Dim == pb.Dim, nil
}

func sortedUnitNames() []string {
	seen := map[string]bool{}
	var out []string
	for n := range unitTable {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
