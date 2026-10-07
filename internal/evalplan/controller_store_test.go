package evalplan

import (
	"bytes"
	"os"
	"testing"
)

// The fixture was encoded by storedBytes before the native evaluation types
// moved out of codexnative (#338). Stored records must keep identical bytes.
func TestVersion2NativeAttemptRecordWireBytes(t *testing.T) {
	data, err := os.ReadFile("testdata/attempt-v2-native.json")
	if err != nil {
		t.Fatal(err)
	}
	var a AttemptRecord
	if err := strictStored(data, &a); err != nil {
		t.Fatal(err)
	}
	if a.SchemaVersion != 2 || a.Native == nil || a.Native.Binding == nil || a.Native.Instructions == nil || a.Native.Cleanup == nil {
		t.Fatalf("fixture lacks version-2 native evidence: %+v", a)
	}
	again, err := storedBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, data) {
		t.Fatalf("re-encoded attempt record differs:\n%s", again)
	}
}
