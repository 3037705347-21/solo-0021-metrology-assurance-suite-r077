package assurance

import (
	"context"
	"errors"
	"math"
	"testing"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func closeTo(value, expected float64) bool {
	return math.Abs(value-expected) <= 1e-9
}

func mustRegisterUnit(t *testing.T, service *Service, symbol, dimension string, factor, offset float64) domain.Unit {
	t.Helper()
	item, err := service.RegisterUnit(context.Background(), contracts.RegisterUnitRequest{
		Symbol:    symbol,
		Dimension: dimension,
		Factor:    factor,
		Offset:    offset,
		Steward:   "Lin",
	})
	if err != nil {
		t.Fatalf("register unit %s: %v", symbol, err)
	}
	return item
}

func mustDevice(t *testing.T, service *Service, tag string) domain.Device {
	t.Helper()
	item, err := service.RegisterDevice(context.Background(), contracts.RegisterDeviceRequest{
		AssetTag: tag,
		Model:    "Bench Meter",
		Room:     "Room A",
		Steward:  "Lin",
	})
	if err != nil {
		t.Fatalf("register device %s: %v", tag, err)
	}
	return item
}

func TestUnitRegistryGovernsDimensions(t *testing.T) {
	service := NewService(repository.New())
	ctx := context.Background()

	if _, err := service.RegisterUnit(ctx, contracts.RegisterUnitRequest{
		Symbol: "mV", Dimension: "voltage", Factor: 0.001, Steward: "Lin",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("first unit of a dimension must be canonical: %v", err)
	}

	volt := mustRegisterUnit(t, service, "V", "voltage", 1, 0)
	if !volt.IsCanonical() {
		t.Fatal("first registered unit must be the canonical reference")
	}
	mustRegisterUnit(t, service, "mV", "voltage", 0.001, 0)

	if _, err := service.RegisterUnit(ctx, contracts.RegisterUnitRequest{
		Symbol: "mV", Dimension: "voltage", Factor: 0.001, Steward: "Lin",
	}); !errors.Is(err, domain.ErrDuplicateUnitSymbol) {
		t.Fatalf("duplicate symbol: %v", err)
	}
	if _, err := service.RegisterUnit(ctx, contracts.RegisterUnitRequest{
		Symbol: "volt", Dimension: "voltage", Factor: 1, Offset: 0, Steward: "Lin",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("second canonical unit: %v", err)
	}
	// Unit symbols are case-sensitive and must not be conflated.
	if _, err := service.RegisterUnit(ctx, contracts.RegisterUnitRequest{
		Symbol: "MV", Dimension: "voltage", Factor: 1e6, Offset: 0, Steward: "Lin",
	}); err != nil {
		t.Fatalf("MV is a distinct unit from mV: %v", err)
	}

	units, err := service.Units(ctx)
	if err != nil {
		t.Fatalf("list units: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("expected 3 units, got %d", len(units))
	}
	for i := 1; i < len(units); i++ {
		if units[i-1].ID >= units[i].ID {
			t.Fatal("unit listing must be ordered by id")
		}
	}
}

func TestCaseAndMeasurementQuantitiesSurviveUnitRevision(t *testing.T) {
	service := NewService(repository.New())
	ctx := context.Background()

	volt := mustRegisterUnit(t, service, "V", "voltage", 1, 0)
	millivolt := mustRegisterUnit(t, service, "mV", "voltage", 0.001, 0)
	mustRegisterUnit(t, service, "K", "temperature", 1, 0)

	device := mustDevice(t, service, "MET-UNIT-1")
	opened, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID:    device.ID,
		Nominal:     25000,
		NominalUnit: "mV",
		Tolerance:   500,
		Steward:     "Qiao",
	})
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	if !closeTo(opened.Case.Nominal, 25) || !closeTo(opened.Case.Tolerance, 0.5) {
		t.Fatalf("case target = %v ± %v", opened.Case.Nominal, opened.Case.Tolerance)
	}
	if opened.Case.SubmittedNominal != 25000 || opened.Case.NominalUnit != "mV" || opened.Case.ToleranceUnit != "mV" {
		t.Fatalf("case submitted quantities = %+v", opened.Case)
	}

	// Unknown, incompatible, and missing units are never silently accepted.
	if _, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25, NominalUnit: "furlong", Tolerance: 0.5, Steward: "Qiao",
	}); !errors.Is(err, domain.ErrActiveCaseExists) {
		t.Fatalf("unfinished case still blocks duplicates: %v", err)
	}
	if _, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25, Unit: "parsec", Technician: "Rui",
	}); !errors.Is(err, domain.ErrUnknownUnit) {
		t.Fatalf("unknown unit: %v", err)
	}
	if _, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 293.15, Unit: "K", Technician: "Rui",
	}); !errors.Is(err, domain.ErrIncompatibleUnit) {
		t.Fatalf("incompatible unit: %v", err)
	}
	if _, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.2, Technician: "Rui",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("missing unit: %v", err)
	}

	measured, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25200, Unit: "mV", Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("record measurement: %v", err)
	}
	recorded := measured.Measurements[0]
	if !closeTo(recorded.Observed, 25.2) || recorded.Submitted != 25200 || recorded.Unit != "mV" {
		t.Fatalf("measurement quantities = %+v", recorded)
	}
	if recorded.UnitVersion != 1 || recorded.ConversionFactor != 0.001 {
		t.Fatalf("measurement conversion basis = version %d factor %v", recorded.UnitVersion, recorded.ConversionFactor)
	}

	// Revising the conversion basis must not rewrite confirmed quantities.
	if _, err := service.ReviseUnit(ctx, volt.ID, contracts.ReviseUnitRequest{Factor: 2, Actor: "Lin"}); !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("canonical unit revision: %v", err)
	}
	revised, err := service.ReviseUnit(ctx, millivolt.ID, contracts.ReviseUnitRequest{Factor: 0.002, Actor: "Lin"})
	if err != nil {
		t.Fatalf("revise unit: %v", err)
	}
	if revised.Version != 2 {
		t.Fatalf("revised unit version = %d", revised.Version)
	}

	reread, err := service.Case(ctx, opened.Case.ID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	if !closeTo(reread.Case.Nominal, 25) {
		t.Fatalf("case target moved after revision: %v", reread.Case.Nominal)
	}
	confirmed := reread.Measurements[0]
	if !closeTo(confirmed.Observed, 25.2) || confirmed.ConversionFactor != 0.001 || confirmed.UnitVersion != 1 {
		t.Fatalf("confirmed measurement was rewritten: %+v", confirmed)
	}

	// New submissions after the revision use the revised basis.
	second := mustDevice(t, service, "MET-UNIT-2")
	secondOpened, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: second.ID, Nominal: 25, NominalUnit: "V", Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("open second case: %v", err)
	}
	rechecked, err := service.RecordMeasurement(ctx, secondOpened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 13000, Unit: "mV", Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("record post-revision measurement: %v", err)
	}
	latest := rechecked.Measurements[0]
	if !closeTo(latest.Observed, 26) || latest.Accepted || rechecked.Case.State != domain.CaseRecheckRequired {
		t.Fatalf("post-revision measurement = %+v", latest)
	}
	if latest.UnitVersion != 2 {
		t.Fatalf("post-revision measurement unit version = %d", latest.UnitVersion)
	}

	// Sealing keeps the physical fact confirmed at submission time.
	sealed, err := service.SealCase(ctx, opened.Case.ID, contracts.SealCaseRequest{
		Reviewer: "Mira", Summary: "accepted measurement sealed for review",
	})
	if err != nil {
		t.Fatalf("seal case: %v", err)
	}
	if sealed.Seal == nil || sealed.Seal.MeasurementID != recorded.ID {
		t.Fatalf("seal = %+v", sealed.Seal)
	}
	if !closeTo(sealed.Measurements[0].Observed, 25.2) {
		t.Fatalf("sealed measurement = %+v", sealed.Measurements[0])
	}
}

