package domain

import (
	"fmt"
	"strings"
	"time"
)

// Measurement keeps both the quantity exactly as the technician submitted it
// (Observed in ObservedUnit) and the quantity the case adopted after unit
// conversion (Adopted in the case Unit). Both are fixed at record time, so
// later changes to conversion rules never rewrite a recorded measurement.
type Measurement struct {
	ID           string    `json:"id"`
	CaseID       string    `json:"case_id"`
	Observed     float64   `json:"observed"`
	ObservedUnit string    `json:"observed_unit"`
	Adopted      float64   `json:"adopted"`
	Unit         string    `json:"unit"`
	Nominal      float64   `json:"nominal"`
	Deviation    float64   `json:"deviation"`
	Tolerance    float64   `json:"tolerance"`
	Accepted     bool      `json:"accepted"`
	Technician   string    `json:"technician"`
	CreatedAt    time.Time `json:"created_at"`
}

func NewMeasurement(id, caseID string, observed float64, observedUnit string, adopted float64, unit string, nominal, tolerance float64, technician string, at time.Time) (Measurement, error) {
	item := Measurement{
		ID:           strings.TrimSpace(id),
		CaseID:       strings.TrimSpace(caseID),
		Observed:     observed,
		ObservedUnit: NormalizeUnitSymbol(observedUnit),
		Adopted:      adopted,
		Unit:         NormalizeUnitSymbol(unit),
		Nominal:      nominal,
		Deviation:    adopted - nominal,
		Tolerance:    tolerance,
		Accepted:     abs(adopted-nominal) <= tolerance,
		Technician:   strings.TrimSpace(technician),
		CreatedAt:    at.UTC(),
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
	if m.ObservedUnit == "" || m.Unit == "" {
		return fmt.Errorf("%w: observed unit and case unit are required", ErrInvalidInput)
	}
	if !isFinite(m.Observed) || !isFinite(m.Adopted) || !isFinite(m.Nominal) || !isFinite(m.Tolerance) || m.Tolerance <= 0 {
		return fmt.Errorf("%w: observed, adopted, and nominal must be finite and tolerance must be positive", ErrInvalidInput)
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
