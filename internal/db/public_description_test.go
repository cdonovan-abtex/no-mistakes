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

func TestClaimLaunchReceiptConflictingPublicDescriptionPreservesFirstObserver(t *testing.T) {
	for _, tc := range []struct {
		name string
		pin  *publicdescription.Snapshot
		path string
	}{
		{name: "unpinned"},
		{name: "committed explicit", pin: &publicdescription.Snapshot{Path: "public.json"}, path: "public.json"},
		{name: "committed omitted", pin: &publicdescription.Snapshot{Path: "public.json"}},
		{name: "adopted omitted", pin: &publicdescription.Snapshot{RawJSON: `{"title":"public","body":"Independent public material"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			repo, err := d.InsertRepo(t.TempDir(), "https://github.com/test/repo", "main")
			if err != nil {
				t.Fatal(err)
			}
			run, err := d.InsertRunWithPublicDescription(repo.ID, "feature", "head", "base", nil, "nonce", "generation", "digest", "", false, nil, tc.pin)
			if err != nil {
				t.Fatal(err)
			}
			conflict, claimed, err := d.ClaimLaunchReceipt(repo.ID, "feature", "nonce", "head", "generation", "digest", "", false, "other.json")
			if err != nil || claimed || conflict == nil || conflict.ID != run.ID {
				t.Fatalf("conflicting claim = %+v, claimed=%v, err=%v", conflict, claimed, err)
			}
			stored, err := d.GetRun(run.ID)
			if err != nil || stored == nil || stored.LaunchReceiptClaimedAt != nil {
				t.Fatalf("conflict consumed first observer: %+v, err=%v", stored, err)
			}
			first, claimed, err := d.ClaimLaunchReceipt(repo.ID, "feature", "nonce", "head", "generation", "digest", "", false, tc.path)
			if err != nil || !claimed || first == nil || first.ID != run.ID || first.LaunchReceiptClaimedAt == nil {
				t.Fatalf("matching first claim = %+v, claimed=%v, err=%v", first, claimed, err)
			}
			replay, claimed, err := d.ClaimLaunchReceipt(repo.ID, "feature", "nonce", "head", "generation", "digest", "", false, tc.path)
			if err != nil || claimed || replay == nil || replay.ID != run.ID {
				t.Fatalf("matching replay = %+v, claimed=%v, err=%v", replay, claimed, err)
			}
		})
	}
}
