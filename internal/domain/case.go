package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type CaseState string

const (
	CaseAwaitingMeasurement CaseState = "awaiting_measurement"
	CaseReviewReady         CaseState = "review_ready"
	CaseRecheckRequired     CaseState = "recheck_required"
	CaseSealed              CaseState = "sealed"
)

// VerificationCase fixes its target quantity at open time. Nominal and
// Tolerance are the canonical values the case is judged against; the
// Submitted* fields keep the quantities exactly as the steward entered
// them. Neither is ever recomputed, so later unit revisions cannot move an
// opened case.
type VerificationCase struct {
	ID                 string     `json:"id"`
	DeviceID           string     `json:"device_id"`
	Nominal            float64    `json:"nominal"`
	Tolerance          float64    `json:"tolerance"`
	CanonicalUnit      string     `json:"canonical_unit"`
	Dimension          string     `json:"dimension"`
	SubmittedNominal   float64    `json:"submitted_nominal"`
	NominalUnit        string     `json:"nominal_unit"`
	SubmittedTolerance float64    `json:"submitted_tolerance"`
	ToleranceUnit      string     `json:"tolerance_unit"`
	Steward            string     `json:"steward"`
	State              CaseState  `json:"state"`
	Version            int        `json:"version"`
	OpenedAt           time.Time  `json:"opened_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	SealedAt           *time.Time `json:"sealed_at,omitempty"`
}

func NewVerificationCase(id, deviceID string, nominal, tolerance float64, nominalUnit, toleranceUnit Unit, canonicalUnit, steward string, at time.Time) (VerificationCase, error) {
	if nominalUnit.Dimension != toleranceUnit.Dimension {
		return VerificationCase{}, fmt.Errorf("%w: nominal unit %s is %s but tolerance unit %s is %s",
			ErrIncompatibleUnit, nominalUnit.Symbol, nominalUnit.Dimension, toleranceUnit.Symbol, toleranceUnit.Dimension)
	}
	item := VerificationCase{
		ID:                 strings.TrimSpace(id),
		DeviceID:           strings.TrimSpace(deviceID),
		Nominal:            nominalUnit.ToCanonical(nominal),
		Tolerance:          toleranceUnit.ToCanonicalDelta(tolerance),
		CanonicalUnit:      strings.TrimSpace(canonicalUnit),
		Dimension:          nominalUnit.Dimension,
		SubmittedNominal:   nominal,
		NominalUnit:        nominalUnit.Symbol,
		SubmittedTolerance: tolerance,
		ToleranceUnit:      toleranceUnit.Symbol,
		Steward:            strings.TrimSpace(steward),
		State:              CaseAwaitingMeasurement,
		Version:            1,
		OpenedAt:           at.UTC(),
		UpdatedAt:          at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return VerificationCase{}, err
	}
	return item, nil
}

func (c VerificationCase) Validate() error {
	if c.ID == "" || c.DeviceID == "" || c.Steward == "" {
		return fmt.Errorf("%w: case id, device id, and steward are required", ErrInvalidInput)
	}
	if c.Dimension == "" || c.CanonicalUnit == "" || c.NominalUnit == "" || c.ToleranceUnit == "" {
		return fmt.Errorf("%w: case dimension and quantity units are required", ErrInvalidInput)
	}
	if !isFinite(c.Nominal) || !isFinite(c.Tolerance) || c.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if !isFinite(c.SubmittedNominal) || !isFinite(c.SubmittedTolerance) || c.SubmittedTolerance <= 0 {
		return fmt.Errorf("%w: submitted nominal must be finite and submitted tolerance must be positive", ErrInvalidInput)
	}
	if c.Version < 1 || c.OpenedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: case version and timestamps are required", ErrInvalidInput)
	}
	switch c.State {
	case CaseAwaitingMeasurement, CaseReviewReady, CaseRecheckRequired, CaseSealed:
		return nil
	default:
		return fmt.Errorf("%w: unsupported case state %q", ErrInvalidInput, c.State)
	}
}

func (c VerificationCase) IsUnfinished() bool {
	return c.State != CaseSealed
}

func (c *VerificationCase) RecordMeasurement(accepted bool, at time.Time) error {
	if c.State != CaseAwaitingMeasurement {
		return fmt.Errorf("%w: case is not awaiting measurement", ErrStateConflict)
	}
	if accepted {
		c.State = CaseReviewReady
	} else {
		c.State = CaseRecheckRequired
	}
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Reopen(at time.Time) error {
	if c.State != CaseRecheckRequired {
		return fmt.Errorf("%w: only a recheck_required case can be reopened", ErrStateConflict)
	}
	c.State = CaseAwaitingMeasurement
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Seal(at time.Time) error {
	if c.State != CaseReviewReady {
		return fmt.Errorf("%w: case is not ready for review", ErrStateConflict)
	}
	stamp := at.UTC()
	c.State = CaseSealed
	c.Version++
	c.UpdatedAt = stamp
	c.SealedAt = &stamp
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
