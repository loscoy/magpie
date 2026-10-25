package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
)

// Old model URLs deliberately remain routable. Authorization uses the
// resolved provider, however: stale model restrictions used to refuse both
// names, and stale account restrictions used to allow both upstream keys.
func TestProviderRenameGatewayMatrix(t *testing.T) {
	const oldID, newID, model = "newapi-string-deepseek", "string-deepseek", "deepseek-v4.1-flash"
	for _, models := range []string{"old", "new", "all", "other"} {
		for _, accounts := range []string{"old", "new", "all", "gone"} {
			t.Run(models+"-models/"+accounts+"-accounts", func(t *testing.T) {
				fresh(t)
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":%q,"choices":[]}`, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				}))
				t.Cleanup(up.Close)
				if err := provider.Save(provider.Provider{ID: oldID, Name: "String DeepSeek", Chat: up.URL + "/v1", Key: "first", Keys: []provider.KeyAccount{{Key: "second"}}, Models: []string{model}, Routing: provider.Ordered}); err != nil {
					t.Fatal(err)
				}
				if err := provider.Rename(oldID, newID); err != nil {
					t.Fatal(err)
				}
				// Seed the on-disk forms an older magpie left, plus current
				// and unrestricted controls. Each request really authenticates.
				key := access.Key{ID: "matrix", Name: "Matrix", Secret: access.Prefix + "matrix-secret"}
				if models != "all" {
					key.Models = []string{map[string]string{"old": oldID, "new": newID, "other": "unrelated"}[models] + "/*"}
				}
				if accounts != "all" {
					pid, ref := newID, provider.KeyID("second")
					if accounts == "old" {
						pid = oldID
					}
					if accounts == "gone" {
						ref = provider.KeyID("removed")
					}
					key.Accounts = []string{pid + "/" + ref}
				}
				b, err := json.Marshal([]access.Key{key})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(access.Path(), b, 0o600); err != nil {
					t.Fatal(err)
				}
				gw := httptest.NewServer(lanGuard(New().Handler()))
				t.Cleanup(gw.Close)
				for _, pid := range []string{oldID, newID} {
					req, err := http.NewRequest(http.MethodPost, gw.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+pid+"/"+model+`","messages":[{"role":"user","content":"hi"}]}`))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+key.Secret)
					req.Header.Set("Content-Type", "application/json")
					res, err := gw.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(res.Body)
					res.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					var reply struct{ ID string }
					json.Unmarshal(body, &reply)
					t.Logf("request=%s models=%s accounts=%s status=%d upstream-key=%s", pid, models, accounts, res.StatusCode, reply.ID)
					wantStatus, wantKey := http.StatusOK, "second"
					if accounts == "all" {
						wantKey = "first"
					}
					if models == "other" || accounts == "gone" {
						wantStatus, wantKey = http.StatusForbidden, ""
					}
					if res.StatusCode != wantStatus || reply.ID != wantKey {
						t.Errorf("request=%s: got %d/%q, want %d/%q: %s", pid, res.StatusCode, reply.ID, wantStatus, wantKey, body)
					}
				}
			})
		}
	}
}
