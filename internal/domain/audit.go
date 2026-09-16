package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// AuditLogEntry is an append-only audit log entry. Once constructed it has no
// mutators, mirroring the ledger's immutability discipline.
type AuditLogEntry struct {
	id             uuid.UUID
	organizationID uuid.UUID
	propertyID     *uuid.UUID
	actorID        *uuid.UUID
	action         string
	entityType     string
	entityID       *uuid.UUID
	metadata       json.RawMessage
	createdAt      time.Time
}

// NewAuditLogEntryInput carries everything needed to construct an AuditLogEntry.
type NewAuditLogEntryInput struct {
	OrganizationID uuid.UUID
	PropertyID     *uuid.UUID
	ActorID        *uuid.UUID
	Action         string
	EntityType     string
	EntityID       *uuid.UUID
	Metadata       json.RawMessage
}

// NewAuditLogEntry validates and returns an immutable AuditLogEntry.
func NewAuditLogEntry(input NewAuditLogEntryInput) (*AuditLogEntry, error) {
	if input.Action == "" {
		return nil, errors.New("audit action is required")
	}
	if input.EntityType == "" {
		return nil, errors.New("audit entity type is required")
	}
	return &AuditLogEntry{
		organizationID: input.OrganizationID,
		propertyID:     input.PropertyID,
		actorID:        input.ActorID,
		action:         input.Action,
		entityType:     input.EntityType,
		entityID:       input.EntityID,
		metadata:       input.Metadata,
	}, nil
}

func (e *AuditLogEntry) ID() uuid.UUID             { return e.id }
func (e *AuditLogEntry) OrganizationID() uuid.UUID { return e.organizationID }
func (e *AuditLogEntry) PropertyID() *uuid.UUID    { return e.propertyID }
func (e *AuditLogEntry) ActorID() *uuid.UUID       { return e.actorID }
func (e *AuditLogEntry) Action() string            { return e.action }
func (e *AuditLogEntry) EntityType() string        { return e.entityType }
func (e *AuditLogEntry) EntityID() *uuid.UUID      { return e.entityID }
func (e *AuditLogEntry) Metadata() json.RawMessage { return e.metadata }
func (e *AuditLogEntry) CreatedAt() time.Time      { return e.createdAt }
