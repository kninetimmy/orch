package config

import (
	"os"
	"strings"
	"testing"
)

func TestResidualRiskOptInIsCommittedAndRoundTrips(t *testing.T) {
	data, err := os.ReadFile("testdata/valid/minimal.toml")
	if err != nil {
		t.Fatal(err)
	}
	off, err := Parse(data)
	if err != nil || off.ResidualRisk != nil {
		t.Fatalf("opt-out: %+v, %v", off, err)
	}
	section := "\n[residual_risk]\nadmin_user_id = \"U_user\"\n"
	on, err := Parse(append(data, section...))
	if err != nil || on.ResidualRisk == nil || on.ResidualRisk.AdminUserID != "U_user" {
		t.Fatalf("opt-in: %+v, %v", on, err)
	}
	rendered, err := Render(on)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(rendered)
	if err != nil || again.ResidualRisk == nil || *again.ResidualRisk != *on.ResidualRisk {
		t.Fatalf("round trip: %+v, %v", again, err)
	}
	if _, err := Parse(append(data, "\n[residual_risk]\n"...)); err == nil {
		t.Fatal("empty designation accepted")
	}
	if _, err := MergeLocal(off, []byte(section)); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("local designation: %v", err)
	}
	before, err := Revision(off)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Revision(on)
	if err != nil || after == before {
		t.Fatalf("opt-in did not change semantic revision: %s %s, %v", before, after, err)
	}
}
