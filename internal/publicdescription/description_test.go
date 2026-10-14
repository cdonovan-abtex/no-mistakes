package publicdescription

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/testgit"
)

func TestReadPinsCommittedPublicMaterial(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent"))
	dir := t.TempDir()
	realGit, err := testgit.RealGit()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.invalid")
	file := filepath.Join(dir, "public.json")
	if err := os.WriteFile(file, []byte(`{"title":"fix: public title","body":"PUBLIC_SENTINEL"}`), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "fixture")
	head := run("rev-parse", "HEAD")
	pin, err := Read(context.Background(), dir, head, "public.json")
	if err != nil {
		t.Fatal(err)
	}
	if pin.Body != "PUBLIC_SENTINEL" || pin.HeadSHA != head || len(pin.SHA256) != 64 {
		t.Fatalf("invalid pin: %+v", pin)
	}
	if err := os.WriteFile(file, []byte(`{"title":"private","body":"PRIVATE_SENTINEL"}`), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := Read(context.Background(), dir, head, "public.json")
	if err != nil || *again != *pin {
		t.Fatalf("mutable file replaced committed source: %+v %v", again, err)
	}
	if _, err := Read(context.Background(), dir, head, "missing.json"); err == nil {
		t.Fatal("missing committed source accepted")
	}
	if pin.MatchesRequest("another.json") {
		t.Fatal("reattach replaces the pin")
	}
}

func TestReadRejectsUnsafePublicMaterial(t *testing.T) {
	for _, p := range []string{"", "/tmp/public.json", "../public.json", "a/../public.json", "a\\public.json", "HEAD:public.json", "a\npublic.json"} {
		if err := ValidatePath(p); err == nil {
			t.Fatalf("unsafe path %q accepted", p)
		}
	}
}
