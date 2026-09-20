package units_test

import (
	"math"
	"testing"

	"metrologylab/internal/units"
)

func TestTemperatureAndLinearConversions(t *testing.T) {
	v, path, err := units.Convert(0, "°C", "K")
	if err != nil || math.Abs(v-273.15) > 1e-12 {
		t.Fatalf("celsius=%v path=%v err=%v", v, path, err)
	}
	v, _, err = units.Convert(10, "mm", "m")
	if err != nil || math.Abs(v-0.01) > 1e-15 {
		t.Fatalf("mm=%v err=%v", v, err)
	}
	if units.Compatible("mm", "kg") {
		t.Fatal("dimension mismatch accepted")
	}
}
