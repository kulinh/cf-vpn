package xray

import "testing"

func TestAddAndRemoveUser(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := AddUser(&cfg, "alice", "uuid-a", "pass-a"); err != nil {
		t.Fatal(err)
	}
	names := ListUserNames(cfg)
	if len(names) != 2 {
		t.Fatalf("expected 2 users, got %d", len(names))
	}
	if err := RemoveUser(&cfg, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(ListUserNames(cfg)) != 1 {
		t.Fatalf("expected 1 user after remove")
	}
}

func TestValidateUserName(t *testing.T) {
	good := []string{"alice", "user_1", "user-1", "A", "a123456789012345678901234567890AB"[:32]}
	for _, n := range good {
		if err := ValidateUserName(n); err != nil {
			t.Errorf("expected %q valid: %v", n, err)
		}
	}
	bad := []string{"", "has space", "name!", "a123456789012345678901234567890ABC"}
	for _, n := range bad {
		if err := ValidateUserName(n); err == nil {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestAddUserRejectsDuplicate(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := AddUser(&cfg, "user1", "uuid-2", "pass-2"); err == nil {
		t.Fatalf("expected duplicate to error")
	}
}

func TestRemoveUserMissing(t *testing.T) {
	cfg := NewBaseConfig("user1", "uuid-1", "pass-1")
	if err := RemoveUser(&cfg, "nobody"); err == nil {
		t.Fatalf("expected missing to error")
	}
}
