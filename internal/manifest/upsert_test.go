package manifest

import (
	"errors"
	"strings"
	"testing"
)

func mustUpsert(t *testing.T, body string, m Manifest) string {
	t.Helper()
	out, err := Upsert(body, m)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return out
}

func TestUpsertAppend(t *testing.T) {
	m := fullManifest()
	region := mustRender(t, m)

	cases := map[string]struct {
		body   string
		prefix string // must be preserved byte-identically at the front
	}{
		"empty body":          {"", ""},
		"no trailing newline": {"human notes", "human notes"},
		"trailing newline":    {"human notes\n", "human notes\n"},
		"crlf body":           {"human notes\r\n", "human notes\r\n"},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			got := mustUpsert(t, tt.body, m)
			if !strings.HasPrefix(got, tt.prefix) {
				t.Errorf("prefix not preserved\n got %q\nwant prefix %q", got, tt.prefix)
			}
			if !strings.HasSuffix(got, region) {
				t.Error("appended region is not the rendered manifest")
			}
			round, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse(Upsert): %v", err)
			}
			if !equalManifest(round, m) {
				t.Errorf("round trip mismatch: %+v", round)
			}
		})
	}

	// Empty body appends nothing but the region, keeping idempotence sound.
	if got := mustUpsert(t, "", m); got != region {
		t.Errorf("Upsert(\"\") = %q, want the bare region", got)
	}
}

func TestUpsertReplacePreservesSurroundings(t *testing.T) {
	old := mustRender(t, minimalManifest())
	prefix, suffix := "human intro\r\n\r\n", "\r\nhuman outro\r\n"
	body := prefix + old + suffix

	got := mustUpsert(t, body, fullManifest())
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("CRLF prefix not preserved verbatim: %q", got)
	}
	if !strings.HasSuffix(got, suffix) {
		t.Errorf("CRLF suffix not preserved verbatim: %q", got)
	}
	if strings.Contains(got, "cfg-2026-07-10") == false {
		t.Error("replaced region does not carry the new manifest")
	}
	round, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !equalManifest(round, fullManifest()) {
		t.Errorf("mismatch: %+v", round)
	}
}

func TestUpsertIdempotent(t *testing.T) {
	m := fullManifest()
	bodies := map[string]string{
		"empty":            "",
		"plain":            "notes",
		"crlf":             "notes\r\n",
		"with region":      "intro\n\n" + mustRender(t, minimalManifest()) + "\n\noutro\n",
		"crlf with region": "intro\r\n\r\n" + mustRender(t, minimalManifest()) + "\r\noutro\r\n",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			once := mustUpsert(t, body, m)
			twice := mustUpsert(t, once, m)
			if once != twice {
				t.Errorf("Upsert is not idempotent\n once  %q\n twice %q", once, twice)
			}
		})
	}
}

func TestUpsertParseRoundTrip(t *testing.T) {
	// Replacing one manifest with a different one yields the second.
	body := "intro\n\n" + mustRender(t, minimalManifest()) + "\n\noutro\n"
	got := mustUpsert(t, body, hostileManifest())
	round, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !equalManifest(round, hostileManifest()) {
		t.Errorf("mismatch: %+v", round)
	}
}

func TestUpsertMalformedRegionDoesNotRewrite(t *testing.T) {
	body := BeginMarker + "\n### dangling region with no end marker\n"
	got, err := Upsert(body, fullManifest())
	if !errors.Is(err, ErrBadManifest) {
		t.Fatalf("err = %v, want ErrBadManifest", err)
	}
	if got != "" {
		t.Errorf("Upsert returned a rewritten body %q alongside the error", got)
	}
}

func TestUpsertInvalidManifestPropagates(t *testing.T) {
	bad := minimalManifest()
	bad.ConfigRevision = ""
	got, err := Upsert("existing body", bad)
	if err == nil || !strings.Contains(err.Error(), "config_revision") {
		t.Fatalf("err = %v, want Render validation error", err)
	}
	if got != "" {
		t.Errorf("Upsert returned %q alongside the error", got)
	}
}
