// Package bonos provides contractual tables and parity calculations for
// Argentine government bonds (GD29, GD30, GD35, GD38, GD41, GD46).
//
// Bond contracts define coupon schedules and amortization plans. This package
// exposes the static data and pure functions to compute residual capital,
// accrued interest, and technical value (valor técnico) on any given date.
//
// All parity calculations use the 30/360 day-count convention.
package bonos

import (
	"fmt"
	"time"
)

// CouponStep represents a single step in a bond's coupon schedule.
type CouponStep struct {
	// From is the date when this coupon rate becomes effective.
	From time.Time
	// Rate is the annual coupon rate (e.g. 0.01 for 1%).
	Rate float64
}

// Amortization represents a single amortization payment in the bond's schedule.
type Amortization struct {
	// Date is the amortization date.
	Date time.Time
	// Percent is the fraction of original face value paid (e.g. 0.10 for 10%).
	Percent float64
}

// BondSpec is the complete contractual specification of a single bond.
type BondSpec struct {
	// Ticker is the bond identifier (e.g. "GD30").
	Ticker string
	// Maturity is the final maturity date.
	Maturity time.Time
	// CouponSteps are the coupon rate steps, sorted by date ascending.
	CouponSteps []CouponStep
	// Amortizations are the scheduled amortization payments, sorted by date
	// ascending.
	Amortizations []Amortization
}

// bondSpecs holds the static contractual data for all six bonds.
// Data is sourced from SEC prospectuses and bond documentation.
var bondSpecs = map[string]BondSpec{
	"GD29": {
		Ticker:   "GD29",
		Maturity: date(2029, 7, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.01},
		},
		Amortizations: []Amortization{
			{Date: date(2025, 1, 9), Percent: 0.10},
			{Date: date(2025, 7, 9), Percent: 0.10},
			{Date: date(2026, 1, 9), Percent: 0.10},
			{Date: date(2026, 7, 9), Percent: 0.10},
			{Date: date(2027, 1, 9), Percent: 0.10},
			{Date: date(2027, 7, 9), Percent: 0.10},
			{Date: date(2028, 1, 9), Percent: 0.10},
			{Date: date(2028, 7, 9), Percent: 0.10},
			{Date: date(2029, 1, 9), Percent: 0.10},
			{Date: date(2029, 7, 9), Percent: 0.10},
		},
	},
	"GD30": {
		Ticker:   "GD30",
		Maturity: date(2030, 7, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.00125},
			{From: date(2021, 7, 9), Rate: 0.005},
			{From: date(2023, 7, 9), Rate: 0.0075},
			{From: date(2027, 7, 9), Rate: 0.0175},
		},
		Amortizations: []Amortization{
			{Date: date(2024, 7, 9), Percent: 0.04},
			{Date: date(2025, 1, 9), Percent: 0.08},
			{Date: date(2025, 7, 9), Percent: 0.08},
			{Date: date(2026, 1, 9), Percent: 0.08},
			{Date: date(2026, 7, 9), Percent: 0.08},
			{Date: date(2027, 1, 9), Percent: 0.08},
			{Date: date(2027, 7, 9), Percent: 0.08},
			{Date: date(2028, 1, 9), Percent: 0.08},
			{Date: date(2028, 7, 9), Percent: 0.08},
			{Date: date(2029, 1, 9), Percent: 0.08},
			{Date: date(2029, 7, 9), Percent: 0.08},
			{Date: date(2030, 1, 9), Percent: 0.08},
			{Date: date(2030, 7, 9), Percent: 0.08},
		},
	},
	"GD35": {
		Ticker:   "GD35",
		Maturity: date(2035, 7, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.00125},
			{From: date(2021, 7, 9), Rate: 0.01125},
			{From: date(2022, 7, 9), Rate: 0.015},
			{From: date(2023, 7, 9), Rate: 0.03625},
			{From: date(2024, 7, 9), Rate: 0.04125},
			{From: date(2027, 7, 9), Rate: 0.0475},
			{From: date(2028, 7, 9), Rate: 0.05},
		},
		Amortizations: []Amortization{
			{Date: date(2031, 1, 9), Percent: 0.10},
			{Date: date(2031, 7, 9), Percent: 0.10},
			{Date: date(2032, 1, 9), Percent: 0.10},
			{Date: date(2032, 7, 9), Percent: 0.10},
			{Date: date(2033, 1, 9), Percent: 0.10},
			{Date: date(2033, 7, 9), Percent: 0.10},
			{Date: date(2034, 1, 9), Percent: 0.10},
			{Date: date(2034, 7, 9), Percent: 0.10},
			{Date: date(2035, 1, 9), Percent: 0.10},
			{Date: date(2035, 7, 9), Percent: 0.10},
		},
	},
	"GD38": {
		Ticker:   "GD38",
		Maturity: date(2038, 1, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.00125},
			{From: date(2021, 7, 9), Rate: 0.02},
			{From: date(2022, 7, 9), Rate: 0.03875},
			{From: date(2023, 7, 9), Rate: 0.0425},
			{From: date(2024, 7, 9), Rate: 0.05},
		},
		Amortizations: amortizationSeries(22, date(2027, 7, 9), date(2038, 1, 9)),
	},
	"GD41": {
		Ticker:   "GD41",
		Maturity: date(2041, 7, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.00125},
			{From: date(2021, 7, 9), Rate: 0.025},
			{From: date(2022, 7, 9), Rate: 0.035},
			{From: date(2029, 7, 9), Rate: 0.04875},
		},
		Amortizations: amortizationSeries(28, date(2028, 1, 9), date(2041, 7, 9)),
	},
	"GD46": {
		Ticker:   "GD46",
		Maturity: date(2046, 7, 9),
		CouponSteps: []CouponStep{
			{From: date(2020, 9, 4), Rate: 0.00125},
			{From: date(2021, 7, 9), Rate: 0.01125},
			{From: date(2022, 7, 9), Rate: 0.015},
			{From: date(2023, 7, 9), Rate: 0.03625},
			{From: date(2024, 7, 9), Rate: 0.04125},
			{From: date(2027, 7, 9), Rate: 0.04375},
			{From: date(2028, 7, 9), Rate: 0.05},
		},
		Amortizations: amortizationSeries(44, date(2025, 1, 9), date(2046, 7, 9)),
	},
}

