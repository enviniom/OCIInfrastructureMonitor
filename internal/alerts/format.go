package alerts

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// gib is one gibibyte (2^30 bytes), the only unit accepted by the
// configuration.
const gib = int64(1 << 30)

// bytesFromGiB converts a GiB value to exact bytes. The configuration is
// validated as finite and positive, but the evaluators must never panic on a
// hand-built value.
func bytesFromGiB(value float64) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value > float64(math.MaxInt64)/float64(gib) {
		return math.MaxInt64
	}
	return int64(math.Round(value * float64(gib)))
}

// formatGiB renders bytes as GiB with two decimals and a decimal comma.
func formatGiB(bytes int64) string {
	value := strconv.FormatFloat(float64(bytes)/float64(gib), 'f', 2, 64)
	return strings.Replace(value, ".", ",", 1) + " GiB"
}

// formatThousands groups the digits of value with dots, as used in es-ES.
func formatThousands(value int64) string {
	digits := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = strings.TrimPrefix(digits, "-")
	}
	var out strings.Builder
	out.Grow(len(digits) + len(digits)/3 + 1)
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte('.')
		}
		out.WriteByte(digits[i])
	}
	if negative {
		return "-" + out.String()
	}
	return out.String()
}

// formatTimestamp renders an instant as a UTC timestamp without seconds, which
// is enough for an hourly window.
func formatTimestamp(at time.Time) string {
	return at.UTC().Format("2006-01-02 15:04")
}