func TestOffsetUnitsConvertPointsButNotTolerances(t *testing.T) {
	service := NewService(repository.New())
	ctx := context.Background()

	mustRegisterUnit(t, service, "K", "temperature", 1, 0)
	mustRegisterUnit(t, service, "°C", "temperature", 1, 273.15)

	device := mustDevice(t, service, "MET-TEMP-1")
	opened, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID:      device.ID,
		Nominal:       20,
		NominalUnit:   "°C",
		Tolerance:     1,
		ToleranceUnit: "K",
		Steward:       "Qiao",
	})
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	if !closeTo(opened.Case.Nominal, 293.15) {
		t.Fatalf("20 °C nominal in canonical units = %v", opened.Case.Nominal)
	}
	// A tolerance is an interval: the Celsius offset must never apply to it.
	if !closeTo(opened.Case.Tolerance, 1) {
		t.Fatalf("1 K tolerance in canonical units = %v", opened.Case.Tolerance)
	}

	measured, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 20.4, Unit: "°C", Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("record measurement: %v", err)
	}
	reading := measured.Measurements[0]
	if !closeTo(reading.Observed, 293.55) || !closeTo(reading.Deviation, 0.4) || !reading.Accepted {
		t.Fatalf("celsius reading = %+v", reading)
	}
	if !closeTo(reading.ConversionOffset, 273.15) {
		t.Fatalf("recorded conversion offset = %v", reading.ConversionOffset)
	}
}
