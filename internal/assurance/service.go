package assurance

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type Service struct {
	repository *repository.Repository
	now        func() time.Time
}

func NewService(repo *repository.Repository) *Service {
	return &Service{
		repository: repo,
		now:        time.Now,
	}
}

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (domain.Device, error) {
	var created domain.Device
	tag := domain.NormalizeAssetTag(input.AssetTag)
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.DeviceByTag(tag); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateAssetTag, tag)
		}
		item, err := domain.NewDevice(
			m.NextID("device"),
			input.AssetTag,
			input.Model,
			input.Room,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutDevice(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			item.ID,
			"",
			"device_enrolled",
			item.Steward,
			"device enrolled for assurance work",
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	return created, err
}

func (s *Service) Device(ctx context.Context, id string) (domain.Device, error) {
	var found domain.Device
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Device(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		found = item
		return nil
	})
	return found, err
}

// RegisterUnit adds a unit to the steward-maintained registry. The first
// unit of a dimension must be its canonical reference (factor 1, offset 0);
// every later unit of the dimension carries an affine conversion basis to
// that reference.
func (s *Service) RegisterUnit(ctx context.Context, input contracts.RegisterUnitRequest) (domain.Unit, error) {
	var created domain.Unit
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		symbol := domain.NormalizeUnitSymbol(input.Symbol)
		dimension := domain.NormalizeDimension(input.Dimension)
		if _, exists := m.UnitBySymbol(symbol); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateUnitSymbol, symbol)
		}
		canonical, hasCanonical := canonicalUnitForDimension(m.AllUnits(), dimension)
		switch {
		case !hasCanonical && (input.Factor != 1 || input.Offset != 0):
			return fmt.Errorf("%w: first unit of dimension %s must be the canonical reference with factor 1 and offset 0", domain.ErrInvalidInput, dimension)
		case hasCanonical && input.Factor == 1 && input.Offset == 0:
			return fmt.Errorf("%w: dimension %s already has canonical unit %s", domain.ErrInvalidInput, dimension, canonical.Symbol)
		}
		item, err := domain.NewUnit(m.NextID("unit"), symbol, dimension, input.Factor, input.Offset, s.now())
		if err != nil {
			return err
		}
		steward := strings.TrimSpace(input.Steward)
		if steward == "" {
			return fmt.Errorf("%w: steward is required", domain.ErrInvalidInput)
		}
		if err := m.PutUnit(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"unit",
			item.ID,
			"",
			"unit_registered",
			steward,
			fmt.Sprintf("unit %s registered for dimension %s (canonical = value * %s + %s)",
				item.Symbol, item.Dimension, formatNumber(item.Factor), formatNumber(item.Offset)),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	return created, err
}

func (s *Service) Units(ctx context.Context) ([]domain.Unit, error) {
	var result []domain.Unit
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		result = snapshot.AllUnits()
		slices.SortFunc(result, func(a, b domain.Unit) int {
			return strings.Compare(a.ID, b.ID)
		})
		return nil
	})
	return result, err
}

// ReviseUnit replaces the conversion basis of a non-canonical unit. Cases
// fix their target at open time and measurements snapshot the basis at
// submission, so a revision never rewrites confirmed quantities or seals.
func (s *Service) ReviseUnit(ctx context.Context, id string, input contracts.ReviseUnitRequest) (domain.Unit, error) {
	var revised domain.Unit
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, exists := m.Unit(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: unit %s", domain.ErrNotFound, id)
		}
		previousFactor, previousOffset := item.Factor, item.Offset
		if err := item.Revise(input.Factor, input.Offset, s.now()); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		if actor == "" {
			return fmt.Errorf("%w: actor is required", domain.ErrInvalidInput)
		}
		if err := m.UpdateUnit(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"unit",
			item.ID,
			"",
			"unit_revised",
			actor,
			fmt.Sprintf("unit %s conversion revised from factor %s offset %s to factor %s offset %s; confirmed measurements keep their recorded basis",
				item.Symbol, formatNumber(previousFactor), formatNumber(previousOffset),
				formatNumber(item.Factor), formatNumber(item.Offset)),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		revised = item
		return nil
	})
	return revised, err
}

