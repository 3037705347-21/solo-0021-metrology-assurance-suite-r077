package domain

import "errors"

var (
	ErrInvalidInput        = errors.New("invalid input")
	ErrNotFound            = errors.New("resource not found")
	ErrDuplicateAssetTag   = errors.New("duplicate asset tag")
	ErrDuplicateUnitSymbol = errors.New("duplicate unit symbol")
	ErrActiveCaseExists    = errors.New("device already has an unfinished case")
	ErrStateConflict       = errors.New("resource state conflict")
	ErrSealAlreadyCreated  = errors.New("case already has an evidence seal")
	ErrUnknownUnit         = errors.New("unit is not registered")
	ErrIncompatibleUnit    = errors.New("unit dimension is incompatible")
)

func IsKnownError(err error) bool {
	return errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrDuplicateAssetTag) ||
		errors.Is(err, ErrDuplicateUnitSymbol) ||
		errors.Is(err, ErrActiveCaseExists) ||
		errors.Is(err, ErrStateConflict) ||
		errors.Is(err, ErrSealAlreadyCreated) ||
		errors.Is(err, ErrUnknownUnit) ||
		errors.Is(err, ErrIncompatibleUnit)
}
