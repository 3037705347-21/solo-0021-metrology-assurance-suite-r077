package assurance

import (
	"context"
	"fmt"
	"sort"
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

func (s *Service) RegisterUnit(ctx context.Context, input contracts.RegisterUnitRequest) (domain.UnitDefinition, error) {
	var created domain.UnitDefinition
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		symbol := domain.NormalizeUnitSymbol(input.Symbol)
		if _, exists := m.Unit(symbol); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateUnit, symbol)
		}
		item, err := domain.NewUnitDefinition(
			input.Symbol,
			input.Dimension,
			input.Scale,
			input.Offset,
			input.Basis,
			input.Maintainer,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutUnit(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"unit",
			item.Symbol,
			"",
			"unit_registered",
			item.Maintainer,
			fmt.Sprintf("unit %s registered for dimension %s: canonical = %g * value + %g (%s)",
				item.Symbol, item.Dimension, item.Scale, item.Offset, item.Basis),
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

func (s *Service) ReviseUnit(ctx context.Context, symbol string, input contracts.ReviseUnitRequest) (domain.UnitDefinition, error) {
	var revised domain.UnitDefinition
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, exists := m.Unit(domain.NormalizeUnitSymbol(symbol))
		if !exists {
			return fmt.Errorf("%w: unit %s", domain.ErrNotFound, symbol)
		}
		if err := item.Revise(input.Scale, input.Offset, input.Basis, input.Maintainer, s.now()); err != nil {
			return err
		}
		if err := m.UpdateUnit(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"unit",
			item.Symbol,
			"",
			"unit_revised",
			item.Maintainer,
			fmt.Sprintf("unit %s revised to revision %d: canonical = %g * value + %g (%s)",
				item.Symbol, item.Revision, item.Scale, item.Offset, item.Basis),
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

func (s *Service) Unit(ctx context.Context, symbol string) (domain.UnitDefinition, error) {
	var found domain.UnitDefinition
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Unit(domain.NormalizeUnitSymbol(symbol))
		if !exists {
			return fmt.Errorf("%w: unit %s", domain.ErrNotFound, symbol)
		}
		found = item
		return nil
	})
	return found, err
}

func (s *Service) Units(ctx context.Context) ([]domain.UnitDefinition, error) {
	var result []domain.UnitDefinition
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		result = snapshot.AllUnits()
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Symbol < result[j].Symbol })
	return result, nil
}

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
		unit, err := requireUnit(m, input.Unit)
		if err != nil {
			return err
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			unit.Symbol,
			unit.Dimension,
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
			fmt.Sprintf("verification case opened: nominal %g %s, tolerance %g %s, dimension %s",
				item.Nominal, item.Unit, item.Tolerance, item.Unit, item.Dimension),
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

func (s *Service) RecordMeasurement(ctx context.Context, caseID string, input contracts.RecordMeasurementRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		submitted, err := requireUnit(m, input.Unit)
		if err != nil {
			return err
		}
		if submitted.Dimension != item.Dimension {
			return fmt.Errorf("%w: case %s targets dimension %s but submitted unit %s is %s",
				domain.ErrIncompatibleUnit, item.ID, item.Dimension, submitted.Symbol, submitted.Dimension)
		}
		caseUnit, exists := m.Unit(item.Unit)
		if !exists {
			return fmt.Errorf("%w: case unit %s", domain.ErrUnknownUnit, item.Unit)
		}
		adopted, err := domain.ConvertValue(input.Observed, submitted, caseUnit)
		if err != nil {
			return err
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			input.Observed,
			submitted.Symbol,
			adopted,
			caseUnit.Symbol,
			item.Nominal,
			item.Tolerance,
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
		detail := fmt.Sprintf("observed %g %s adopted as %g %s; deviation %+g %s exceeds tolerance %g %s",
			measurement.Observed, measurement.ObservedUnit, measurement.Adopted, measurement.Unit,
			measurement.Deviation, measurement.Unit, measurement.Tolerance, measurement.Unit)
		if measurement.Accepted {
			kind = "measurement_accepted"
			detail = fmt.Sprintf("observed %g %s adopted as %g %s; deviation %+g %s within tolerance %g %s",
				measurement.Observed, measurement.ObservedUnit, measurement.Adopted, measurement.Unit,
				measurement.Deviation, measurement.Unit, measurement.Tolerance, measurement.Unit)
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
			measurement.Adopted,
			measurement.Unit,
			measurement.Deviation,
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
			fmt.Sprintf("%s; sealed value %g %s, deviation %+g %s",
				seal.Summary, seal.AdoptedValue, seal.Unit, seal.Deviation, seal.Unit),
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

// requireUnit resolves a submitted unit symbol against the registry. A blank
// symbol is invalid input and an unregistered symbol is an unknown unit;
// neither is silently accepted.
func requireUnit(m *repository.Mutation, symbol string) (domain.UnitDefinition, error) {
	normalized := domain.NormalizeUnitSymbol(symbol)
	if normalized == "" {
		return domain.UnitDefinition{}, fmt.Errorf("%w: unit is required", domain.ErrInvalidInput)
	}
	unit, exists := m.Unit(normalized)
	if !exists {
		return domain.UnitDefinition{}, fmt.Errorf("%w: %s", domain.ErrUnknownUnit, normalized)
	}
	return unit, nil
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
