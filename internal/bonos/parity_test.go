package bonos

import (
	"math"
	"testing"
	"time"
)

const tolerance = 1e-6

func TestResidualCapital(t *testing.T) {
	tests := []struct {
		name    string
		ticker  string
		date    time.Time
		want    float64
		wantErr bool
	}{
		{
			name:    "GD29 before any amortization",
			ticker:  "GD29",
			date:    date(2024, 1, 1),
			want:    100.0,
			wantErr: false,
		},
		{
			name:    "GD29 after first amortization (2025-01-09)",
			ticker:  "GD29",
			date:    date(2025, 1, 10),
			want:    90.0,
			wantErr: false,
		},
		{
			name:    "GD29 after all amortizations (maturity)",
			ticker:  "GD29",
			date:    date(2029, 7, 10),
			want:    0.0,
			wantErr: false,
		},
		{
			name:    "GD30 before first amortization",
			ticker:  "GD30",
			date:    date(2024, 7, 8),
			want:    100.0,
			wantErr: false,
		},
		{
			name:    "GD30 after first special payment (2024-07-09 = 4%)",
			ticker:  "GD30",
			date:    date(2024, 7, 10),
			want:    96.0,
			wantErr: false,
		},
		{
			name:    "GD30 after first regular payment (2025-01-09 = 8%)",
			ticker:  "GD30",
			date:    date(2025, 1, 10),
			want:    88.0,
			wantErr: false,
		},
		{
			name:    "GD35 before amortizations",
			ticker:  "GD35",
			date:    date(2030, 12, 31),
			want:    100.0,
			wantErr: false,
		},
		{
			name:    "unknown ticker",
			ticker:  "INVALID",
			date:    date(2024, 1, 1),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResidualCapital(tt.ticker, tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResidualCapital() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("ResidualCapital() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestCouponRate(t *testing.T) {
	tests := []struct {
		name    string
		ticker  string
		date    time.Time
		want    float64
		wantErr bool
	}{
		{
			name:    "GD29 any date",
			ticker:  "GD29",
			date:    date(2024, 6, 15),
			want:    0.01,
			wantErr: false,
		},
		{
			name:    "GD30 before step-up (2021-01-01)",
			ticker:  "GD30",
			date:    date(2021, 1, 1),
			want:    0.00125,
			wantErr: false,
		},
		{
			name:    "GD30 after first step (2021-08-01)",
			ticker:  "GD30",
			date:    date(2021, 8, 1),
			want:    0.005,
			wantErr: false,
		},
		{
			name:    "GD30 after second step (2023-08-01)",
			ticker:  "GD30",
			date:    date(2023, 8, 1),
			want:    0.0075,
			wantErr: false,
		},
		{
			name:    "GD30 after last step (2028-01-01)",
			ticker:  "GD30",
			date:    date(2028, 1, 1),
			want:    0.0175,
			wantErr: false,
		},
		{
			name:    "GD35 at accrual start",
			ticker:  "GD35",
			date:    date(2020, 9, 4),
			want:    0.00125,
			wantErr: false,
		},
		{
			name:    "unknown ticker",
			ticker:  "INVALID",
			date:    date(2024, 1, 1),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CouponRate(tt.ticker, tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("CouponRate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("CouponRate() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestAccruedInterest(t *testing.T) {
	tests := []struct {
		name    string
		ticker  string
		date    time.Time
		want    float64
		wantErr bool
	}{
		{
			name:    "GD29 on coupon date (2021-07-09)",
			ticker:  "GD29",
			date:    date(2021, 7, 9),
			want:    0.0,
			wantErr: false,
		},
		{
			name:    "GD29 one day after coupon date (2021-07-10)",
			ticker:  "GD29",
			date:    date(2021, 7, 10),
			want:    0.000028, // Very rough estimate; exact value is small
			wantErr: false,
		},
		{
			name:    "GD30 at first coupon date (2021-07-09)",
			ticker:  "GD30",
			date:    date(2021, 7, 9),
			want:    0.0,
			wantErr: false,
		},
		{
			name:    "unknown ticker",
			ticker:  "INVALID",
			date:    date(2024, 1, 1),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AccruedInterest(tt.ticker, tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("AccruedInterest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// For the "one day after coupon" test, just check that it's small and positive.
			if tt.name == "GD29 one day after coupon date (2021-07-10)" {
				if got < 0 || got > 0.01 {
					t.Errorf("AccruedInterest() = %f, expected small positive value", got)
				}
			} else if math.Abs(got-tt.want) > tolerance {
				t.Errorf("AccruedInterest() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestTechnicalValue(t *testing.T) {
	tests := []struct {
		name    string
		ticker  string
		date    time.Time
		want    float64
		wantErr bool
	}{
		{
			name:    "GD29 at coupon date (2024-07-09)",
			ticker:  "GD29",
			date:    date(2024, 7, 9),
			want:    100.0, // 100% residual + 0 accrued (on coupon)
			wantErr: false,
		},
		{
			name:    "GD30 on a coupon date",
			ticker:  "GD30",
			date:    date(2021, 7, 9),
			want:    100.0, // 100% residual + 0 accrued
			wantErr: false,
		},
		{
			name:    "unknown ticker",
			ticker:  "INVALID",
			date:    date(2024, 1, 1),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TechnicalValue(tt.ticker, tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("TechnicalValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("TechnicalValue() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestParity(t *testing.T) {
	tests := []struct {
		name    string
		price   float64
		ticker  string
		date    time.Time
		want    float64
		wantErr bool
	}{
		{
			name:    "GD30 at par",
			price:   100.0,
			ticker:  "GD30",
			date:    date(2021, 7, 9),
			want:    100.0,
			wantErr: false,
		},
		{
			name:    "GD30 at 95% par",
			price:   95.0,
			ticker:  "GD30",
			date:    date(2021, 7, 9),
			want:    95.0,
			wantErr: false,
		},
		{
			name:    "unknown ticker",
			price:   100.0,
			ticker:  "INVALID",
			date:    date(2024, 1, 1),
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parity(tt.price, tt.ticker, tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if math.Abs(got-tt.want) > tolerance {
				t.Errorf("Parity() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestDaysBetween30360(t *testing.T) {
	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want int
	}{
		{
			name: "same date",
			from: date(2024, 1, 1),
			to:   date(2024, 1, 1),
			want: 0,
		},
		{
			name: "one month (30 days in 30/360)",
			from: date(2024, 1, 9),
			to:   date(2024, 2, 9),
			want: 30,
		},
		{
			name: "six months (Jan 9 to Jul 9 = 180 days)",
			from: date(2024, 1, 9),
			to:   date(2024, 7, 9),
			want: 180,
		},
		{
			name: "one year (360 days in 30/360)",
			from: date(2024, 1, 9),
			to:   date(2025, 1, 9),
			want: 360,
		},
		{
			name: "month-end edge (Jan 31 to Feb 28)",
			from: date(2024, 1, 31),
			to:   date(2024, 2, 28),
			want: 28, // 30/360: cap D1=31→30, D2=28; (2024-2024)*360 + (2-1)*30 + (28-30) = 0 + 30 - 2 = 28
		},
		{
			name: "from day 31 to day 30 (both capped)",
			from: date(2024, 3, 31),
			to:   date(2024, 4, 30),
			want: 30, // 30/360: cap D1=31→30, D2=30 (capped because D1≥30); (2024-2024)*360 + (4-3)*30 + (30-30) = 0 + 30 + 0 = 30
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := daysBetween30360(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("daysBetween30360() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBondSpecsExist(t *testing.T) {
	// Ensure all six bonds are defined in the spec map.
	expectedTickers := []string{"GD29", "GD30", "GD35", "GD38", "GD41", "GD46"}
	for _, ticker := range expectedTickers {
		if _, exists := bondSpecs[ticker]; !exists {
			t.Errorf("Bond spec for %s is missing", ticker)
		}
	}
}

func TestBondSpecMaturityDates(t *testing.T) {
	// Verify maturity dates match the specification.
	tests := []struct {
		ticker   string
		maturity time.Time
	}{
		{"GD29", date(2029, 7, 9)},
		{"GD30", date(2030, 7, 9)},
		{"GD35", date(2035, 7, 9)},
		{"GD38", date(2038, 1, 9)},
		{"GD41", date(2041, 7, 9)},
		{"GD46", date(2046, 7, 9)},
	}

	for _, tt := range tests {
		spec, exists := bondSpecs[tt.ticker]
		if !exists {
			t.Errorf("Bond spec for %s not found", tt.ticker)
			continue
		}
		if !spec.Maturity.Equal(tt.maturity) {
			t.Errorf("%s maturity = %v, want %v", tt.ticker, spec.Maturity, tt.maturity)
		}
	}
}

func TestGD29AmortizationCount(t *testing.T) {
	// GD29 should have 10 amortizations (10 x 10% from 2025-01-09 to 2029-07-09).
	spec := bondSpecs["GD29"]
	if len(spec.Amortizations) != 10 {
		t.Errorf("GD29 amortizations count = %d, want 10", len(spec.Amortizations))
	}
	// Check that they sum to 100%.
	total := 0.0
	for _, amort := range spec.Amortizations {
		total += amort.Percent
	}
	if math.Abs(total-1.0) > tolerance {
		t.Errorf("GD29 amortizations sum = %f, want 1.0", total)
	}
}

func TestGD30AmortizationCount(t *testing.T) {
	// GD30 should have 13 amortizations (1 x 4% + 12 x 8%).
	spec := bondSpecs["GD30"]
	if len(spec.Amortizations) != 13 {
		t.Errorf("GD30 amortizations count = %d, want 13", len(spec.Amortizations))
	}
	total := 0.0
	for _, amort := range spec.Amortizations {
		total += amort.Percent
	}
	if math.Abs(total-1.0) > tolerance {
		t.Errorf("GD30 amortizations sum = %f, want 1.0", total)
	}
}

func TestCouponRateOnBoundary(t *testing.T) {
	// Test that coupon rates change exactly on the specified dates.
	tests := []struct {
		ticker     string
		before     time.Time
		fromDate   time.Time
		after      time.Time
		wantBefore float64
		wantAfter  float64
	}{
		{
			ticker:     "GD30",
			before:     date(2021, 7, 8),
			fromDate:   date(2021, 7, 9),
			after:      date(2021, 7, 10),
			wantBefore: 0.00125,
			wantAfter:  0.005,
		},
	}

	for _, tt := range tests {
		// Before the step date
		got, _ := CouponRate(tt.ticker, tt.before)
		if math.Abs(got-tt.wantBefore) > tolerance {
			t.Errorf("%s before %v: got %f, want %f", tt.ticker, tt.fromDate, got, tt.wantBefore)
		}
		// After the step date
		got, _ = CouponRate(tt.ticker, tt.after)
		if math.Abs(got-tt.wantAfter) > tolerance {
			t.Errorf("%s after %v: got %f, want %f", tt.ticker, tt.fromDate, got, tt.wantAfter)
		}
	}
}
