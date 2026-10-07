package domain

import "errors"

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrNotFound           = errors.New("resource not found")
	ErrDuplicateAssetTag  = errors.New("duplicate asset tag")
	ErrActiveCaseExists   = errors.New("device already has an unfinished case")
	ErrStateConflict      = errors.New("resource state conflict")
	ErrSealAlreadyCreated = errors.New("case already has an evidence seal")
	ErrUnknownUnit        = errors.New("unknown unit of measure")
	ErrIncompatibleUnit   = errors.New("incompatible unit of measure")
	ErrDuplicateUnit      = errors.New("duplicate unit symbol")
)

func IsKnownError(err error) bool {
	return errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrDuplicateAssetTag) ||
		errors.Is(err, ErrActiveCaseExists) ||
		errors.Is(err, ErrStateConflict) ||
		errors.Is(err, ErrSealAlreadyCreated) ||
		errors.Is(err, ErrUnknownUnit) ||
		errors.Is(err, ErrIncompatibleUnit) ||
		errors.Is(err, ErrDuplicateUnit)
}
