package db

import (
	"fmt"

	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
)

// PinRunPublicDescription is the null-to-pin compare-and-set used under the
// executor's parked-gate mutex. It never changes intent, head, status or custody.
// The schema trigger refuses every subsequent replacement or removal.
func (d *DB) PinRunPublicDescription(id string, pin *publicdescription.Snapshot) error {
	if pin == nil || pin.RawJSON == "" {
		return fmt.Errorf("captured public description is required")
	}
	result, err := d.sql.Exec(`UPDATE runs SET public_description = ? WHERE id = ? AND public_description IS NULL`, pin, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("public description already pinned or run missing")
	}
	return nil
}
