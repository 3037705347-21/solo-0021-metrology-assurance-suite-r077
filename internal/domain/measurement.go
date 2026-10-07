package domain

import (
	"fmt"
	"strings"
	"time"
)

// Measurement records one observed quantity. Submitted and Unit keep the
// value exactly as the technician entered it; Observed is the same physical
// quantity adopted into the canonical unit of the case dimension. The
// conversion basis (UnitVersion, ConversionFactor, ConversionOffset) is
// snapshotted at submission, so later unit revisions never rewrite a
// confirmed measurement.
type Measurement struct {
	ID               string    `json:"id"`
	CaseID           string    `json:"case_id"`
	Observed         float64   `json:"observed"`
	Submitted        float64   `json:"submitted"`
	Unit             string    `json:"unit"`
	UnitVersion      int       `json:"unit_version"`
	ConversionFactor float64   `json:"conversion_factor"`
	ConversionOffset float64   `json:"conversion_offset"`
	CanonicalUnit    string    `json:"canonical_unit"`
	Nominal          float64   `json:"nominal"`
	Deviation        float64   `json:"deviation"`
	Tolerance        float64   `json:"tolerance"`
	Accepted         bool      `json:"accepted"`
	Technician       string    `json:"technician"`
	CreatedAt        time.Time `json:"created_at"`
}

func NewMeasurement(id, caseID string, submitted float64, unit Unit, nominal, tolerance float64, canonicalUnit, technician string, at time.Time) (Measurement, error) {
	observed := unit.ToCanonical(submitted)
	item := Measurement{
		ID:               strings.TrimSpace(id),
		CaseID:           strings.TrimSpace(caseID),
		Observed:         observed,
		Submitted:        submitted,
		Unit:             unit.Symbol,
		UnitVersion:      unit.Version,
		ConversionFactor: unit.Factor,
		ConversionOffset: unit.Offset,
		CanonicalUnit:    strings.TrimSpace(canonicalUnit),
		Nominal:          nominal,
		Deviation:        observed - nominal,
		Tolerance:        tolerance,
		Accepted:         abs(observed-nominal) <= tolerance,
		Technician:       strings.TrimSpace(technician),
		CreatedAt:        at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return Measurement{}, err
	}
	return item, nil
}

func (m Measurement) Validate() error {
	if m.ID == "" || m.CaseID == "" || m.Technician == "" {
		return fmt.Errorf("%w: measurement id, case id, and technician are required", ErrInvalidInput)
	}
	if m.Unit == "" || m.CanonicalUnit == "" {
		return fmt.Errorf("%w: measurement unit and canonical unit are required", ErrInvalidInput)
	}
	if m.UnitVersion < 1 || !isFinite(m.ConversionFactor) || m.ConversionFactor <= 0 || !isFinite(m.ConversionOffset) {
		return fmt.Errorf("%w: measurement conversion basis is required", ErrInvalidInput)
	}
	if !isFinite(m.Submitted) || !isFinite(m.Observed) || !isFinite(m.Nominal) || !isFinite(m.Tolerance) || m.Tolerance <= 0 {
		return fmt.Errorf("%w: submitted, observed, and nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: measurement time is required", ErrInvalidInput)
	}
	return nil
}

func (m Measurement) AbsoluteDeviation() float64 {
	return abs(m.Deviation)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
