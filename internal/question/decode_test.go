package question

import (
	"fmt"
	"strings"
	"testing"
)

func TestDecodeAnswersValid(t *testing.T) {
	a, err := DecodeAnswers([]byte(fmt.Sprintf(`{"schema_version":%d,"answers":{"a":"1"}}`, SchemaVersion)))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	if a.Answers["a"] != "1" {
		t.Errorf("answers = %v, want a=1", a.Answers)
	}
}

func TestDecodeAnswersNormalizesNilMap(t *testing.T) {
	a, err := DecodeAnswers([]byte(fmt.Sprintf(`{"schema_version":%d}`, SchemaVersion)))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	if a.Answers == nil {
		t.Fatal("Answers is nil, want a non-nil empty map")
	}
	if len(a.Answers) != 0 {
		t.Errorf("Answers = %v, want empty", a.Answers)
	}
}

func TestDecodeAnswersRejectsUnknownFields(t *testing.T) {
	_, err := DecodeAnswers([]byte(fmt.Sprintf(`{"schema_version":%d,"answers":{},"bogus":true}`, SchemaVersion)))
	if err == nil {
		t.Fatal("DecodeAnswers succeeded, want error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err = %v, want mention of the unknown field", err)
	}
}

func TestDecodeAnswersRejectsPreviousSchemaVersion(t *testing.T) {
	_, err := DecodeAnswers([]byte(`{"schema_version":1,"answers":{}}`))
	if err == nil {
		t.Fatal("DecodeAnswers succeeded, want error")
	}
	for _, want := range []string{"schema_version 1", fmt.Sprintf("supports %d", SchemaVersion)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want mention of %q", err, want)
		}
	}
}

func TestDecodeAnswersRejectsTrailingData(t *testing.T) {
	_, err := DecodeAnswers([]byte(fmt.Sprintf(`{"schema_version":%d,"answers":{}}{}`, SchemaVersion)))
	if err == nil {
		t.Fatal("DecodeAnswers succeeded, want error")
	}
}

func TestDecodeAnswersRejectsMalformedJSON(t *testing.T) {
	_, err := DecodeAnswers([]byte(`{`))
	if err == nil {
		t.Fatal("DecodeAnswers succeeded, want error")
	}
}
