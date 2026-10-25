package access

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/provider"
)

func TestProviderRenameOffSubscriptionID(t *testing.T) {
	for _, failure := range []string{"none", "stage", "provider", "finish", "restore"} {
		t.Run(failure, func(t *testing.T) {
			if failure != "none" && failure != "provider" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("Unix file write permissions")
			}
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			// New providers cannot take a subscription id. This is the
			// providers.json an older version was allowed to save.
			b, err := json.Marshal(map[string]any{"providers": []provider.Provider{{ID: "codex", Name: "Legacy relay", Chat: "http://127.0.0.1:1/v1", Key: "first"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(provider.Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(provider.Path(), b, 0o600); err != nil {
				t.Fatal(err)
			}
			provider.Changed()
			first, second := provider.KeyID("first"), provider.KeyID("second")
			if err := save([]Key{
				{ID: "limited", Secret: "test-secret", Models: []string{"CODEX/*"}, Accounts: []string{"codex/" + first}},
				{ID: "disabled", Off: true, Secret: "disabled-secret", Models: []string{"codex/*"}},
				{ID: "other", Secret: "other-secret", Models: []string{"other/*"}},
			}); err != nil {
				t.Fatal(err)
			}
			original := provider.RenameAccessWithoutAlias
			t.Cleanup(func() {
				provider.RenameAccessWithoutAlias = original
				os.Chmod(Path(), 0o600)
			})
			if failure == "stage" {
				if err := os.Chmod(Path(), 0o400); err != nil {
					t.Fatal(err)
				}
			}
			provider.RenameAccessWithoutAlias = func(from, to string, store func() error) (bool, error) {
				return original(from, to, func() error {
					if failure == "provider" || failure == "restore" {
						if failure == "restore" {
							if err := os.Chmod(Path(), 0o400); err != nil {
								t.Fatal(err)
							}
						}
						return errors.New("provider write failed")
					}
					if err := store(); err != nil {
						return err
					}
					if failure == "finish" {
						if err := os.Chmod(Path(), 0o400); err != nil {
							t.Fatal(err)
						}
					}
					return nil
				})
			}
			err = provider.Rename("codex", "legacy-relay")
			if (err != nil) != (failure != "none") {
				t.Fatalf("rename result: %v", err)
			}
			moved := failure == "none" || failure == "finish"
			wantID := "codex"
			if moved {
				wantID = "legacy-relay"
			}
			p, findErr := provider.Find(wantID)
			if findErr != nil || p.ID != wantID || slices.Contains(p.Was, "codex") || provider.Renamed()["codex"] != "" {
				t.Fatalf("subscription id became an alias or provider moved incorrectly: %+v, %v", p, findErr)
			}
			who, ok := Authenticate("test-secret")
			if failure == "finish" || failure == "restore" {
				if ok || err == nil || !strings.Contains(err.Error(), "disabled") {
					t.Fatalf("failed finish/restore did not leave key safely disabled: authenticated=%v error=%v", ok, err)
				}
			} else if !ok || !who.Allows(wantID+"/model") || !who.AllowsAccount(wantID, first) || who.AllowsAccount(wantID, second) {
				t.Fatalf("rename lost restrictions: authenticated=%v identity=%+v", ok, who)
			}
			if _, ok := Authenticate("disabled-secret"); ok {
				t.Fatal("an already disabled key was enabled")
			}
			if _, ok := Authenticate("other-secret"); !ok {
				t.Fatal("an unrelated key was disabled")
			}
			if failure == "finish" {
				if err := os.Chmod(Path(), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Update("on-key", Change{Key: "limited"}); err != nil {
					t.Fatal(err)
				}
				who, ok := Authenticate("test-secret")
				if !ok || !who.Allows("legacy-relay/model") || who.AllowsAccount("legacy-relay", second) {
					t.Fatal("enabling the staged key lost its migrated restrictions")
				}
			}
		})
	}
}
