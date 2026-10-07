# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices, maintains a steward-owned
registry of metrological units, opens verification cases with a fixed target
quantity, records measurements in any registered unit of the same dimension,
handles failed tolerance decisions, and seals traceable evidence records.

## Run

```bash
go run ./cmd/metrology-service --addr 127.0.0.1:18121
```

The service listens on `127.0.0.1:18121` by default.

## Build And Verify

```bash
go build ./...
go test ./...
go run ./cmd/workflowcheck --workflow register-device
go run ./cmd/workflowcheck --workflow open-verification-case
go run ./cmd/workflowcheck --workflow record-measurement
go run ./cmd/workflowcheck --workflow reopen-verification-case
go run ./cmd/workflowcheck --workflow seal-verification
go run ./cmd/workflowcheck --workflow measurement-units
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not a unit test.

## Units And Quantities

Every quantity carries a verifiable unit. The steward maintains the registry:

```bash
# The first unit of a dimension is its canonical reference (factor 1, offset 0).
curl -X POST localhost:18121/units -d '{"symbol":"V","dimension":"voltage","factor":1,"offset":0,"steward":"Lin"}'
# Later units declare an affine basis to it: canonical = value*factor + offset.
curl -X POST localhost:18121/units -d '{"symbol":"mV","dimension":"voltage","factor":0.001,"offset":0,"steward":"Lin"}'
curl -X POST localhost:18121/units -d '{"symbol":"°C","dimension":"temperature","factor":1,"offset":273.15,"steward":"Lin"}'
```

Cases fix their target quantity at open time and measurements adopt the
submitted value into the canonical unit of the case dimension, keeping both
the submitted and the adopted quantities:

```bash
curl -X POST localhost:18121/cases -d '{"device_id":"device-0001","nominal":25000,"nominal_unit":"mV","tolerance":500,"steward":"Qiao"}'
curl -X POST localhost:18121/cases/case-0001/measurements -d '{"observed":25200,"unit":"mV","technician":"Rui"}'
```

Rules:

- Unit symbols are case-sensitive (`mV` and `MV` are different units).
- Unknown, missing, or dimension-incompatible units are rejected with
  `unknown_unit`, `invalid_input`, or `incompatible_unit` errors.
- Tolerances are intervals: offsets never apply to them (a 1 °C tolerance is
  a 1 K tolerance).
- `POST /units/{id}/revise` updates the conversion basis of a non-canonical
  unit. Confirmed measurements, cases, and seals keep the basis recorded at
  submission, so a revision never rewrites evidence; only new submissions
  use the revised basis.

## API

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /units`
- `GET /units`
- `POST /units/{id}/revise`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Layout

- `cmd/metrology-service`: executable HTTP server.
- `cmd/workflowcheck`: bounded workflow verification commands.
- `internal/httpapi`: route and JSON boundary.
- `internal/assurance`: workflow orchestration.
- `internal/domain`: state rules and invariants.
- `internal/repository`: copy-on-write in-memory persistence.
- `internal/contracts`: API request and response models.
- `internal/bootstrap`: concrete dependency wiring.

## Inputs And Outputs

Inputs are JSON HTTP requests. Outputs are JSON response envelopes with either a
resource view or a stable error object:

```json
{
  "error": {
    "code": "state_conflict",
    "message": "case is not awaiting measurement"
  }
}
```

The service has no environment variables and no external dependencies.
