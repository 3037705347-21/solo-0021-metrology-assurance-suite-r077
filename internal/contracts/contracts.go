package contracts

import "example.com/solo-0021-metrology-assurance-suite/internal/domain"

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

// RegisterUnitRequest adds a unit to the steward-maintained registry. Factor
// and Offset are the conversion basis to the canonical unit of the
// dimension: canonical = value*Factor + Offset. The first unit registered
// for a dimension must be its canonical reference (factor 1, offset 0).
type RegisterUnitRequest struct {
	Symbol    string  `json:"symbol"`
	Dimension string  `json:"dimension"`
	Factor    float64 `json:"factor"`
	Offset    float64 `json:"offset"`
	Steward   string  `json:"steward"`
}

// ReviseUnitRequest replaces the conversion basis of a non-canonical unit.
// Confirmed measurements and seals keep the basis recorded at submission.
type ReviseUnitRequest struct {
	Factor float64 `json:"factor"`
	Offset float64 `json:"offset"`
	Actor  string  `json:"actor"`
}

// OpenCaseRequest fixes the target quantity of a case. NominalUnit is
// required; ToleranceUnit defaults to NominalUnit when omitted. Both units
// must be registered and share one dimension.
type OpenCaseRequest struct {
	DeviceID      string  `json:"device_id"`
	Nominal       float64 `json:"nominal"`
	NominalUnit   string  `json:"nominal_unit"`
	Tolerance     float64 `json:"tolerance"`
	ToleranceUnit string  `json:"tolerance_unit"`
	Steward       string  `json:"steward"`
}

// RecordMeasurementRequest submits one observed quantity. Unit is required
// and must be registered for the dimension of the case.
type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Unit       string  `json:"unit"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type UnitResponse struct {
	Unit domain.Unit `json:"unit"`
}

type UnitsResponse struct {
	Units []domain.Unit `json:"units"`
}

type CaseResponse struct {
	Case         domain.VerificationCase `json:"case"`
	Measurements []domain.Measurement    `json:"measurements"`
	Seal         *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events       []domain.AuditEvent     `json:"events"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
