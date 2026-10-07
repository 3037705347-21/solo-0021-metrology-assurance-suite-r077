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

- Laboratory steward: registers devices and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- UnitDefinition: a registered unit of measure with a symbol, the dimension it
  belongs to, the affine conversion rule to the dimension's canonical unit
  (`canonical = scale * value + offset`), the basis for the rule, a maintainer,
  and a revision counter.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, the unit and dimension that fix its target quantity,
  current state, version, and assigned steward.
- Measurement: an observed value with its submitted unit, the adopted value
  converted into the case unit, computed deviation, acceptance decision,
  technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and a snapshot of its adopted quantity.
- AuditEvent: an append-only trace of meaningful case and device changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Maintain Measurement Units

The steward sends `POST /units` with a symbol, dimension, scale, offset, basis,
and maintainer to register a unit of measure, and `PUT /units/{symbol}` to
revise its conversion rule and basis. Symbols are case-sensitive and unique;
the dimension is fixed at registration. Revising a rule bumps the revision and
applies only to quantities recorded afterwards — confirmed measurements and
sealed evidence keep the values they were recorded with.

### 3. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance,
unit, and owner. The service verifies that the device exists, is active, and
has no unfinished case, and that the unit is registered. A valid request
creates a case in `awaiting_measurement` whose target quantity is fixed as the
nominal value and tolerance in that unit and dimension.

### 4. Record Measurement

A technician sends `POST /cases/{id}/measurements` with an observed value, its
unit, and a technician. The case must be `awaiting_measurement`. An unknown
unit is rejected with `unknown_unit`; a unit from a different dimension than
the case is rejected with `incompatible_unit`. Otherwise the service converts
the observed value into the case unit, computes the signed deviation from the
nominal value, and stores both the submitted and the adopted quantity. An
in-tolerance measurement moves the case to `review_ready`; an out-of-tolerance
measurement moves it to `recheck_required`.

### 5. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 6. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation, creates exactly one evidence
seal that snapshots the adopted value, unit, and deviation, moves the case to
`sealed`, and rejects later mutations.

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
- Unit symbols are unique and case-sensitive; a unit's dimension never changes.
- A device has at most one unfinished verification case.
- A case fixes its target quantity: nominal value, tolerance, and unit.
- Measurements are accepted only while a case is awaiting measurement.
- A measurement without a registered unit, or with a unit from another
  dimension, is rejected explicitly and never silently compared as a bare
  number.
- A measurement stores the submitted and adopted quantities at record time;
  revising a conversion rule never rewrites recorded measurements or seals.
- A tolerance must be positive.
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
- `GET /units/{symbol}`
- `PUT /units/{symbol}`
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
seal. The measurement-units check registers voltage and temperature units,
records a measurement in a compatible unit, rejects unknown and incompatible
units, revises a conversion rule, and proves the revision neither rewrites the
confirmed measurement and seal nor the quantities restored by reads and audit
events.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
