package instructions

import (
	"errors"
	"testing"
)

func TestLocate(t *testing.T) {
	begin1 := "<!-- orchestrator:managed:start version=1 -->"
	begin2 := "<!-- orchestrator:managed:start version=2 -->"
	end := EndMarker

	cases := map[string]struct {
		content string
		wantErr error // nil means success
		want    location
	}{
		"absent": {
			content: "just some prose\nno markers here\n",
			wantErr: errNotFound,
		},
		"empty content": {
			content: "",
			wantErr: errNotFound,
		},
		"valid": {
			content: begin1 + "\nbody\n" + end + "\n",
			want:    location{begin: 0, end: 2, version: 1},
		},
		"valid no trailing newline": {
			content: begin1 + "\nbody\n" + end,
			want:    location{begin: 0, end: 2, version: 1},
		},
		"valid version two": {
			content: begin2 + "\nbody\n" + end + "\n",
			want:    location{begin: 0, end: 2, version: 2},
		},
		"valid with surrounding prose": {
			content: "intro\n\n" + begin1 + "\nbody\n" + end + "\n\noutro\n",
			want:    location{begin: 2, end: 4, version: 1},
		},
		"duplicate begin": {
			content: begin1 + "\n" + begin1 + "\nbody\n" + end + "\n",
			wantErr: ErrMalformed,
		},
		"duplicate end": {
			content: begin1 + "\nbody\n" + end + "\n" + end + "\n",
			wantErr: ErrMalformed,
		},
		"end before begin (reversed)": {
			content: end + "\nbody\n" + begin1 + "\n",
			wantErr: ErrMalformed,
		},
		"nested": {
			content: begin1 + "\n" + begin2 + "\nbody\n" + end + "\n" + end + "\n",
			wantErr: ErrMalformed,
		},
		"unpaired begin": {
			content: begin1 + "\nbody with no end\n",
			wantErr: ErrMalformed,
		},
		"unpaired end": {
			content: "body with no begin\n" + end + "\n",
			wantErr: ErrMalformed,
		},
		"crlf markers": {
			content: begin1 + "\r\nbody\r\n" + end + "\r\n",
			want:    location{begin: 0, end: 2, version: 1},
		},
		"near-miss wrong spacing ignored": {
			content: "<!--orchestrator:managed:start version=1-->\nprose\n",
			wantErr: errNotFound,
		},
		"near-miss missing version ignored": {
			content: "<!-- orchestrator:managed:start -->\nprose\n",
			wantErr: errNotFound,
		},
		"near-miss leading zero ignored": {
			content: "<!-- orchestrator:managed:start version=01 -->\nprose\n",
			wantErr: errNotFound,
		},
		"near-miss negative sign ignored": {
			content: "<!-- orchestrator:managed:start version=-1 -->\nprose\n",
			wantErr: errNotFound,
		},
		"near-miss mid-line mention ignored": {
			content: "See " + begin1 + " mentioned inline.\n",
			wantErr: errNotFound,
		},
		"version overflow is malformed": {
			content: "<!-- orchestrator:managed:start version=99999999999999999999 -->\nbody\n" + end + "\n",
			wantErr: ErrMalformed,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lines := splitLines(tc.content)
			got, err := locate(lines)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("locate() err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("locate(): %v", err)
			}
			if got != tc.want {
				t.Errorf("locate() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
