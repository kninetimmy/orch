package question

import (
	"strings"
	"testing"
)

func selectQuestion(opts ...Option) Question {
	return Question{ID: "q", Header: "Q", Prompt: "prompt?", Kind: KindSelect, Options: opts}
}

func TestSpecCheckSelect(t *testing.T) {
	tests := []struct {
		name    string
		q       Question
		wantErr bool
	}{
		{
			name: "valid two options",
			q:    selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"}),
		},
		{
			name: "valid four options with default",
			q: func() Question {
				q := selectQuestion(
					Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"},
					Option{Value: "c", Label: "C"}, Option{Value: "d", Label: "D"},
				)
				q.Default = "c"
				return q
			}(),
		},
		{
			name:    "one option",
			q:       selectQuestion(Option{Value: "a", Label: "A"}),
			wantErr: true,
		},
		{
			name: "five options",
			q: selectQuestion(
				Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"},
				Option{Value: "c", Label: "C"}, Option{Value: "d", Label: "D"},
				Option{Value: "e", Label: "E"},
			),
			wantErr: true,
		},
		{
			name:    "duplicate option values",
			q:       selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "a", Label: "A2"}),
			wantErr: true,
		},
		{
			name: "default not an option",
			q: func() Question {
				q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
				q.Default = "z"
				return q
			}(),
			wantErr: true,
		},
		{
			name: "default not an option but free text",
			q: func() Question {
				q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
				q.Default = "z"
				q.FreeText = true
				return q
			}(),
			wantErr: false,
		},
		{
			name: "header too long",
			q: func() Question {
				q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
				q.Header = "ThirteenChars"
				return q
			}(),
			wantErr: true,
		},
		{
			name: "empty prompt",
			q: func() Question {
				q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
				q.Prompt = ""
				return q
			}(),
			wantErr: true,
		},
		{
			name: "empty id",
			q: func() Question {
				q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
				q.ID = ""
				return q
			}(),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SpecCheck(tt.q)
			if (err != nil) != tt.wantErr {
				t.Errorf("SpecCheck() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSpecCheckText(t *testing.T) {
	tests := []struct {
		name    string
		q       Question
		wantErr bool
	}{
		{
			name: "valid text",
			q:    Question{ID: "q", Header: "Q", Prompt: "prompt?", Kind: KindText},
		},
		{
			name:    "text with options",
			q:       Question{ID: "q", Header: "Q", Prompt: "prompt?", Kind: KindText, Options: []Option{{Value: "a", Label: "A"}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SpecCheck(tt.q)
			if (err != nil) != tt.wantErr {
				t.Errorf("SpecCheck() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSpecCheckUnknownKind(t *testing.T) {
	q := Question{ID: "q", Header: "Q", Prompt: "prompt?", Kind: "multi"}
	if err := SpecCheck(q); err == nil {
		t.Error("SpecCheck() succeeded for unknown kind, want error")
	}
}

func TestValidateAnswerSelect(t *testing.T) {
	q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})

	tests := []struct {
		name    string
		q       Question
		value   string
		wantErr bool
	}{
		{name: "matching option", q: q, value: "a"},
		{name: "non-option value", q: q, value: "c", wantErr: true},
		{
			name: "non-option value with free text",
			q: func() Question {
				fq := q
				fq.FreeText = true
				return fq
			}(),
			value: "claude-fable-5",
		},
		{
			name: "blank value with free text",
			q: func() Question {
				fq := q
				fq.FreeText = true
				return fq
			}(),
			value:   "   ",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAnswer(tt.q, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAnswer() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAnswerSelectErrorNamesIDAndOptions(t *testing.T) {
	q := selectQuestion(Option{Value: "a", Label: "A"}, Option{Value: "b", Label: "B"})
	err := ValidateAnswer(q, "z")
	if err == nil {
		t.Fatal("ValidateAnswer() succeeded, want error")
	}
	got := err.Error()
	for _, want := range []string{q.ID, "a", "b"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q does not mention %q", got, want)
		}
	}
}

func TestValidateAnswerText(t *testing.T) {
	q := Question{ID: "q", Header: "Q", Prompt: "prompt?", Kind: KindText}
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "non-empty", value: "hello"},
		{name: "empty", value: "", wantErr: true},
		{name: "whitespace only", value: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAnswer(q, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAnswer() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
