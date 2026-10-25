package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

func TestProviderRenameKeepsKeyRestrictions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer provider.Hold()()
	p := provider.Provider{ID: "relay", Name: "Relay", Chat: "http://127.0.0.1:1/v1", Key: "first"}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	keys := []Key{{ID: "limited", Secret: "test-secret", Models: []string{"RELAY/vendor/model*", "group/pool", "other/*", "rel*/*"}, Accounts: []string{"RELAY/first", "other/second"}}, {ID: "unlimited"}}
	if err := save(keys); err != nil {
		t.Fatal(err)
	}
	before, ok := Authenticate("test-secret")
	if !ok {
		t.Fatal("key did not authenticate before rename")
	}
	if err := provider.Rename("relay", "fast"); err != nil {
		t.Fatal(err)
	}
	who, ok := Authenticate("test-secret")
	if !ok || !who.Allows("fast/vendor/model-a") || who.AllowsAccount("fast", "second") || !who.AllowsAccount("fast", "first") {
		t.Fatalf("renamed provider lost restrictions: authenticated=%v models=%v accounts=%v", ok, who.Models, who.Accounts)
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var got []Key
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Models, []string{"fast/vendor/model*", "group/pool", "other/*", "rel*/*"}) || !slices.Equal(got[0].Accounts, []string{"fast/first", "other/second"}) || len(got[1].Models)+len(got[1].Accounts) != 0 {
		t.Fatalf("persisted restrictions: %+v", got)
	}
	if before.Allows("fast/vendor/model-a") || before.AllowsAccount("fast", "first") || before.AllowsAccount("fast", "second") {
		t.Fatal("identity authenticated before rename was used with a new catalog")
	}
	if err := provider.Rename("fast", "faster"); err != nil {
		t.Fatal(err)
	}
	who, ok = Authenticate("test-secret")
	if !ok || !who.Allows("faster/vendor/model-a") || who.AllowsAccount("faster", "second") {
		t.Fatal("chained rename lost its restrictions")
	}
	if err := provider.Rename("faster", "relay"); err != nil {
		t.Fatal(err)
	}
	who, ok = Authenticate("test-secret")
	if !ok || !who.Allows("relay/vendor/model-a") || who.AllowsAccount("relay", "second") {
		t.Fatal("rename back lost its restrictions")
	}
}

func TestProviderReuseRepairsStaleKeyRestrictions(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "rename"}[rename], func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			for _, p := range []provider.Provider{
				{ID: "fast", Was: []string{"relay"}, Name: "Fast", Chat: "http://127.0.0.1:1/v1", Key: "first"},
				{ID: "other", Name: "Other", Chat: "http://127.0.0.1:1/v1", Key: "second"},
			} {
				if err := provider.Save(p); err != nil {
					t.Fatal(err)
				}
			}
			if err := save([]Key{{ID: "limited", Secret: "test-secret", Models: []string{"relay/*"}, Accounts: []string{"relay/first"}}}); err != nil {
				t.Fatal(err)
			}
			var err error
			if rename {
				err = provider.Rename("other", "relay")
			} else {
				err = provider.Save(provider.Provider{ID: "relay", Name: "New", Chat: "http://127.0.0.1:1/v1", Key: "second"})
			}
			if err != nil {
				t.Fatal(err)
			}
			who, ok := Authenticate("test-secret")
			if !ok || !who.Allows("fast/model") || who.Allows("relay/model") || who.AllowsAccount("fast", "second") {
				t.Fatalf("reused id took stale permissions: %+v, authenticated=%v", who, ok)
			}
		})
	}
}

func TestProviderRenameRefusesUnreadableKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := provider.Save(provider.Provider{ID: "fast", Was: []string{"relay"}, Name: "Fast", Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(provider.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Rename("fast", "faster"); err == nil {
		t.Fatal("renamed with unreadable restrictions")
	}
	if err := provider.Save(provider.Provider{ID: "relay", Name: "New", Chat: "http://127.0.0.1:1/v1"}); err == nil {
		t.Fatal("reused an alias with unreadable restrictions")
	}
	after, _ := os.ReadFile(provider.Path())
	if string(before) != string(after) {
		t.Fatal("provider changed despite unreadable restrictions")
	}
	if b, _ := os.ReadFile(Path()); string(b) != "broken" {
		t.Fatal("unreadable restrictions were overwritten")
	}
}

func TestProviderRenameFailedKeyWriteStaysClosed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix file write permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Chat: "http://127.0.0.1:1/v1"}); err != nil {
		t.Fatal(err)
	}
	if err := save([]Key{{ID: "limited", Secret: "test-secret", Models: []string{"relay/*"}, Accounts: []string{"relay/first"}}}); err != nil {
		t.Fatal(err)
	}
	s := settings.Load()
	s.Visible = map[string][]string{"codex": {"relay"}}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	stepPath := filepath.Join(settings.Dir(), "stepfun-auth.json")
	if err := os.WriteFile(stepPath, []byte(`{"relay":{"account":"test-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := provider.SyncAccessRenames
	t.Cleanup(func() {
		provider.SyncAccessRenames = original
		os.Chmod(Path(), 0o600)
	})
	calls := 0
	provider.SyncAccessRenames = func() error {
		calls++
		if calls == 2 {
			if err := os.Chmod(Path(), 0o400); err != nil {
				t.Fatal(err)
			}
		}
		return original()
	}
	err := provider.Rename("relay", "fast")
	if err == nil || !strings.Contains(err.Error(), "provider renamed") {
		t.Fatalf("partial failure not reported: %v", err)
	}
	if !slices.Equal(settings.Load().Visible["codex"], []string{"fast"}) {
		t.Fatal("failed access write skipped settings migration")
	}
	stepBytes, err := os.ReadFile(stepPath)
	if err != nil || !strings.Contains(string(stepBytes), `"fast"`) || strings.Contains(string(stepBytes), `"relay"`) {
		t.Fatalf("failed access write skipped StepFun migration: %s, %v", stepBytes, err)
	}
	if _, ok := Authenticate("test-secret"); ok {
		t.Fatal("authenticated despite failure to save repaired restrictions")
	}
	if err := os.Chmod(Path(), 0o600); err != nil {
		t.Fatal(err)
	}
	who, ok := Authenticate("test-secret")
	if !ok || !who.Allows("fast/model") || who.AllowsAccount("fast", "second") {
		t.Fatal("restriction repair did not recover after write became possible")
	}
}

func TestProviderRestrictionsDoNotGuessReusedIDs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, p := range []provider.Provider{
		{ID: "relay", Name: "New", Chat: "http://127.0.0.1:1/v1"},
		{ID: "fast", Was: []string{"relay", "codex"}, Name: "Fast", Chat: "http://127.0.0.1:1/v1"},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := save([]Key{{ID: "limited", Secret: "test-secret", Models: []string{"relay/*", "codex/*"}, Accounts: []string{"relay/first", "codex/login"}}}); err != nil {
		t.Fatal(err)
	}
	who, ok := Authenticate("test-secret")
	if !ok {
		t.Fatal("key did not authenticate")
	}
	if who.Allows("fast/model") || !who.Allows("relay/model") || !who.Allows("codex/model") || who.AllowsAccount("relay", "second") {
		t.Fatal("assigned an active provider or subscription id to a historical owner")
	}
	if err := os.Remove(provider.Path() + ".bak"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider.Path(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if who.Allows("relay/model") || who.AllowsAccount("fast", "second") {
		t.Fatal("unreadable providers widened restrictions")
	}
}

func TestProviderRenameBackupCannotWidenAccountAccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, second := provider.KeyID("first"), provider.KeyID("second")
	if err := provider.Save(provider.Provider{ID: "relay", Chat: "http://127.0.0.1:1/v1", Key: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := save([]Key{{ID: "limited", Secret: "test-secret", Accounts: []string{"relay/" + first}}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Rename("relay", "fast"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(provider.Path() + ".bak"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider.Path(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := provider.Stored()
	if err != nil || len(ps) != 1 || ps[0].ID != "relay" {
		t.Fatalf("expected routing to recover the old provider: %v, %v", ps, err)
	}
	if who, ok := Authenticate("test-secret"); ok {
		t.Fatalf("authenticated against an older provider generation; unselected account allowed=%v", who.AllowsAccount("relay", second))
	}
	if err := provider.Save(provider.Provider{ID: "unrelated", Chat: "http://127.0.0.1:1/v1", Key: "key"}); err == nil {
		t.Error("an unrelated edit promoted the old backup to current state")
	}
	if who, ok := Authenticate("test-secret"); ok {
		t.Fatalf("backup promotion reopened access; unselected account allowed=%v", who.AllowsAccount("relay", second))
	}
}

func TestProviderOverlappingRenamesKeepAccountAccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, second := provider.KeyID("first"), provider.KeyID("second")
	if err := provider.Save(provider.Provider{ID: "relay", Chat: "http://127.0.0.1:1/v1", Key: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := save([]Key{{ID: "limited", Secret: "test-secret", Accounts: []string{"relay/" + first}}}); err != nil {
		t.Fatal(err)
	}
	original := provider.SyncAccessRenames
	t.Cleanup(func() { provider.SyncAccessRenames = original })
	interleaved := false
	provider.SyncAccessRenames = func() error {
		if !interleaved {
			// The outer rename has read relay, but has not stored it yet.
			interleaved = true
			if err := provider.Rename("relay", "fast"); err != nil {
				return err
			}
		}
		return original()
	}
	if err := provider.Rename("relay", "other"); err == nil {
		t.Error("a stale rename overwrote the completed rename")
	}
	ps, err := provider.Stored()
	if err != nil || len(ps) != 1 {
		t.Fatalf("providers: %v, %v", ps, err)
	}
	who, ok := Authenticate("test-secret")
	if !ok || !who.AllowsAccount(ps[0].ID, first) || who.AllowsAccount(ps[0].ID, second) {
		t.Fatalf("overlapping renames lost account restriction: provider=%s authenticated=%v accounts=%v", ps[0].ID, ok, who.Accounts)
	}
}

func TestProviderReuseCannotTakeIncomingAliasPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := provider.Save(provider.Provider{ID: "fast", Was: []string{"relay"}, Chat: "http://127.0.0.1:1/v1", Key: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := save([]Key{{ID: "limited", Secret: "test-secret", Models: []string{"other/*"}}}); err != nil {
		t.Fatal(err)
	}
	original := provider.SyncAccessRenames
	t.Cleanup(func() { provider.SyncAccessRenames = original })
	provider.SyncAccessRenames = func() error {
		if err := original(); err != nil {
			return err
		}
		// A stale picker saves after old references were repaired, while
		// relay still resolves to fast and before its id is given away.
		_, err := Update("models-key", Change{Key: "limited", Models: []string{"relay/*"}})
		return err
	}
	if err := provider.Save(provider.Provider{ID: "relay", Chat: "http://127.0.0.1:1/v1", Key: "second"}); err != nil {
		t.Fatal(err)
	}
	who, ok := Authenticate("test-secret")
	if !ok || !who.Allows("fast/model") || who.Allows("relay/model") {
		t.Fatalf("incoming permission transferred to reused id: authenticated=%v models=%v", ok, who.Models)
	}
}
