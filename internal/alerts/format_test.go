package alerts

import (
	"math"
	"testing"
)

func TestFormatPercentageTruncatesToOneDecimal(t *testing.T) {
	cases := []struct {
		used  int64
		limit int64
		want  string
	}{
		{8996, 10000, "89,9"},
		{9000, 10000, "90,0"},
		{96636764159, 107374182400, "89,9"},
		{0, 100, "0,0"},
		{1, 3, "33,3"},
		{1, 8, "12,5"},
		{100, 1, "10000,0"},
		{1, 0, "0,0"},
	}
	for _, tc := range cases {
		if got := formatPercentage(tc.used, tc.limit); got != tc.want {
			t.Errorf("formatPercentage(%d, %d) = %q, want %q", tc.used, tc.limit, got, tc.want)
		}
	}
}

func TestPercentageBytesRoundsUp(t *testing.T) {
	cases := []struct {
		limit      int64
		percentage int
		want       int64
	}{
		{1000, 50, 500},
		{999, 50, 500},
		{101, 50, 51},
		{100, 100, 100},
		{0, 50, 0},
		{100, 0, 0},
	}
	for _, tc := range cases {
		if got := percentageBytes(tc.limit, tc.percentage); got != tc.want {
			t.Errorf("percentageBytes(%d, %d) = %d, want %d", tc.limit, tc.percentage, got, tc.want)
		}
	}
}

func TestBytesFromGiB(t *testing.T) {
	if got := bytesFromGiB(1); got != gib {
		t.Errorf("bytesFromGiB(1) = %d, want %d", got, gib)
	}
	if got := bytesFromGiB(0.5); got != gib/2 {
		t.Errorf("bytesFromGiB(0.5) = %d, want %d", got, gib/2)
	}
	if got := bytesFromGiB(-1); got != 0 {
		t.Errorf("bytesFromGiB(-1) = %d, want 0", got)
	}
	if got := bytesFromGiB(math.NaN()); got != 0 {
		t.Errorf("bytesFromGiB(NaN) = %d, want 0", got)
	}
}

func TestFormattingHelpers(t *testing.T) {
	if got := formatGiB(gib); got != "1,00 GiB" {
		t.Errorf("formatGiB(1 GiB) = %q", got)
	}
	if got := formatThousands(1234567890); got != "1.234.567.890" {
		t.Errorf("formatThousands = %q", got)
	}
	if got := formatPercentList([]int{50, 75, 90}); got != "50 %, 75 %, 90 %" {
		t.Errorf("formatPercentList = %q", got)
	}
}

func TestSeverityNames(t *testing.T) {
	cases := map[Severity]string{
		SeverityInfo:     "informativa",
		SeverityWarning:  "advertencia",
		SeverityCritical: "crítica",
		SeverityLimit:    "límite alcanzado",
	}
	for severity, want := range cases {
		if got := severity.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", severity, got, want)
		}
		if !severity.Valid() {
			t.Errorf("Severity(%d).Valid() = false", severity)
		}
	}
	if Severity(0).Valid() || Severity(5).Valid() {
		t.Error("severity outside 1..4 reported as valid")
	}
}
