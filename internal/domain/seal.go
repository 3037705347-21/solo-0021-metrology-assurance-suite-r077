package domain

import (
	"fmt"
	"strings"
	"time"
)

// EvidenceSeal is the immutable closing record for a reviewed case. It
// snapshots the adopted quantity of the selected measurement so the sealed
// evidence keeps its physical meaning even if unit conversion rules are
// revised afterwards.
type EvidenceSeal struct {
	ID            string    `json:"id"`
	CaseID        string    `json:"case_id"`
	DeviceID      string    `json:"device_id"`
	MeasurementID string    `json:"measurement_id"`
	Reviewer      string    `json:"reviewer"`
	Summary       string    `json:"summary"`
	AdoptedValue  float64   `json:"adopted_value"`
	Unit          string    `json:"unit"`
	Deviation     float64   `json:"deviation"`
	SealedAt      time.Time `json:"sealed_at"`
}

func NewEvidenceSeal(id, caseID, deviceID, measurementID, reviewer, summary string, adoptedValue float64, unit string, deviation float64, at time.Time) (EvidenceSeal, error) {
	item := EvidenceSeal{
		ID:            strings.TrimSpace(id),
		CaseID:        strings.TrimSpace(caseID),
		DeviceID:      strings.TrimSpace(deviceID),
		MeasurementID: strings.TrimSpace(measurementID),
		Reviewer:      strings.TrimSpace(reviewer),
		Summary:       strings.TrimSpace(summary),
		AdoptedValue:  adoptedValue,
		Unit:          NormalizeUnitSymbol(unit),
		Deviation:     deviation,
		SealedAt:      at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return EvidenceSeal{}, err
	}
	return item, nil
}

func (s EvidenceSeal) Validate() error {
	if s.ID == "" || s.CaseID == "" || s.DeviceID == "" || s.MeasurementID == "" || s.Reviewer == "" || s.Summary == "" {
		return fmt.Errorf("%w: seal id, case, device, measurement, reviewer, and summary are required", ErrInvalidInput)
	}
	if s.Unit == "" {
		return fmt.Errorf("%w: seal unit is required", ErrInvalidInput)
	}
	if !isFinite(s.AdoptedValue) || !isFinite(s.Deviation) {
		return fmt.Errorf("%w: sealed value and deviation must be finite", ErrInvalidInput)
	}
	if s.SealedAt.IsZero() {
		return fmt.Errorf("%w: seal time is required", ErrInvalidInput)
	}
	return nil
}
