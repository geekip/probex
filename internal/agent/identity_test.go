package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestServerAssignedIdentityAndInvitationReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	request, err := loadIdentity(path, "", "node", "", "", false)
	if err != nil || request.ID != "" || request.Mode != "request" {
		t.Fatal(request, err)
	}
	repeated, err := loadIdentity(path, "", "node", "", "", false)
	if err != nil || repeated.Key != request.Key {
		t.Fatal("lost first response changed request credentials", err)
	}
	request.ID = "1"
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	pending, err := loadIdentity(path, "", "node", "", "", false)
	if err != nil || pending.ID != "1" || pending.Key != request.Key {
		t.Fatal("pending request identity not retained", err)
	}
	joined, err := loadIdentity(path, "1", "node", "", "invitation-a", false)
	if err != nil || joined.Key == request.Key || joined.Mode != "key" || joined.ID != "1" {
		t.Fatal("invitation did not replace request", err)
	}
	saved, err := loadIdentity(path, "1", "node", "", "invitation-a", false)
	if err != nil || saved.Key != joined.Key {
		t.Fatal("same invitation changed saved credentials", err)
	}
	replacement, err := loadIdentity(path, "1", "node", "", "invitation-b", false)
	if err != nil || replacement.Key == saved.Key || replacement.Enrolled {
		t.Fatal("new invitation with same ID did not replace credentials", err)
	}
	rejoin, err := loadIdentity(path, "", "node", "", "", true)
	if err != nil || rejoin.ID != "" || rejoin.Mode != "request" || rejoin.Key == replacement.Key {
		t.Fatal("explicit rejoin did not create a fresh request", err)
	}
}
