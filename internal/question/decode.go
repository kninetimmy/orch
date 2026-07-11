package question

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DecodeAnswers decodes data into an AnswerSet, rejecting any field
// this build does not recognize, any trailing data after the document,
// and any schema_version other than SchemaVersion — the same
// fail-closed discipline internal/run.DecodePlan applies to plan
// documents. A nil Answers map (the wire form omits an empty object's
// absence the same way) is normalized to an empty, non-nil map, so
// callers never special-case "no answers yet" against "answers present
// but nil".
func DecodeAnswers(data []byte) (AnswerSet, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var a AnswerSet
	if err := dec.Decode(&a); err != nil {
		return AnswerSet{}, fmt.Errorf("decode answer set: %w", err)
	}
	if dec.More() {
		return AnswerSet{}, fmt.Errorf("decode answer set: trailing data after document")
	}
	if a.SchemaVersion != SchemaVersion {
		return AnswerSet{}, fmt.Errorf("answer set: unsupported schema_version %d (this build supports %d)", a.SchemaVersion, SchemaVersion)
	}
	if a.Answers == nil {
		a.Answers = map[string]string{}
	}
	return a, nil
}