// amortizationSeries generates a series of equal amortization payments
// (as fractions) from a start date through a maturity date on the Jan 9 / Jul 9
// semiannual schedule. For example, 22 periods of 100/22% each.
func amortizationSeries(numPayments int, start, maturity time.Time) []Amortization {
	amorts := make([]Amortization, 0, numPayments)
	percentPerPayment := 1.0 / float64(numPayments)

	current := start
	for {
		amorts = append(amorts, Amortization{Date: current, Percent: percentPerPayment})
		if current.Equal(maturity) || current.After(maturity) {
			break
		}
		// Advance to the next semiannual date (Jan 9 or Jul 9).
		current = nextSemiannual(current)
	}
	return amorts
}

// nextSemiannual returns the next Jan 9 or Jul 9 after the given date.
// If the date is Jan 9, return Jul 9 of the same year; if Jul 9, return Jan 9
// of the next year.
func nextSemiannual(t time.Time) time.Time {
	y, m, d := t.Date()
	if m < 7 || (m == 7 && d < 9) {
		return date(y, 7, 9)
	}
	return date(y+1, 1, 9)
}

// date is a helper to construct a time.Time at midnight in UTC.
// (All bond dates are calendar dates with no time zone semantics.)
func date(year, month, day int) time.Time {
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// spec returns the BondSpec for the given ticker, or an error if the ticker
// is unknown.
func spec(ticker string) (BondSpec, error) {
	s, ok := bondSpecs[ticker]
	if !ok {
		return BondSpec{}, fmt.Errorf("bonos: unknown ticker %q", ticker)
	}
	return s, nil
}

// ResidualCapital returns the remaining capital per VN 100 on the given date.
// It is calculated as 100 minus the sum of all amortization percentages
// (as fractions of original face) on or before the date.
//
// For example, after a 10% amortization, ResidualCapital returns 90.0.
func ResidualCapital(ticker string, date time.Time) (float64, error) {
	s, err := spec(ticker)
	if err != nil {
		return 0, err
	}

	amortized := 0.0
	for _, amort := range s.Amortizations {
		if amort.Date.After(date) {
			break
		}
		amortized += amort.Percent
	}
	return 100.0 * (1.0 - amortized), nil
}

// CouponRate returns the annual coupon rate in effect on the given date.
// It is determined by the coupon step schedule.
func CouponRate(ticker string, date time.Time) (float64, error) {
	s, err := spec(ticker)
	if err != nil {
		return 0, err
	}

	// Find the last coupon step on or before the date.
	var rate float64
	for _, step := range s.CouponSteps {
		if step.From.After(date) {
			break
		}
		rate = step.Rate
	}
	return rate, nil
}

// AccruedInterest returns the accrued interest per VN 100 on the given date.
// It uses the 30/360 day-count convention and assumes semiannual coupon periods
// (Jan 9 and Jul 9). The formula is:
//
//	IC = ResidualCapital × annual_coupon_rate × days_since_last_coupon / 360
//
// For dates before 2021-07-09 (the first coupon date), the accrual start is
// 2020-09-04. For dates on or after 2021-07-09, the last coupon boundary is
// the most recent Jan 9 or Jul 9 on or before the date.
func AccruedInterest(ticker string, d time.Time) (float64, error) {
	_, err := spec(ticker)
	if err != nil {
		return 0, err
	}

	// Find the last coupon date on or before the given date.
	// Coupon dates are Jan 9 and Jul 9 semiannually, starting from 2021-07-09.
	var lastCoupon time.Time
	if d.Before(date(2021, 7, 9)) {
		// Before the first coupon date, use the accrual start.
		lastCoupon = date(2020, 9, 4)
	} else {
		// Find the most recent Jan 9 or Jul 9 on or before the date.
		y, m, day := d.Date()
		if m < 7 || (m == 7 && day < 9) {
			// Before Jul 9 this year, use Jan 9 this year.
			lastCoupon = time.Date(y, 1, 9, 0, 0, 0, 0, time.UTC)
		} else if m == 7 && day >= 9 {
			// On or after Jul 9 this year, use Jul 9 this year.
			lastCoupon = time.Date(y, 7, 9, 0, 0, 0, 0, time.UTC)
		} else {
			// After Jul 9 (in Aug-Dec), use Jul 9 this year.
			lastCoupon = time.Date(y, 7, 9, 0, 0, 0, 0, time.UTC)
		}
	}

	// Compute days since last coupon using 30/360 convention.
	days := daysBetween30360(lastCoupon, d)

	// Get the coupon rate and residual capital on the given date.
	couponRate, err := CouponRate(ticker, d)
	if err != nil {
		return 0, err
	}
	residual, err := ResidualCapital(ticker, d)
	if err != nil {
		return 0, err
	}

	// IC = VR × coupon_rate × days / 360
	accrued := residual * couponRate * float64(days) / 360.0
	return accrued, nil
}

// TechnicalValue returns the valor técnico (VR + IC) per VN 100 on the given date.
// It is the sum of residual capital and accrued interest.
func TechnicalValue(ticker string, d time.Time) (float64, error) {
	residual, err := ResidualCapital(ticker, d)
	if err != nil {
		return 0, err
	}
	accrued, err := AccruedInterest(ticker, d)
	if err != nil {
		return 0, err
	}
	return residual + accrued, nil
}

// Parity returns the parity percentage: market_price_usd / technical_value × 100.
// The market price is typically quoted in USD and the technical value is per
// VN 100. The result is a percentage (e.g. 95.5 for 95.5%).
func Parity(priceUSD float64, ticker string, d time.Time) (float64, error) {
	tv, err := TechnicalValue(ticker, d)
	if err != nil {
		return 0, err
	}
	if tv == 0 {
		return 0, fmt.Errorf("bonos: technical value is zero, cannot compute parity")
	}
	return (priceUSD / tv) * 100.0, nil
}

// daysBetween30360 computes the number of days between two dates using the
// 30/360 day-count convention. This convention is used in bond interest
// calculations.
//
// The formula is: days = (Y2-Y1)*360 + (M2-M1)*30 + (D2-D1)
// where day values are capped at 30: if D1 >= 30, set D1 = 30; if D2 >= 30
// and D1 >= 30, set D2 = 30.
func daysBetween30360(from, to time.Time) int {
	y1, m1, d1 := from.Date()
	y2, m2, d2 := to.Date()

	// Cap day values at 30.
	if d1 >= 30 {
		d1 = 30
	}
	if d2 >= 30 && d1 >= 30 {
		d2 = 30
	}

	days := (y2-y1)*360 + (int(m2)-int(m1))*30 + (d2 - d1)
	return days
}
