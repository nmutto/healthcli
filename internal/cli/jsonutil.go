package cli

import (
	"math"
	"time"
)

// Values of todayReport.Status.
const (
	statusOK       = "ok"       // the requested date has data
	statusFallback = "fallback" // it had none; Date is the latest day that does
	statusNoData   = "no_data"  // the database has nothing at all
)

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// jsonSafe makes an overflowed daily value encodable: JSON has no Infinity, so
// it becomes the largest float (the text shows it in the huge-number format).
// NaN has no sensible number and becomes null.
func jsonSafe(v *float64) *float64 {
	if v == nil {
		return nil
	}
	switch {
	case math.IsNaN(*v):
		return nil
	case math.IsInf(*v, 1):
		m := math.MaxFloat64
		return &m
	case math.IsInf(*v, -1):
		m := -math.MaxFloat64
		return &m
	}
	return v
}
