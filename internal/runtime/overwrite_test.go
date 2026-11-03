package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/kites262/piperouter/internal/config"
)

func TestOverwriteSnapshotHotUpdateAndReload(t *testing.T) {
	m, path, _ := newTestManager(t)
	cfg := m.Current().Config.Clone()
	cfg.Overwrite = config.OverwriteConfig{Enabled: true, Rules: []config.OverwriteRule{
		{Name: "alias", Enabled: true, Phase: "req", Field: "model", From: "a", To: "b"},
	}}
	cfg.Routes[0].Proxy.Overwrites = []string{"alias"}
	first, err := m.Apply(cfg, m.Current().Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Config.Overwrite.Rules[0]; got.Condition != (config.OverwriteCondition{Phase: "req", Path: ".model", Op: "eq", Value: "a"}) || got.Action != (config.OverwriteAction{Phase: "req", Path: ".model", Op: "set", Value: "b"}) {
		t.Fatalf("legacy rule did not normalize to condition/action: %#v", got)
	}
	assertModel := func(snap *Snapshot, want string) {
		t.Helper()
		got, err := snap.Table.Match("/api/chat/completions").Overwrite.Replace([]byte(`{"model":"a"}`))
		if err != nil || string(got) != `{"model":"`+want+`"}` {
			t.Fatalf("snapshot Replace = %s, %v; want %q", got, err, want)
		}
	}
	assertModel(first, "b")
	second, err := m.Update(first.Revision, func(c *config.Config) error {
		c.Overwrite.Rules[0].Action.Value = "c"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertModel(second, "c")
	assertModel(first, "b") // Existing requests retain their compiled rule set.

	contents := readFile(t, path)
	updatedContents := strings.Replace(contents, "value: c", "value: d", 1)
	if updatedContents == contents {
		t.Fatal("canonical YAML did not contain the updated action value")
	}
	if err := os.WriteFile(path, []byte(updatedContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadFromFile(); err != nil {
		t.Fatal(err)
	}
	third := m.Current()
	assertModel(third, "d")
	assertModel(second, "c")
	invalid := third.Config.Clone()
	invalid.Routes[0].Proxy.Overwrites = []string{"missing"}
	if err := config.WriteAtomic(path, invalid); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadFromFile(); err == nil {
		t.Fatal("invalid reference unexpectedly reloaded")
	}
	if m.Current() != third {
		t.Fatal("failed reload replaced the active snapshot")
	}
	assertModel(m.Current(), "d")

	if _, err := m.Update(third.Revision, func(c *config.Config) error {
		c.Overwrite.Enabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if m.Current().Table.Match("/api").Overwrite != nil {
		t.Fatal("disabled feature should compile no plan")
	}
	assertModel(third, "d")
}
