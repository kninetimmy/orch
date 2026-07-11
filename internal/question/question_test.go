package question

import (
	"encoding/json"
	"testing"
)

func TestDocKindRoundTrip(t *testing.T) {
	kinds := []DocKind{DocQuestions, DocSummary, DocComplete, DocAborted}
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			doc := Document{SchemaVersion: SchemaVersion, Kind: k}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got Document
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.Kind != k {
				t.Errorf("round-tripped Kind = %q, want %q", got.Kind, k)
			}
		})
	}
}

func TestDocumentOmitsEmptyOptionalFields(t *testing.T) {
	doc := Document{SchemaVersion: SchemaVersion, Kind: DocAborted}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"progress", "questions", "summary", "complete"} {
		if _, ok := raw[key]; ok {
			t.Errorf("aborted document carries unexpected key %q: %s", key, data)
		}
	}
}
