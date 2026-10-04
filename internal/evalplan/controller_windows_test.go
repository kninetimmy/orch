package evalplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestControllerWindowsDriveAliasAndReparse(t *testing.T) {
	if controllerTestProcess(t) {
		return
	}
	f := newControllerFixture(t, "screen", 1)
	e := f.prepare(t)
	approval := filepath.Join(f.root, "assertion-source.json")
	writeFixture(t, approval, fixtureJSON(t, Approval{1, f.record.PlanDigest, "test-human", now(), ApprovalStatement}))
	drive := ""
	for _, candidate := range []string{"Z:", "Y:", "X:", "W:"} {
		if _, err := os.Stat(candidate + `\`); !os.IsNotExist(err) {
			continue
		}
		if err := exec.CommandContext(t.Context(), "subst", candidate, f.root).Run(); err == nil {
			drive = candidate
			break
		}
	}
	if drive == "" {
		t.Fatal("unable to construct required temporary Windows drive-alias fixture")
	}
	t.Cleanup(func() {
		if err := exec.Command("subst", drive, "/D").Run(); err != nil {
			t.Error(err)
		}
	})
	if _, err := Load(t.Context(), f.repo, drive+`\`, f.record.PlanDigest); err == nil {
		t.Fatal("controller accepted drive alias for saved storage")
	}
	if _, err := openGuarded(drive + `\`); err == nil {
		t.Fatal("controller accepted drive alias root")
	}
	if _, err := ReadApproval(f.repo, filepath.Join(drive+`\`, "assertion-source.json")); err == nil {
		t.Fatal("approval reader accepted drive alias")
	}
	if _, err := Inspect(drive+`\`, e.ID); err == nil {
		t.Fatal("snapshot reader accepted drive alias")
	}
	if _, err := RetainReport(drive+`\`, e.ID); err == nil {
		t.Fatal("report publisher accepted drive alias")
	}
	alias := filepath.Join(filepath.Dir(f.root), "junction")
	// Values travel as environment data, not interpolated shell expressions.
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:ORCH_TEST_LINK -Target $env:ORCH_TEST_TARGET -ErrorAction Stop | Out-Null`)
	cmd.Env = append(os.Environ(), "ORCH_TEST_LINK="+alias, "ORCH_TEST_TARGET="+f.root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("required reparse fixture: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(alias); err != nil {
			t.Error(err)
		}
	})
	if _, err := openGuarded(alias); err == nil {
		t.Fatal("controller accepted reparse root")
	}
	if _, err := Load(t.Context(), f.repo, alias, f.record.PlanDigest); err == nil {
		t.Fatal("saved loader followed reparse root")
	}
	if _, err := ReadApproval(f.repo, filepath.Join(alias, "assertion-source.json")); err == nil {
		t.Fatal("approval reader followed reparse root")
	}
	if _, err := RetainReport(alias, e.ID); err == nil {
		t.Fatal("report publisher followed reparse root")
	}
}
