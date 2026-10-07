package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// UnitDefinition describes one registered unit of measure: the dimension it
// belongs to and the affine rule that converts a value in this unit to the
// dimension's canonical unit (canonical = scale*value + offset). The basis
// records the steward's reference for the rule so later revisions stay
// auditable. Revising a definition never rewrites quantities that were
// already recorded with an earlier revision.
type UnitDefinition struct {
	Symbol     string    `json:"symbol"`
	Dimension  string    `json:"dimension"`
	Scale      float64   `json:"scale"`
	Offset     float64   `json:"offset"`
	Basis      string    `json:"basis"`
	Maintainer string    `json:"maintainer"`
	Revision   int       `json:"revision"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewUnitDefinition(symbol, dimension string, scale, offset float64, basis, maintainer string, at time.Time) (UnitDefinition, error) {
	item := UnitDefinition{
		Symbol:     NormalizeUnitSymbol(symbol),
		Dimension:  NormalizeDimension(dimension),
		Scale:      scale,
		Offset:     offset,
		Basis:      strings.TrimSpace(basis),
		Maintainer: strings.TrimSpace(maintainer),
		Revision:   1,
		CreatedAt:  at.UTC(),
		UpdatedAt:  at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return UnitDefinition{}, err
	}
	return item, nil
}

// NormalizeUnitSymbol trims surrounding whitespace but keeps the case:
// unit symbols are case-sensitive (mV and MV are different units).
func NormalizeUnitSymbol(value string) string {
	return strings.TrimSpace(value)
}

func NormalizeDimension(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (u UnitDefinition) Validate() error {
	if u.Symbol == "" || u.Dimension == "" || u.Basis == "" || u.Maintainer == "" {
		return fmt.Errorf("%w: unit symbol, dimension, basis, and maintainer are required", ErrInvalidInput)
	}
	if len(u.Symbol) > 16 || strings.IndexFunc(u.Symbol, unicode.IsSpace) >= 0 {
		return fmt.Errorf("%w: unit symbol must be 1-16 characters without whitespace", ErrInvalidInput)
	}
	if !isFinite(u.Scale) || u.Scale == 0 || !isFinite(u.Offset) {
		return fmt.Errorf("%w: unit scale must be finite and non-zero and offset must be finite", ErrInvalidInput)
	}
	if u.Revision < 1 || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: unit revision and timestamps are required", ErrInvalidInput)
	}
	return nil
}

// Revise replaces the conversion rule and basis. The dimension stays fixed
// because quantities already recorded against this unit belong to it.
func (u *UnitDefinition) Revise(scale, offset float64, basis, maintainer string, at time.Time) error {
	candidate := *u
	candidate.Scale = scale
	candidate.Offset = offset
	candidate.Basis = strings.TrimSpace(basis)
	candidate.Maintainer = strings.TrimSpace(maintainer)
	candidate.Revision = u.Revision + 1
	candidate.UpdatedAt = at.UTC()
	if err := candidate.Validate(); err != nil {
		return err
	}
	*u = candidate
	return nil
}

func (u UnitDefinition) ToCanonical(value float64) float64 {
	return u.Scale*value + u.Offset
}

func (u UnitDefinition) FromCanonical(value float64) float64 {
	return (value - u.Offset) / u.Scale
}

// ConvertValue expresses a quantity recorded in one unit in another unit of
// the same dimension. Units of different dimensions are incompatible and are
// rejected explicitly instead of being compared as bare numbers.
func ConvertValue(value float64, from, to UnitDefinition) (float64, error) {
	if from.Dimension != to.Dimension {
		return 0, fmt.Errorf(
			"%w: unit %s (%s) cannot be interpreted as %s (%s)",
			ErrIncompatibleUnit, from.Symbol, from.Dimension, to.Symbol, to.Dimension,
		)
	}
	if from.Symbol == to.Symbol {
		return value, nil
	}
	return to.FromCanonical(from.ToCanonical(value)), nil
}