// OpenCase fixes the target quantity of the case. The submitted nominal and
// tolerance are converted into the canonical unit of their dimension once,
// at open time, and are never recomputed afterwards.
func (s *Service) OpenCase(ctx context.Context, input contracts.OpenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		deviceID := strings.TrimSpace(input.DeviceID)
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, deviceID)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			return fmt.Errorf("%w: case %s is still %s", domain.ErrActiveCaseExists, active.ID, active.State)
		}
		nominalUnit, err := requireUnit(m, input.NominalUnit)
		if err != nil {
			return err
		}
		toleranceSymbol := domain.NormalizeUnitSymbol(input.ToleranceUnit)
		if toleranceSymbol == "" {
			toleranceSymbol = nominalUnit.Symbol
		}
		toleranceUnit, err := requireUnit(m, toleranceSymbol)
		if err != nil {
			return err
		}
		canonical, hasCanonical := canonicalUnitForDimension(m.AllUnits(), nominalUnit.Dimension)
		if !hasCanonical {
			return fmt.Errorf("canonical unit missing for dimension %s", nominalUnit.Dimension)
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			nominalUnit,
			toleranceUnit,
			canonical.Symbol,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_opened",
			item.Steward,
			fmt.Sprintf("verification case opened: nominal %s, tolerance %s, adopted target %s ± %s",
				formatQuantity(item.SubmittedNominal, item.NominalUnit),
				formatQuantity(item.SubmittedTolerance, item.ToleranceUnit),
				formatQuantity(item.Nominal, item.CanonicalUnit),
				formatQuantity(item.Tolerance, item.CanonicalUnit)),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

func (s *Service) Case(ctx context.Context, id string) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Case(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result = caseResponseFromSnapshot(snapshot, item)
		return nil
	})
	return result, err
}

// RecordMeasurement adopts the submitted quantity into the canonical unit
// of the case dimension. The measurement keeps the submitted value and unit
// together with the conversion basis used at submission, so reads and
// events always restore the quantities as they were recorded.
func (s *Service) RecordMeasurement(ctx context.Context, caseID string, input contracts.RecordMeasurementRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		unit, err := requireUnit(m, input.Unit)
		if err != nil {
			return err
		}
		if unit.Dimension != item.Dimension {
			return fmt.Errorf("%w: case %s targets dimension %s but unit %s is %s",
				domain.ErrIncompatibleUnit, item.ID, item.Dimension, unit.Symbol, unit.Dimension)
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			input.Observed,
			unit,
			item.Nominal,
			item.Tolerance,
			item.CanonicalUnit,
			input.Technician,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := item.RecordMeasurement(measurement.Accepted, s.now()); err != nil {
			return err
		}
		if err := m.AppendMeasurement(measurement); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		kind := "measurement_rejected"
		detail := fmt.Sprintf("measurement %s adopted as %s exceeded tolerance and requires another measurement",
			formatQuantity(measurement.Submitted, measurement.Unit),
			formatQuantity(measurement.Observed, measurement.CanonicalUnit))
		if measurement.Accepted {
			kind = "measurement_accepted"
			detail = fmt.Sprintf("measurement %s adopted as %s is within tolerance and ready for review",
				formatQuantity(measurement.Submitted, measurement.Unit),
				formatQuantity(measurement.Observed, measurement.CanonicalUnit))
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			kind,
			measurement.Technician,
			detail,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

func (s *Service) ReopenCase(ctx context.Context, caseID string, input contracts.ReopenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if err := item.Reopen(s.now()); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_reopened",
			actor,
			reason,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

// SealCase selects the accepted measurement with the smallest absolute
// canonical deviation recorded at submission. Sealing never reconverts
// quantities, so the sealed evidence keeps the physical fact that was
// confirmed when each measurement was submitted.
func (s *Service) SealCase(ctx context.Context, caseID string, input contracts.SealCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if _, exists := m.Seal(item.ID); exists {
			return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.ID)
		}
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			input.Reviewer,
			input.Summary,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutSeal(seal); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_sealed",
			seal.Reviewer,
			seal.Summary,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

func bestAcceptedMeasurement(items []domain.Measurement) (domain.Measurement, bool) {
	var selected domain.Measurement
	found := false
	for _, item := range items {
		if !item.Accepted {
			continue
		}
		if !found || item.AbsoluteDeviation() < selected.AbsoluteDeviation() {
			selected = item
			found = true
		}
	}
	return selected, found
}

// requireUnit resolves a submitted unit symbol against the registry. A
// missing symbol is invalid input and an unregistered symbol is rejected as
// an unknown unit; neither is ever silently defaulted.
func requireUnit(m *repository.Mutation, symbol string) (domain.Unit, error) {
	normalized := domain.NormalizeUnitSymbol(symbol)
	if normalized == "" {
		return domain.Unit{}, fmt.Errorf("%w: unit is required", domain.ErrInvalidInput)
	}
	unit, exists := m.UnitBySymbol(normalized)
	if !exists {
		return domain.Unit{}, fmt.Errorf("%w: %s", domain.ErrUnknownUnit, normalized)
	}
	return unit, nil
}

func canonicalUnitForDimension(units []domain.Unit, dimension string) (domain.Unit, bool) {
	for _, item := range units {
		if item.Dimension == dimension && item.IsCanonical() {
			return item, true
		}
	}
	return domain.Unit{}, false
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func formatQuantity(value float64, unit string) string {
	return formatNumber(value) + " " + unit
}

func caseResponse(m *repository.Mutation, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: m.Measurements(item.ID),
		Events:       m.Events(item.ID),
	}
	if seal, exists := m.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}

func caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: snapshot.Measurements(item.ID),
		Events:       snapshot.Events(item.ID),
	}
	if seal, exists := snapshot.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}
