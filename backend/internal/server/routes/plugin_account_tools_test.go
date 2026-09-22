package routes

import "testing"

func TestAccountToolDeletionResourcesRequireFullAccountScope(t *testing.T) {
	for _, name := range []string{"taxonomy.folders.delete", "taxonomy.tags.delete"} {
		if !accountToolResourceRequiresAllAccounts(name) {
			t.Fatalf("global deletion can change accounts outside the binding: %s", name)
		}
	}
	for _, name := range []string{"taxonomy.folders.list", "taxonomy.folders.create", "taxonomy.folders.order", "taxonomy.folders.update", "taxonomy.tags.list", "taxonomy.tags.create", "taxonomy.tags.order", "taxonomy.tags.update", "taxonomy.account.update", "taxonomy.bulk.update", "tests.models", "tests.submit"} {
		if accountToolResourceRequiresAllAccounts(name) {
			t.Fatalf("unrelated metadata or explicit account operations must retain their original scope: %s", name)
		}
	}
}
