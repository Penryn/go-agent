package search

import "testing"

func TestMemoryScopeVisible(t *testing.T) {
	tests := []struct {
		name            string
		scope           string
		groupID, userID int64
		want            bool
	}{
		{name: "global session sees global", scope: "global", want: true},
		{name: "global session rejects group", scope: "group:1", want: false},
		{name: "group sees global", scope: "global", groupID: 1, want: true},
		{name: "group sees own scope", scope: "group:1", groupID: 1, want: true},
		{name: "group rejects foreign scope", scope: "group:2", groupID: 1, want: false},
		{name: "user sees own scope", scope: "group:1:user:7", groupID: 1, userID: 7, want: true},
		{name: "user rejects another user", scope: "group:1:user:8", groupID: 1, userID: 7, want: false},
		{name: "zero user cannot see user scope", scope: "group:1:user:7", groupID: 1, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MemoryScopeVisible(tt.scope, tt.groupID, tt.userID); got != tt.want {
				t.Fatalf("MemoryScopeVisible(%q, %d, %d) = %v, want %v", tt.scope, tt.groupID, tt.userID, got, tt.want)
			}
		})
	}
}
