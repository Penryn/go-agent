package search

import "fmt"

// MemoryScopeVisible is the shared visibility rule for stores and retrieval.
func MemoryScopeVisible(scope string, groupID, userID int64) bool {
	if groupID == 0 {
		return scope == "global"
	}
	if scope == "global" || scope == fmt.Sprintf("group:%d", groupID) {
		return true
	}
	return userID != 0 && scope == fmt.Sprintf("group:%d:user:%d", groupID, userID)
}
