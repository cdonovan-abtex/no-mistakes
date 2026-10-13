package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
)

func TestPublicDescriptionAdoptionPinCannotBeReplacedOrRemoved(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo, err := d.InsertRepo(t.TempDir(), "https://github.com/test/repo", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.InsertRun(repo.ID, "feature", strings.Repeat("a", 40), strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	pin, err := publicdescription.Capture([]byte(`{"title":"public","body":"Independent public material"}`), run.HeadSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PinRunPublicDescription(run.ID, pin); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []any{nil, `{"title":"replacement"}`} {
		if _, err := d.sql.Exec(`UPDATE runs SET public_description = ? WHERE id = ?`, raw, run.ID); err == nil {
			t.Fatal("immutable publication pin changed")
		}
	}
	got, err := d.GetRun(run.ID)
	if err != nil || got.PublicDescription == nil || *got.PublicDescription != *pin {
		t.Fatalf("durable pin changed: %+v %v", got, err)
	}
}
