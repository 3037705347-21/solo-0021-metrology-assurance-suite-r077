# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices, maintains the unit registry, and
  opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- Unit: a steward-maintained metrological unit with a case-sensitive symbol, a
  dimension, and an affine conversion basis (`canonical = value*factor +
  offset`) to the canonical unit of its dimension. The first unit registered
  for a dimension is its canonical reference (factor 1, offset 0) and can
  never be revised.
- VerificationCase: one bounded assurance activity for a device. The case
  fixes its target quantity at open time: it keeps the submitted nominal and
  tolerance with their units, the dimension, and the adopted canonical
  values, none of which are ever recomputed.
- Measurement: an observed quantity. It keeps the submitted value and unit,
  the adopted canonical value, the conversion basis (factor, offset, unit
  version) used at submission, the computed canonical deviation, the
  acceptance decision, technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement.
- AuditEvent: an append-only trace of meaningful case, device, and unit
  changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Register Unit

The steward sends `POST /units` with a symbol, dimension, factor, and offset.
The service rejects duplicate symbols, requires the first unit of a dimension
to be its canonical reference (factor 1, offset 0), and rejects a second
canonical reference for the same dimension. `GET /units` lists the registry.

### 3. Revise Unit

The steward sends `POST /units/{id}/revise` with a new factor and offset. Only
non-canonical units can be revised; the revision bumps the unit version and is
recorded in the audit trail. Confirmed measurements and seals keep the
conversion basis recorded at submission, so a revision never rewrites
recorded evidence.

### 4. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, nominal unit,
tolerance, optional tolerance unit (defaulting to the nominal unit), and
owner. The service verifies that the device exists, is active, and has no
unfinished case. Both units must be registered and share one dimension;
unknown or incompatible units are rejected. A valid request converts the
nominal and tolerance into the canonical unit of the dimension once, fixes
them as the case target, and creates a case in `awaiting_measurement`.

### 5. Record Measurement

A technician sends `POST /cases/{id}/measurements` with an observed value, its
unit, and a technician. The case must be `awaiting_measurement`. The unit must
be registered for the dimension of the case. The service adopts the submitted
quantity into the canonical unit, snapshots the conversion basis, and computes
the signed canonical deviation from the fixed case target. An in-tolerance
measurement moves the case to `review_ready`; an out-of-tolerance measurement
moves it to `recheck_required`.

### 6. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 7. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute canonical deviation recorded at
submission, creates exactly one evidence seal, moves the case to `sealed`,
and rejects later mutations. Sealing never reconverts quantities.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- Unit symbols are unique and case-sensitive; each dimension has exactly one
  canonical reference unit (factor 1, offset 0), registered first, and a
  canonical unit can never be revised.
- A device has at most one unfinished verification case.
- A case fixes its dimension and canonical target at open time; later unit
  revisions never move an opened case.
- Measurements are accepted only while a case is awaiting measurement.
- A measurement records the submitted value and unit plus the adopted
  canonical value and the conversion basis used at submission; confirmed
  measurements are never recomputed after unit revisions.
- Tolerances and deviations are intervals: unit offsets never apply to them.
- A tolerance must be positive.
- Unknown, missing, or dimension-incompatible units are rejected, never
  silently accepted.
- A case can have at most one evidence seal.
- A sealed case cannot accept measurements, reopen, or seal again.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

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

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The measurement-units check registers canonical and affine units,
verifies that unknown and incompatible units are rejected, confirms that a
unit revision never rewrites confirmed measurements or seals, and proves that
offset units convert point quantities but never tolerances.

Domain and service tests under `internal/domain` and `internal/assurance`
cover the conversion math, the registry rules, and the immutability of
confirmed quantities across unit revisions.
