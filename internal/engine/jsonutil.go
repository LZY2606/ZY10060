package engine

import "encoding/json"

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func numField(x float64) any {
	if x != x || x > 1e300 || x < -1e300 {
		return nil
	}
	return x
}
func lowField(nr NodeResult) any {
	if nr.Redacted {
		return nil
	}
	return numField(nr.IntervalLow)
}
func highField(nr NodeResult) any {
	if nr.Redacted {
		return nil
	}
	return numField(nr.IntervalHigh)
}
func kField(nr NodeResult) any {
	if nr.Redacted {
		return nil
	}
	return nr.K
}
