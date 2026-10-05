package names

import (
	"regexp"
	"testing"
)

func TestGenerateIsDeterministicAndWellFormed(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{2}$`)
	a := Generate("dc8701d0-14aa-41ff-8b13-e3bea6aa634a", 0)
	if a != Generate("dc8701d0-14aa-41ff-8b13-e3bea6aa634a", 0) {
		t.Fatal("same session id must give the same name")
	}
	if !re.MatchString(a) {
		t.Fatalf("unexpected name format: %q", a)
	}
	if a == Generate("dc8701d0-14aa-41ff-8b13-e3bea6aa634a", 1) {
		t.Fatal("a retry attempt should produce a different name")
	}
}
