package units

import (
	"fmt"
	"strings"
)

type Dimension struct {
	Kilogram int
	Meter    int
	Second   int
	Ampere   int
	Kelvin   int
	Mole     int
	Candela  int
	Amount   int
}

type Definition struct {
	Symbol    string
	Name      string
	Dimension Dimension
	Scale     float64
	Offset    float64
}

var definitions = map[string]Definition{}

func init() {
	add(Definition{Name: "kilogram", Symbol: "kg", Dimension: Dimension{Kilogram: 1}, Scale: 1})
	add(Definition{Name: "gram", Symbol: "g", Scale: 0.001, Dimension: Dimension{Kilogram: 1}})
	add(Definition{Name: "milligram", Symbol: "mg", Scale: 0.000001, Dimension: Dimension{Kilogram: 1}})
	add(Definition{Name: "tonne", Symbol: "t", Scale: 1000, Dimension: Dimension{Kilogram: 1}})
	add(Definition{Name: "pound", Symbol: "lb", Scale: 0.45359237, Dimension: Dimension{Kilogram: 1}})
	add(Definition{Name: "metre", Symbol: "m", Dimension: Dimension{Meter: 1}, Scale: 1})
	add(Definition{Name: "millimetre", Symbol: "mm", Scale: 0.001, Dimension: Dimension{Meter: 1}})
	add(Definition{Name: "centimetre", Symbol: "cm", Scale: 0.01, Dimension: Dimension{Meter: 1}})
	add(Definition{Name: "kilometre", Symbol: "km", Scale: 1000, Dimension: Dimension{Meter: 1}})
	add(Definition{Name: "inch", Symbol: "in", Scale: 0.0254, Dimension: Dimension{Meter: 1}})
	add(Definition{Name: "foot", Symbol: "ft", Scale: 0.3048, Dimension: Dimension{Meter: 1}})
	add(Definition{Name: "second", Symbol: "s", Dimension: Dimension{Second: 1}, Scale: 1})
	add(Definition{Name: "minute", Symbol: "min", Scale: 60, Dimension: Dimension{Second: 1}})
	add(Definition{Name: "hour", Symbol: "h", Scale: 3600, Dimension: Dimension{Second: 1}})
	add(Definition{Name: "kelvin", Symbol: "K", Dimension: Dimension{Kelvin: 1}, Scale: 1})
	add(Definition{Name: "degree Celsius", Symbol: "°C", Scale: 1, Offset: 273.15, Dimension: Dimension{Kelvin: 1}})
	add(Definition{Name: "degree Fahrenheit", Symbol: "°F", Scale: 5.0 / 9.0, Offset: 459.67 * 5.0 / 9.0, Dimension: Dimension{Kelvin: 1}})
	add(Definition{Name: "ampere", Symbol: "A", Dimension: Dimension{Ampere: 1}, Scale: 1})
	add(Definition{Name: "mole", Symbol: "mol", Dimension: Dimension{Mole: 1}, Scale: 1})
	add(Definition{Name: "candela", Symbol: "cd", Dimension: Dimension{Candela: 1}, Scale: 1})
	add(Definition{Name: "pascal", Symbol: "Pa", Dimension: Dimension{Kilogram: 1, Meter: -1, Second: -2}, Scale: 1})
	add(Definition{Name: "hectopascal", Symbol: "hPa", Scale: 100, Dimension: Dimension{Kilogram: 1, Meter: -1, Second: -2}})
	add(Definition{Name: "litre", Symbol: "L", Scale: 0.001, Dimension: Dimension{Meter: 3}})
	add(Definition{Name: "millilitre", Symbol: "mL", Scale: 0.000001, Dimension: Dimension{Meter: 3}})
	add(Definition{Name: "cubic metre", Symbol: "m3", Scale: 1, Dimension: Dimension{Meter: 3}})
	add(Definition{Name: "one", Symbol: "", Dimension: Dimension{}, Scale: 1})
	add(Definition{Name: "one", Symbol: "1", Dimension: Dimension{}, Scale: 1})
	add(Definition{Name: "percent", Symbol: "%", Scale: 0.01, Dimension: Dimension{}})
	add(Definition{Name: "parts per million", Symbol: "ppm", Scale: 0.000001, Dimension: Dimension{}})
	add(Definition{Name: "radian", Symbol: "rad", Dimension: Dimension{}, Scale: 1})
	add(Definition{Name: "degree", Symbol: "deg", Scale: 3.141592653589793 / 180, Dimension: Dimension{}})
}

func add(d Definition) { definitions[d.Symbol] = d }

func Canonical(symbol string) (Definition, bool) {
	d, ok := definitions[strings.TrimSpace(symbol)]
	return d, ok
}

func Compatible(a, b string) bool {
	da, oa := Canonical(a)
	db, ob := Canonical(b)
	return oa && ob && da.Dimension == db.Dimension
}

func Convert(value float64, from, to string) (float64, []string, error) {
	df, ok := Canonical(from)
	if !ok {
		return 0, nil, fmt.Errorf("unknown unit %q", from)
	}
	dt, ok := Canonical(to)
	if !ok {
		return 0, nil, fmt.Errorf("unknown unit %q", to)
	}
	if df.Dimension != dt.Dimension {
		return 0, nil, fmt.Errorf("dimension %s cannot convert to %s", DimensionName(df.Dimension), DimensionName(dt.Dimension))
	}
	canonical := value*df.Scale + df.Offset
	out := (canonical - dt.Offset) / dt.Scale
	path := []string{displaySymbol(from), "canonical:" + DimensionName(df.Dimension), displaySymbol(to)}
	return out, path, nil
}

func displaySymbol(s string) string {
	if s == "" {
		return "1"
	}
	return s
}

func DimensionName(d Dimension) string {
	parts := []string{}
	addPart := func(s string, n int) {
		if n == 0 {
			return
		}
		if n == 1 {
			parts = append(parts, s)
		} else {
			parts = append(parts, fmt.Sprintf("%s^%d", s, n))
		}
	}
	addPart("kg", d.Kilogram)
	addPart("m", d.Meter)
	addPart("s", d.Second)
	addPart("A", d.Ampere)
	addPart("K", d.Kelvin)
	addPart("mol", d.Mole)
	addPart("cd", d.Candela)
	if len(parts) == 0 {
		return "dimensionless"
	}
	return strings.Join(parts, " ")
}
