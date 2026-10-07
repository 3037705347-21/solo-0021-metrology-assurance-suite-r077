package contracts

import "example.com/solo-0021-metrology-assurance-suite/internal/domain"

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type RegisterUnitRequest struct {
	Symbol     string  `json:"symbol"`
	Dimension  string  `json:"dimension"`
	Scale      float64 `json:"scale"`
	Offset     float64 `json:"offset"`
	Basis      string  `json:"basis"`
	Maintainer string  `json:"maintainer"`
}

type ReviseUnitRequest struct {
	Scale      float64 `json:"scale"`
	Offset     float64 `json:"offset"`
	Basis      string  `json:"basis"`
	Maintainer string  `json:"maintainer"`
}

type OpenCaseRequest struct {
	DeviceID  string  `json:"device_id"`
	Nominal   float64 `json:"nominal"`
	Tolerance float64 `json:"tolerance"`
	Unit      string  `json:"unit"`
	Steward   string  `json:"steward"`
}

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
	Unit domain.UnitDefinition `json:"unit"`
}

type UnitsResponse struct {
	Units []domain.UnitDefinition `json:"units"`
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
