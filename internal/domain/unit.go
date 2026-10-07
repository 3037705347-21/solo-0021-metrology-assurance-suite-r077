package domain

import (
	"fmt"
	"strings"
	"time"
)

// Unit is a steward-maintained metrological unit. Factor and Offset express
// the affine conversion from this unit to the canonical unit of its
// dimension: canonical = value*Factor + Offset. The first unit registered
// for a dimension must be its canonical reference (Factor 1, Offset 0) and
// can never be revised, so confirmed quantities keep a stable meaning.
type Unit struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Dimension string    `json:"dimension"`
	Factor    float64   `json:"factor"`
	Offset    float64   `json:"offset"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func NewUnit(id, symbol, dimension string, factor, offset float64, at time.Time) (Unit, error) {
	item := Unit{
		ID:        strings.TrimSpace(id),
		Symbol:    NormalizeUnitSymbol(symbol),
		Dimension: NormalizeDimension(dimension),
		Factor:    factor,
		Offset:    offset,
		Version:   1,
		CreatedAt: at.UTC(),
		UpdatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return Unit{}, err
	}
	return item, nil
}

// NormalizeUnitSymbol trims surrounding whitespace. Symbol case is
// significant: mV and MV are different units and must never be conflated.
func NormalizeUnitSymbol(value string) string {
	return strings.TrimSpace(value)
}

// NormalizeDimension folds a dimension name to a single lowercase form so
// "Voltage" and "voltage" name the same physical quantity.
func NormalizeDimension(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (u Unit) Validate() error {
	if u.ID == "" || u.Symbol == "" || u.Dimension == "" {
		return fmt.Errorf("%w: unit id, symbol, and dimension are required", ErrInvalidInput)
	}
	if len(u.Symbol) > 32 || len(u.Dimension) > 64 {
		return fmt.Errorf("%w: unit symbol or dimension is too long", ErrInvalidInput)
	}
	if !isFinite(u.Factor) || u.Factor <= 0 || !isFinite(u.Offset) {
		return fmt.Errorf("%w: unit factor must be positive and offset must be finite", ErrInvalidInput)
	}
	if u.Version < 1 || u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: unit version and timestamps are required", ErrInvalidInput)
	}
	return nil
}

// IsCanonical reports whether the unit is the canonical reference of its
// dimension.
func (u Unit) IsCanonical() bool {
	return u.Factor == 1 && u.Offset == 0
}

// ToCanonical converts a point quantity (for example 20 °C) into the
// canonical unit of the dimension.
func (u Unit) ToCanonical(value float64) float64 {
	return value*u.Factor + u.Offset
}

// ToCanonicalDelta converts an interval quantity (for example a tolerance)
// into the canonical unit. Offsets never apply to intervals: a 1 °C
// tolerance is a 1 K tolerance.
func (u Unit) ToCanonicalDelta(value float64) float64 {
	return value * u.Factor
}

// Revise replaces the conversion basis of a non-canonical unit. Confirmed
// measurements and seals snapshot the basis at submission, so a revision
// never rewrites recorded evidence.
func (u *Unit) Revise(factor, offset float64, at time.Time) error {
	if u.IsCanonical() {
		return fmt.Errorf("%w: canonical unit %s of dimension %s cannot be revised", ErrStateConflict, u.Symbol, u.Dimension)
	}
	if !isFinite(factor) || factor <= 0 || !isFinite(offset) {
		return fmt.Errorf("%w: unit factor must be positive and offset must be finite", ErrInvalidInput)
	}
	if factor == 1 && offset == 0 {
		return fmt.Errorf("%w: revised conversion would duplicate the canonical reference of dimension %s", ErrInvalidInput, u.Dimension)
	}
	u.Factor = factor
	u.Offset = offset
	u.Version++
	u.UpdatedAt = at.UTC()
	return nil
}
