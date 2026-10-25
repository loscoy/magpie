package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/provider"
)

func init() {
	provider.SyncAccessRenames = func() error {
		mu.Lock()
		defer mu.Unlock()
		_, err := load()
		return err
	}
	provider.RenameAccessWithoutAlias = renameWithoutAlias
}

// An authenticated request keeps one restriction snapshot. If routing has
// moved to another set of provider ids meanwhile, refuse it: the next
// request authenticates with the repaired prefixes. The version is memoized
// with the routing catalog, not rebuilt for each model or account.
func (who Identity) currentProviders() bool {
	if who.renameVersion == "" {
		return true
	}
	now, err := provider.AccessRenameVersion()
	return err == nil && now == who.renameVersion
}

// renamedProviderRef changes only a literal provider component. A glob
// spanning providers, a group and the model or account suffix stay as given.
func renamedProviderRef(ref string, renames map[string]string) string {
	pid, rest, ok := strings.Cut(strings.TrimSpace(ref), "/")
	if to := renames[strings.ToLower(pid)]; ok && to != "" {
		return to + "/" + rest
	}
	return ref
}

// repairProviderRefs is called under mu. Saving before handing keys out
// makes the repair survive an old id being assigned to another provider.
// A failed read or write refuses authentication instead of losing a hold.
func repairProviderRefs(keys []Key) (string, error) {
	needed := false
	for _, k := range keys {
		needed = needed || len(k.Models)+len(k.Accounts) > 0
	}
	if !needed {
		return "", nil
	}
	renames, version, err := provider.ReadRenamed()
	if err != nil {
		return "", err
	}
	changed := false
	for i := range keys {
		changed = renameKeyRefs(&keys[i], renames) || changed
	}
	if changed {
		return version, save(keys)
	}
	return version, nil
}

func renameKeyRefs(key *Key, renames map[string]string) bool {
	changed := false
	for _, refs := range [][]string{key.Models, key.Accounts} {
		for j, ref := range refs {
			if now := renamedProviderRef(ref, renames); now != ref {
				refs[j], changed = now, true
			}
		}
	}
	return changed
}

// A subscription id cannot remain an alias for a legacy custom provider.
// Keep changed keys disabled on disk while the two files are committed:
// even a crash or failed rollback cannot expose an account without its hold.
// After a failed final write, their new references are saved but they remain
// disabled; the administrator can enable them again once writes work.
func renameWithoutAlias(from, to string, store func() error) (bool, error) {
	mu.Lock()
	defer mu.Unlock()
	keys, err := load()
	if err != nil {
		return false, err
	}
	moved := slices.Clone(keys)
	var changed []int
	for i := range moved {
		moved[i].Models = slices.Clone(moved[i].Models)
		moved[i].Accounts = slices.Clone(moved[i].Accounts)
		if renameKeyRefs(&moved[i], map[string]string{from: to}) {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		err := store()
		return err == nil, err
	}
	staged := slices.Clone(moved)
	for _, i := range changed {
		staged[i].Off = true
	}
	if err := save(staged); err != nil {
		return false, err
	}
	if err := store(); err != nil {
		if restoreErr := save(keys); restoreErr != nil {
			return false, errors.Join(err, fmt.Errorf("gateway keys left disabled; restore their provider references before enabling them: %w", restoreErr))
		}
		return false, err
	}
	if err := save(moved); err != nil {
		return true, fmt.Errorf("provider renamed; affected gateway keys have their new references but remain disabled: %w", err)
	}
	return true, nil
}
