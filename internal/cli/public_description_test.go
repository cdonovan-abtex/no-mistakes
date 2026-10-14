package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAxiPublicDescriptionRefusesOldDaemonBeforeLaunch(t *testing.T) {
	launched := olderDaemonFixture(t, nil)
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.Flags().String("public-description", "", "")
	if err := cmd.Flags().Set("public-description", "public.json"); err != nil {
		t.Fatal(err)
	}
	err := runAxiRunWithLaunchProof(cmd, false, nil, "complete private intent", "", false, "", "", defaultAxiWait)
	if err == nil || !strings.Contains(out.String(), "does not support --public-description") {
		t.Fatalf("old daemon not refused: %v %s", err, out.String())
	}
	if len(*launched) != 0 {
		t.Fatal("launch occurred before capability refusal")
	}
}

func TestPublicDescriptionPushOptions(t *testing.T) {
	for _, options := range [][]string{{publicDescriptionPushOptionPrefix + "../private.json"}, {publicDescriptionPushOptionPrefix + "a.json", publicDescriptionPushOptionPrefix + "b.json"}, {publicDescriptionPushOptionPrefix}} {
		if _, err := parsePublicDescriptionPushOptions(options); err == nil {
			t.Fatalf("unsafe options accepted: %v", options)
		}
	}
	p, err := parsePublicDescriptionPushOptions([]string{"no-mistakes.omit-intent", publicDescriptionPushOptionPrefix + "public description.json"})
	if err != nil || p != "public description.json" {
		t.Fatalf("supported option not preserved: %q %v", p, err)
	}
}
