// Package valobj stays develop Metadata and other val objs
package valobj

import (
	"fmt"
	"strconv"
	"time"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
)

// Metadata represents creation/update timestamps
type Metadata struct {
	createdAt int64 // Unix timestamp in milliseconds
	updatedAt int64 // Unix timestamp in milliseconds
}

func validateMetadata(createdAt, updatedAt int64) error {
	if createdAt < 0 {
		return corerr.ErrMetadataCreatedAtNegative
	}
	if updatedAt < 0 {
		return corerr.ErrMetadataUpdatedAtNegative
	}
	if updatedAt < createdAt {
		return corerr.ErrMetadataUpdatedBeforeCreated
	}
	return nil
}

// NewMetadata creates Metadata with explicit timestamps
func NewMetadata(createdAt, updatedAt int64) (Metadata, error) {
	if err := validateMetadata(createdAt, updatedAt); err != nil {
		return Metadata{}, err
	}
	return Metadata{createdAt: createdAt, updatedAt: updatedAt}, nil
}

// NewMetadataNow creates Metadata with current time for both timestamps
func NewMetadataNow() Metadata {
	now := time.Now().UnixMilli()
	return Metadata{createdAt: now, updatedAt: now}
}

// Touch returns new Metadata with updated updatedAt  immutable update
func (m Metadata) Touch() Metadata {
	return Metadata{
		createdAt: m.createdAt,
		updatedAt: time.Now().UnixMilli(),
	}
}

func (m Metadata) CreatedAt() int64 { return m.createdAt }
func (m Metadata) UpdatedAt() int64 { return m.updatedAt }

func (m Metadata) Equals(other Metadata) bool {
	return m.createdAt == other.createdAt && m.updatedAt == other.updatedAt
}

func (m Metadata) String() string {
	cA := strconv.FormatInt(m.CreatedAt(), 10)
	uA := strconv.FormatInt(m.UpdatedAt(), 10)
	return fmt.Sprintf("created_at=%s updated_at=%s", cA, uA)
}
