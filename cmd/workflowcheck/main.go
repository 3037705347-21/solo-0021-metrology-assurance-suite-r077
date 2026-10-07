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

	if err := h.mustRegisterUnit("V", "voltage", 1, 0, "SI derived unit"); err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID, "V", 25.0, 0.5)
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
	unit, err := nestedString(opened.Body, "case", "unit")
	if err != nil {
		return err
	}
	if unit != "V" {
		return fmt.Errorf("opened case unit = %q", unit)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"unit":      "V",
		"steward":   "Qiao",
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

	if err := h.mustRegisterUnit("V", "voltage", 1, 0, "SI derived unit"); err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID, "V", 25.0, 0.5)
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
	rejectCase, err := h.openCase(rejectDeviceID, "V", 25.0, 0.5)
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

	// The steward maintains the available dimensions and conversion bases.
	if err := h.mustRegisterUnit("V", "voltage", 1, 0, "SI derived unit"); err != nil {
		return err
	}
	if err := h.mustRegisterUnit("mV", "voltage", 0.001, 0, "SI milli prefix"); err != nil {
		return err
	}
	if err := h.mustRegisterUnit("K", "temperature", 1, 0, "SI base unit"); err != nil {
		return err
	}
	if err := h.mustRegisterUnit("°C", "temperature", 1, 273.15, "Celsius offset from kelvin"); err != nil {
		return err
	}
	duplicate, err := h.registerUnit("V", "voltage", 1, 0, "duplicate definition")
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "duplicate_unit"); err != nil {
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
		return fmt.Errorf("registered units = %d, expected 4", len(units))
	}

	// A case fixes its target quantity: nominal and tolerance in one unit.
	deviceA, err := h.createDevice("MET-1501")
	if err != nil {
		return err
	}
	openedA, err := h.openCase(deviceA, "V", 0.25, 0.01)
	if err != nil {
		return err
	}
	caseA, err := nestedString(openedA.Body, "case", "id")
	if err != nil {
		return err
	}
	dimension, err := nestedString(openedA.Body, "case", "dimension")
	if err != nil {
		return err
	}
	if dimension != "voltage" {
		return fmt.Errorf("case dimension = %q", dimension)
	}

	// A measurement submitted in mV is adopted in the case unit V.
	recorded, err := h.recordMeasurement(caseA, 249.5, "mV", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(recorded, http.StatusCreated); err != nil {
		return err
	}
	if err := expectMeasurement(recorded.Body, 249.5, "mV", 0.2495, "V", -0.0005, true); err != nil {
		return err
	}

	// Sealing snapshots the adopted quantity into the evidence record.
	sealed, err := h.request(http.MethodPost, "/cases/"+caseA+"/seal", map[string]any{
		"reviewer": "Wei",
		"summary":  "accepted measurement sealed for review",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	if err := expectSealQuantity(sealed.Body, 0.2495, "V", -0.0005); err != nil {
		return err
	}

	// The steward revises the mV conversion rule afterwards.
	revised, err := h.request(http.MethodPut, "/units/mV", map[string]any{
		"scale":      0.0011,
		"offset":     0,
		"basis":      "corrected calibration factor",
		"maintainer": "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revised, http.StatusOK); err != nil {
		return err
	}
	revision, err := nestedFloat(revised.Body, "unit", "revision")
	if err != nil {
		return err
	}
	if revision != 2 {
		return fmt.Errorf("mV revision = %v, expected 2", revision)
	}

	// The revision must not rewrite the confirmed measurement or the seal.
	reread, err := h.request(http.MethodGet, "/cases/"+caseA, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(reread, http.StatusOK); err != nil {
		return err
	}
	if err := expectMeasurement(reread.Body, 249.5, "mV", 0.2495, "V", -0.0005, true); err != nil {
		return fmt.Errorf("revision rewrote history: %w", err)
	}
	if err := expectSealQuantity(reread.Body, 0.2495, "V", -0.0005); err != nil {
		return fmt.Errorf("revision rewrote the seal: %w", err)
	}

	// Unknown and incompatible units are rejected explicitly, never silently.
	deviceB, err := h.createDevice("MET-1502")
	if err != nil {
		return err
	}
	openedB, err := h.openCase(deviceB, "V", 0.25, 0.01)
	if err != nil {
		return err
	}
	caseB, err := nestedString(openedB.Body, "case", "id")
	if err != nil {
		return err
	}
	unknown, err := h.recordMeasurement(caseB, 1.0, "furlong", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(unknown, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(unknown, "unknown_unit"); err != nil {
		return err
	}
	incompatible, err := h.recordMeasurement(caseB, 250.0, "K", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(incompatible, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(incompatible, "incompatible_unit"); err != nil {
		return err
	}
	missing, err := h.request(http.MethodPost, "/cases/"+caseB+"/measurements", map[string]any{
		"observed":   250.0,
		"technician": "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missing, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(missing, "invalid_input"); err != nil {
		return err
	}
	deviceD, err := h.createDevice("MET-1504")
	if err != nil {
		return err
	}
	unknownCaseUnit, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceD,
		"nominal":   1.0,
		"tolerance": 0.1,
		"unit":      "furlong",
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(unknownCaseUnit, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(unknownCaseUnit, "unknown_unit"); err != nil {
		return err
	}

	// New measurements use the revised rule: 253 mV is now 0.2783 V.
	revisedRule, err := h.recordMeasurement(caseB, 253.0, "mV", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(revisedRule, http.StatusCreated); err != nil {
		return err
	}
	if err := expectMeasurement(revisedRule.Body, 253.0, "mV", 0.2783, "V", 0.0283, false); err != nil {
		return err
	}

	// Affine conversion: 298 K is adopted as 24.85 °C on a Celsius case.
	deviceC, err := h.createDevice("MET-1503")
	if err != nil {
		return err
	}
	openedC, err := h.openCase(deviceC, "°C", 25.0, 0.5)
	if err != nil {
		return err
	}
	caseC, err := nestedString(openedC.Body, "case", "id")
	if err != nil {
		return err
	}
	celsius, err := h.recordMeasurement(caseC, 298.0, "K", "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(celsius, http.StatusCreated); err != nil {
		return err
	}
	if err := expectMeasurement(celsius.Body, 298.0, "K", 24.85, "°C", -0.15, true); err != nil {
		return err
	}

	// The audit trail restores the submitted and adopted quantities.
	events, err := h.request(http.MethodGet, "/cases/"+caseA+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !eventDetailContains(events.Body, "measurement_accepted", "249.5 mV") {
		return errors.New("measurement event does not restore the submitted quantity")
	}
	if !eventDetailContains(events.Body, "measurement_accepted", "0.2495 V") {
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

func (h *harness) registerUnit(symbol, dimension string, scale, offset float64, basis string) (apiResponse, error) {
	return h.request(http.MethodPost, "/units", map[string]any{
		"symbol":     symbol,
		"dimension":  dimension,
		"scale":      scale,
		"offset":     offset,
		"basis":      basis,
		"maintainer": "Lin",
	})
}

func (h *harness) mustRegisterUnit(symbol, dimension string, scale, offset float64, basis string) error {
	response, err := h.registerUnit(symbol, dimension, scale, offset, basis)
	if err != nil {
		return err
	}
	return expectStatus(response, http.StatusCreated)
}

func (h *harness) openCase(deviceID, unit string, nominal, tolerance float64) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   nominal,
		"tolerance": tolerance,
		"unit":      unit,
		"steward":   "Qiao",
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

func (h *harness) rejectedCase(tag string) (string, error) {
	if err := h.mustRegisterUnit("V", "voltage", 1, 0, "SI derived unit"); err != nil {
		return "", err
	}
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID, "V", 25.0, 0.5)
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
	if err := h.mustRegisterUnit("V", "voltage", 1, 0, "SI derived unit"); err != nil {
		return "", err
	}
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID, "V", 25.0, 0.5)
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

func expectNear(actual, expected float64, label string) error {
	const tolerance = 1e-9
	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		return fmt.Errorf("%s = %v, expected %v", label, actual, expected)
	}
	return nil
}

func expectMeasurement(body map[string]any, observed float64, observedUnit string, adopted float64, unit string, deviation float64, accepted bool) error {
	value, err := nestedFloat(body, "measurements", 0, "observed")
	if err != nil {
		return err
	}
	if err := expectNear(value, observed, "observed"); err != nil {
		return err
	}
	symbol, err := measurementString(body, "observed_unit")
	if err != nil {
		return err
	}
	if symbol != observedUnit {
		return fmt.Errorf("observed_unit = %q, expected %q", symbol, observedUnit)
	}
	value, err = nestedFloat(body, "measurements", 0, "adopted")
	if err != nil {
		return err
	}
	if err := expectNear(value, adopted, "adopted"); err != nil {
		return err
	}
	caseUnit, err := measurementString(body, "unit")
	if err != nil {
		return err
	}
	if caseUnit != unit {
		return fmt.Errorf("unit = %q, expected %q", caseUnit, unit)
	}
	value, err = nestedFloat(body, "measurements", 0, "deviation")
	if err != nil {
		return err
	}
	if err := expectNear(value, deviation, "deviation"); err != nil {
		return err
	}
	flag, err := nestedBool(body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if flag != accepted {
		return fmt.Errorf("accepted = %v, expected %v", flag, accepted)
	}
	return nil
}

func measurementString(body map[string]any, key string) (string, error) {
	items, err := nestedSlice(body, "measurements")
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", errors.New("measurements is empty")
	}
	entry, ok := items[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("measurements[0] is not an object")
	}
	value, ok := entry[key].(string)
	if !ok {
		return "", fmt.Errorf("measurements[0].%s is not a string", key)
	}
	return value, nil
}

func expectSealQuantity(body map[string]any, adopted float64, unit string, deviation float64) error {
	value, err := nestedFloat(body, "seal", "adopted_value")
	if err != nil {
		return err
	}
	if err := expectNear(value, adopted, "seal adopted_value"); err != nil {
		return err
	}
	symbol, err := nestedString(body, "seal", "unit")
	if err != nil {
		return err
	}
	if symbol != unit {
		return fmt.Errorf("seal unit = %q, expected %q", symbol, unit)
	}
	value, err = nestedFloat(body, "seal", "deviation")
	if err != nil {
		return err
	}
	return expectNear(value, deviation, "seal deviation")
}

func eventDetailContains(root map[string]any, kind string, fragment string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] != kind {
			continue
		}
		detail, ok := event["detail"].(string)
		if ok && strings.Contains(detail, fragment) {
			return true
		}
	}
	return false
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
