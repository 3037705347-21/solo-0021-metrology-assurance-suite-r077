package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
		"measurement-units":        checkMeasurementUnits,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	if err := h.setupUnits(); err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":    deviceID,
		"nominal":      25.0,
		"nominal_unit": "V",
		"tolerance":    0.5,
		"steward":      "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	if err := h.setupUnits(); err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "V", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "V", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "V", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	if err := h.setupUnits(); err != nil {
		return err
	}
	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	if err := h.setupUnits(); err != nil {
		return err
	}
	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func checkMeasurementUnits() error {
	h := newHarness()
	defer h.close()

	// The first unit of a dimension must be its canonical reference.
	nonCanonical, err := h.registerUnit("mV", "voltage", 0.001, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(nonCanonical, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(nonCanonical, "invalid_input"); err != nil {
		return err
	}

	volt, err := h.registerUnit("V", "voltage", 1, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(volt, http.StatusCreated); err != nil {
		return err
	}
	voltID, err := nestedString(volt.Body, "unit", "id")
	if err != nil {
		return err
	}
	millivolt, err := h.registerUnit("mV", "voltage", 0.001, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(millivolt, http.StatusCreated); err != nil {
		return err
	}
	millivoltID, err := nestedString(millivolt.Body, "unit", "id")
	if err != nil {
		return err
	}

	duplicate, err := h.registerUnit("mV", "voltage", 0.001, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "duplicate_unit_symbol"); err != nil {
		return err
	}

	secondCanonical, err := h.registerUnit("volt", "voltage", 1, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(secondCanonical, http.StatusBadRequest); err != nil {
		return err
	}

	if _, err := h.registerUnit("K", "temperature", 1, 0, "Lin"); err != nil {
		return err
	}
	celsius, err := h.registerUnit("°C", "temperature", 1, 273.15, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(celsius, http.StatusCreated); err != nil {
		return err
	}

	listed, err := h.request(http.MethodGet, "/units", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(listed, http.StatusOK); err != nil {
		return err
	}
	units, err := nestedSlice(listed.Body, "units")
	if err != nil {
		return err
	}
	if len(units) != 4 {
		return fmt.Errorf("expected 4 registered units, got %d", len(units))
	}

	// Unknown units are rejected when a case is opened.
	deviceID, err := h.createDevice("MET-2001")
	if err != nil {
		return err
	}
	unknownUnit, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":    deviceID,
		"nominal":      25.0,
		"nominal_unit": "furlong",
		"tolerance":    0.5,
		"steward":      "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(unknownUnit, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(unknownUnit, "unknown_unit"); err != nil {
		return err
	}

	// The case fixes its target quantity in canonical terms at open time;
	// the omitted tolerance unit defaults to the nominal unit.
	opened, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":    deviceID,
		"nominal":      25000,
		"nominal_unit": "mV",
		"tolerance":    500,
		"steward":      "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(opened, http.StatusCreated); err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	if err := expectFloatClose(opened.Body, 25, 1e-9, "case", "nominal"); err != nil {
		return err
	}
	if err := expectFloatClose(opened.Body, 0.5, 1e-9, "case", "tolerance"); err != nil {
		return err
	}
	if err := expectFloatClose(opened.Body, 25000, 1e-9, "case", "submitted_nominal"); err != nil {
		return err
	}
	for path, want := range map[string]string{
		"case.nominal_unit":   "mV",
		"case.tolerance_unit": "mV",
		"case.canonical_unit": "V",
		"case.dimension":      "voltage",
	} {
		parts := strings.Split(path, ".")
		got, err := nestedString(opened.Body, parts...)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s = %q, expected %q", path, got, want)
		}
	}

	// Incompatible, unknown, and missing units are never silently accepted.
	incompatible, err := h.recordMeasurement(caseID, 293.15, "K", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(incompatible, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(incompatible, "incompatible_unit"); err != nil {
		return err
	}
	unknownMeasure, err := h.recordMeasurement(caseID, 25, "parsec", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(unknownMeasure, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(unknownMeasure, "unknown_unit"); err != nil {
		return err
	}
	missingUnit, err := h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   25.2,
		"technician": "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missingUnit, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(missingUnit, "invalid_input"); err != nil {
		return err
	}

	// A millivolt submission is adopted into the canonical volt basis.
	measured, err := h.recordMeasurement(caseID, 25200, "mV", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(measured, http.StatusCreated); err != nil {
		return err
	}
	if err := expectFloatClose(measured.Body, 25.2, 1e-9, "measurements", 0, "observed"); err != nil {
		return err
	}
	if err := expectFloatClose(measured.Body, 25200, 1e-9, "measurements", 0, "submitted"); err != nil {
		return err
	}
	if err := expectFloatClose(measured.Body, 0.001, 1e-12, "measurements", 0, "conversion_factor"); err != nil {
		return err
	}
	measurementID, err := nestedStringAt(measured.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}
	unitSymbol, err := nestedStringAt(measured.Body, "measurements", 0, "unit")
	if err != nil {
		return err
	}
	if unitSymbol != "mV" {
		return fmt.Errorf("measurement unit = %q", unitSymbol)
	}

	// Revising a conversion basis must not rewrite confirmed quantities.
	revised, err := h.request(http.MethodPost, "/units/"+millivoltID+"/revise", map[string]any{
		"factor": 0.002,
		"offset": 0,
		"actor":  "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revised, http.StatusOK); err != nil {
		return err
	}
	reviseCanonical, err := h.request(http.MethodPost, "/units/"+voltID+"/revise", map[string]any{
		"factor": 2,
		"offset": 0,
		"actor":  "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reviseCanonical, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(reviseCanonical, "state_conflict"); err != nil {
		return err
	}

	reread, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(reread, http.StatusOK); err != nil {
		return err
	}
	if err := expectFloatClose(reread.Body, 25, 1e-9, "case", "nominal"); err != nil {
		return err
	}
	if err := expectFloatClose(reread.Body, 25.2, 1e-9, "measurements", 0, "observed"); err != nil {
		return err
	}
	if err := expectFloatClose(reread.Body, 0.001, 1e-12, "measurements", 0, "conversion_factor"); err != nil {
		return err
	}
	unitVersion, err := nestedFloat(reread.Body, "measurements", 0, "unit_version")
	if err != nil {
		return err
	}
	if unitVersion != 1 {
		return fmt.Errorf("confirmed measurement unit_version = %v, expected 1", unitVersion)
	}

	// New submissions after the revision use the revised basis.
	secondDeviceID, err := h.createDevice("MET-2002")
	if err != nil {
		return err
	}
	secondOpened, err := h.openCase(secondDeviceID)
	if err != nil {
		return err
	}
	secondCaseID, err := nestedString(secondOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	rechecked, err := h.recordMeasurement(secondCaseID, 13000, "mV", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rechecked, http.StatusCreated); err != nil {
		return err
	}
	if err := expectFloatClose(rechecked.Body, 26, 1e-9, "measurements", 0, "observed"); err != nil {
		return err
	}
	recheckedState, err := nestedString(rechecked.Body, "case", "state")
	if err != nil {
		return err
	}
	if recheckedState != "recheck_required" {
		return fmt.Errorf("revised-basis measurement state = %q", recheckedState)
	}
	recheckedVersion, err := nestedFloat(rechecked.Body, "measurements", 0, "unit_version")
	if err != nil {
		return err
	}
	if recheckedVersion != 2 {
		return fmt.Errorf("post-revision measurement unit_version = %v, expected 2", recheckedVersion)
	}

	// Offset units convert point quantities but never tolerances.
	tempDeviceID, err := h.createDevice("MET-2003")
	if err != nil {
		return err
	}
	tempOpened, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":      tempDeviceID,
		"nominal":        20,
		"nominal_unit":   "°C",
		"tolerance":      1,
		"tolerance_unit": "K",
		"steward":        "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(tempOpened, http.StatusCreated); err != nil {
		return err
	}
	if err := expectFloatClose(tempOpened.Body, 293.15, 1e-9, "case", "nominal"); err != nil {
		return err
	}
	if err := expectFloatClose(tempOpened.Body, 1, 1e-9, "case", "tolerance"); err != nil {
		return err
	}
	tempCaseID, err := nestedString(tempOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	tempMeasured, err := h.recordMeasurement(tempCaseID, 293.65, "K", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(tempMeasured, http.StatusCreated); err != nil {
		return err
	}
	if err := expectFloatClose(tempMeasured.Body, 0.5, 1e-9, "measurements", 0, "deviation"); err != nil {
		return err
	}

	celsiusDeviceID, err := h.createDevice("MET-2004")
	if err != nil {
		return err
	}
	celsiusOpened, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":    celsiusDeviceID,
		"nominal":      293.15,
		"nominal_unit": "K",
		"tolerance":    1,
		"steward":      "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(celsiusOpened, http.StatusCreated); err != nil {
		return err
	}
	celsiusCaseID, err := nestedString(celsiusOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	celsiusMeasured, err := h.recordMeasurement(celsiusCaseID, 20.4, "°C", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(celsiusMeasured, http.StatusCreated); err != nil {
		return err
	}
	if err := expectFloatClose(celsiusMeasured.Body, 293.55, 1e-9, "measurements", 0, "observed"); err != nil {
		return err
	}
	if err := expectFloatClose(celsiusMeasured.Body, 273.15, 1e-9, "measurements", 0, "conversion_offset"); err != nil {
		return err
	}

	// Sealing keeps the physical fact confirmed at submission time.
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Mira",
		"summary":  "accepted measurement sealed for review",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealedMeasurement, err := nestedString(sealed.Body, "seal", "measurement_id")
	if err != nil {
		return err
	}
	if sealedMeasurement != measurementID {
		return fmt.Errorf("seal selected measurement %q, expected %q", sealedMeasurement, measurementID)
	}
	if err := expectFloatClose(sealed.Body, 25.2, 1e-9, "measurements", 0, "observed"); err != nil {
		return err
	}

	// Events restore the submitted and adopted quantities.
	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !eventDetailContains(events.Body, "measurement_accepted", "25200 mV") {
		return errors.New("measurement event does not restore the submitted quantity")
	}
	if !eventDetailContains(events.Body, "measurement_accepted", "25.2 V") {
		return errors.New("measurement event does not restore the adopted quantity")
	}
	return nil
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":    deviceID,
		"nominal":      25.0,
		"nominal_unit": "V",
		"tolerance":    0.5,
		"steward":      "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, unit, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"unit":       unit,
		"technician": technician,
	})
}

func (h *harness) registerUnit(symbol, dimension string, factor, offset float64, steward string) (apiResponse, error) {
	return h.request(http.MethodPost, "/units", map[string]any{
		"symbol":    symbol,
		"dimension": dimension,
		"factor":    factor,
		"offset":    offset,
		"steward":   steward,
	})
}

func (h *harness) setupUnits() error {
	volt, err := h.registerUnit("V", "voltage", 1, 0, "Lin")
	if err != nil {
		return err
	}
	if err := expectStatus(volt, http.StatusCreated); err != nil {
		return err
	}
	millivolt, err := h.registerUnit("mV", "voltage", 0.001, 0, "Lin")
	if err != nil {
		return err
	}
	return expectStatus(millivolt, http.StatusCreated)
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "V", "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "V", "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedStringAt(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedFloat(root map[string]any, path ...any) (float64, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%v is not a number", path)
	}
	return number, nil
}

func expectFloatClose(root map[string]any, expected, epsilon float64, path ...any) error {
	value, err := nestedFloat(root, path...)
	if err != nil {
		return err
	}
	diff := value - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		return fmt.Errorf("%v = %v, expected %v", path, value, expected)
	}
	return nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func eventDetailContains(root map[string]any, kind, fragment string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		detail, _ := event["detail"].(string)
		if event["kind"] == kind && strings.Contains(detail, fragment) {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
