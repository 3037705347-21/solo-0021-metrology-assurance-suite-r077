package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func mustUnit(t *testing.T, id, symbol, dimension string, factor, offset float64) Unit {
	t.Helper()
	item, err := NewUnit(id, symbol, dimension, factor, offset, time.Now())
	if err != nil {
		t.Fatalf("new unit %s: %v", symbol, err)
	}
	return item
}

func closeTo(value, expected float64) bool {
	return math.Abs(value-expected) <= 1e-9
}

func TestUnitConversionToCanonical(t *testing.T) {
	millivolt := mustUnit(t, "unit-1", "mV", "voltage", 0.001, 0)
	if got := millivolt.ToCanonical(25000); !closeTo(got, 25) {
		t.Fatalf("25000 mV in canonical units = %v, expected 25", got)
	}
	if got := millivolt.ToCanonicalDelta(500); !closeTo(got, 0.5) {
		t.Fatalf("500 mV interval in canonical units = %v, expected 0.5", got)
	}

	celsius := mustUnit(t, "unit-2", "°C", "temperature", 1, 273.15)
	if got := celsius.ToCanonical(20); !closeTo(got, 293.15) {
		t.Fatalf("20 °C in canonical units = %v, expected 293.15", got)
	}
	// A tolerance is an interval: offsets never apply to intervals.
	if got := celsius.ToCanonicalDelta(1); !closeTo(got, 1) {
		t.Fatalf("1 °C interval in canonical units = %v, expected 1", got)
	}
}

func TestUnitValidation(t *testing.T) {
	if _, err := NewUnit("unit-1", "mV", "voltage", 0, 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero factor: %v", err)
	}
	if _, err := NewUnit("unit-1", "mV", "voltage", -0.001, 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative factor: %v", err)
	}
	if _, err := NewUnit("unit-1", "mV", "voltage", math.NaN(), 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NaN factor: %v", err)
	}
	if _, err := NewUnit("unit-1", " mV ", " Voltage ", 0.001, 0, time.Now()); err != nil {
		t.Fatalf("surrounding whitespace is normalized: %v", err)
	}
	if _, err := NewUnit("unit-1", "", "voltage", 1, 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty symbol: %v", err)
	}
}

func TestUnitSymbolCaseIsSignificant(t *testing.T) {
	millivolt := mustUnit(t, "unit-1", "mV", "voltage", 0.001, 0)
	megavolt := mustUnit(t, "unit-2", "MV", "voltage", 1e6, 0)
	if millivolt.Symbol == megavolt.Symbol {
		t.Fatal("mV and MV must never be conflated")
	}
	if NormalizeDimension(" Voltage ") != "voltage" {
		t.Fatal("dimension names normalize to lowercase")
	}
	if NormalizeUnitSymbol(" mV ") != "mV" {
		t.Fatal("unit symbols keep their case")
	}
}

func TestReviseUnit(t *testing.T) {
	canonical := mustUnit(t, "unit-1", "V", "voltage", 1, 0)
	if err := canonical.Revise(2, 0, time.Now()); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("canonical unit revision: %v", err)
	}

	millivolt := mustUnit(t, "unit-2", "mV", "voltage", 0.001, 0)
	if err := millivolt.Revise(1, 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("revision must not duplicate the canonical reference: %v", err)
	}
	if err := millivolt.Revise(0, 0, time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-positive factor: %v", err)
	}
	if err := millivolt.Revise(0.002, 0, time.Now()); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if millivolt.Version != 2 || millivolt.Factor != 0.002 {
		t.Fatalf("revised unit = version %d factor %v", millivolt.Version, millivolt.Factor)
	}
}

func TestNewVerificationCaseFixesCanonicalTarget(t *testing.T) {
	volt := mustUnit(t, "unit-1", "V", "voltage", 1, 0)
	millivolt := mustUnit(t, "unit-2", "mV", "voltage", 0.001, 0)
	kelvin := mustUnit(t, "unit-3", "K", "temperature", 1, 0)

	item, err := NewVerificationCase("case-1", "device-1", 25000, 500, millivolt, millivolt, "V", "Qiao", time.Now())
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	if !closeTo(item.Nominal, 25) || !closeTo(item.Tolerance, 0.5) {
		t.Fatalf("canonical target = %v ± %v", item.Nominal, item.Tolerance)
	}
	if item.SubmittedNominal != 25000 || item.SubmittedTolerance != 500 {
		t.Fatalf("submitted quantities = %v, %v", item.SubmittedNominal, item.SubmittedTolerance)
	}
	if item.NominalUnit != "mV" || item.ToleranceUnit != "mV" || item.CanonicalUnit != "V" || item.Dimension != "voltage" {
		t.Fatalf("case quantity units = %+v", item)
	}

	if _, err := NewVerificationCase("case-2", "device-1", 25, 1, volt, kelvin, "V", "Qiao", time.Now()); !errors.Is(err, ErrIncompatibleUnit) {
		t.Fatalf("mixed dimensions must be rejected: %v", err)
	}
	if _, err := NewVerificationCase("case-3", "device-1", 25, -0.5, volt, volt, "V", "Qiao", time.Now()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative tolerance: %v", err)
	}
}

func TestNewMeasurementAdoptsSubmittedQuantity(t *testing.T) {
	millivolt := mustUnit(t, "unit-1", "mV", "voltage", 0.001, 0)

	item, err := NewMeasurement("measurement-1", "case-1", 25200, millivolt, 25, 0.5, "V", "Rui", time.Now())
	if err != nil {
		t.Fatalf("record measurement: %v", err)
	}
	if !closeTo(item.Observed, 25.2) || item.Submitted != 25200 {
		t.Fatalf("adopted = %v, submitted = %v", item.Observed, item.Submitted)
	}
	if item.Unit != "mV" || item.UnitVersion != 1 || item.ConversionFactor != 0.001 || item.ConversionOffset != 0 {
		t.Fatalf("conversion basis = %+v", item)
	}
	if !closeTo(item.Deviation, item.Observed-item.Nominal) {
		t.Fatalf("deviation %v must equal observed minus nominal", item.Deviation)
	}
	if !item.Accepted {
		t.Fatal("in-tolerance measurement was not accepted")
	}

	// The tolerance boundary stays inclusive on the full canonical deviation.
	boundary, err := NewMeasurement("measurement-2", "case-1", 25500, millivolt, 25, 0.5, "V", "Rui", time.Now())
	if err != nil {
		t.Fatalf("record boundary measurement: %v", err)
	}
	if !boundary.Accepted {
		t.Fatalf("boundary deviation %v must be accepted within tolerance %v", boundary.Deviation, boundary.Tolerance)
	}

	beyond, err := NewMeasurement("measurement-3", "case-1", 26000, millivolt, 25, 0.5, "V", "Rui", time.Now())
	if err != nil {
		t.Fatalf("record rejected measurement: %v", err)
	}
	if beyond.Accepted {
		t.Fatalf("deviation %v must exceed tolerance %v", beyond.Deviation, beyond.Tolerance)
	}
}
