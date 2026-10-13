// Package publicdescription pins independently authored public PR material from
// a committed file. It never derives public prose from validation inputs.
package publicdescription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kunchenguid/no-mistakes/internal/git"
)

const MaxBytes = 48 * 1024

type requestKey struct{}
type capturedRequestKey struct{}

// WithCapturedRequest carries an inherited independent description on rerun;
// it cannot select or replace an active run's publication pin.
func WithCapturedRequest(ctx context.Context, raw string) context.Context {
	return context.WithValue(ctx, capturedRequestKey{}, raw)
}
func CapturedRequest(ctx context.Context) string {
	raw, _ := ctx.Value(capturedRequestKey{}).(string)
	return raw
}

// WithRequest carries launch-only selection through the shared fresh/proof/
// rerun dispatch helpers. Effective pipeline behavior reads the durable run pin,
// never this context value. It does not change any validation input.
func WithRequest(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, requestKey{}, p)
}

func RequestPath(ctx context.Context) string {
	p, _ := ctx.Value(requestKey{}).(string)
	return p
}

// Snapshot is immutable for a run, including across recovery and pipeline fixes.
// HeadSHA identifies the submitted source, not the head later certified by Test.
type Snapshot struct {
	Path    string `json:"path"`
	HeadSHA string `json:"head_sha"`
	SHA256  string `json:"sha256"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	// RawJSON is present only for one-time adoption by a pre-push parked run.
	// It is captured in the immutable pin, never reopened from caller storage.
	RawJSON string `json:"raw_json,omitempty"`
}

type Content struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func ValidatePath(p string) error {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\:") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return fmt.Errorf("public description requires a repository-relative committed file path")
	}
	return nil
}

// Read reads the committed blob, never a mutable working copy or private file.
func Read(ctx context.Context, dir, head, p string) (*Snapshot, error) {
	if p == "" {
		return nil, nil
	}
	if err := ValidatePath(p); err != nil {
		return nil, err
	}
	if len(head) != 40 || strings.Trim(head, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("public description requires an exact source commit")
	}
	raw, err := git.RunRaw(ctx, dir, "show", head+":"+p)
	if err != nil {
		return nil, fmt.Errorf("read committed public description: %w", err)
	}
	return parse(raw, head, p, false)
}

// Capture parses independently supplied committable material for an existing
// run, without editing the application's source or replacing validation intent.
func Capture(raw []byte, submittedHead string) (*Snapshot, error) {
	return parse(raw, submittedHead, "", true)
}

// ReadFile reads bounded caller input. Only its captured bytes, not this path,
// cross the RPC boundary or become publication state.
func ReadFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("public description must be a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if _, err := Capture(raw, strings.Repeat("0", 40)); err != nil {
		return nil, err
	}
	return raw, nil
}

// Verify re-establishes the content/hash/source binding at every emission.
func (s *Snapshot) Verify(ctx context.Context, dir string) error {
	var source *Snapshot
	var err error
	if s.RawJSON != "" {
		source, err = Capture([]byte(s.RawJSON), s.HeadSHA)
	} else {
		source, err = Read(ctx, dir, s.HeadSHA, s.Path)
	}
	if err != nil {
		return err
	}
	if source == nil || *source != *s {
		return fmt.Errorf("public description source differs from the run pin; refusing publication")
	}
	return nil
}

func parse(raw []byte, head, p string, captured bool) (*Snapshot, error) {
	if len(head) != 40 || strings.Trim(head, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("public description requires an exact source commit")
	}
	if len(raw) > MaxBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("public description must be UTF-8 and at most %d bytes", MaxBytes)
	}
	var content Content
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return nil, fmt.Errorf("public description must be a JSON object with title and body: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("public description contains trailing JSON")
	}
	if strings.TrimSpace(content.Title) == "" || strings.TrimSpace(content.Body) == "" || strings.ContainsAny(content.Title, "\r\n") {
		return nil, fmt.Errorf("public description requires a nonempty single-line title and nonempty body")
	}
	// Raw marker scans are used by attestation consumers. Independently supplied
	// public material must not introduce a second live marker or owned appendix.
	if strings.Contains(content.Body, "<!-- no-mistakes-") {
		return nil, fmt.Errorf("public description cannot contain no-mistakes protocol markers")
	}
	sum := sha256.Sum256(raw)
	s := &Snapshot{Path: p, HeadSHA: head, SHA256: fmt.Sprintf("%x", sum), Title: content.Title, Body: content.Body}
	if captured {
		s.RawJSON = string(raw)
	}
	return s, nil
}

func (s Snapshot) Value() (driver.Value, error) { return json.Marshal(s) }
func (s *Snapshot) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("invalid public description snapshot type %T", src)
	}
	return json.Unmarshal(raw, s)
}

// MatchesRequest allows omission on reattach, never replacement of a pin.
func (s *Snapshot) MatchesRequest(p string) bool { return p == "" || (s != nil && s.Path == p) }
