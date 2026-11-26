// goca:manual — вторичный порт BC#4 deletion guard.
package out

import "context"

// ActivePatientsReader — синхронный guard удаления (BC#4).
type ActivePatientsReader interface {
	ActivePatients(ctx context.Context, programID string, version int) (int, error)
}
