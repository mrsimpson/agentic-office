package coreapi

import "github.com/google/uuid"

// NewCommandID returns a new ID that is unique and sortable by time (UUIDv7).
func NewCommandID() string { return uuid.Must(uuid.NewV7()).String() }
