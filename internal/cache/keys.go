package cache

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

func TextHash(text string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

// EventContextHash covers the per-turn inputs that change recall results
// independently of the query text: visible speakers and topic IDs. Using it
// instead of the per-event actor version lets identical queries reuse the
// cache across unrelated messages.
func EventContextHash(userIDs []int64, topicIDs []string) string {
	users := append([]int64(nil), userIDs...)
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	topics := append([]string(nil), topicIDs...)
	sort.Strings(topics)
	h := sha256.New()
	lastUser := int64(0)
	for i, id := range users {
		if i > 0 && id == lastUser {
			continue
		}
		fmt.Fprintf(h, "u%d,", id)
		lastUser = id
	}
	h.Write([]byte{'|'})
	for i, id := range topics {
		if id == "" || i > 0 && id == topics[i-1] {
			continue
		}
		h.Write([]byte(id))
		h.Write([]byte{','})
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

func EmbeddingKey(modelVersion, text string) string {
	return fmt.Sprintf("embed:v3:%s:%s", modelVersion, TextHash(text))
}

func NotesIndexKey(groupID, memoryRevision int64) string {
	return fmt.Sprintf("notes-index:%d:%d", groupID, memoryRevision)
}

func RecallKey(groupID, memoryRevision int64, modelVersion, query, contextHash string) string {
	return fmt.Sprintf("recall:%d:%d:%s:%s:%s", groupID, memoryRevision, modelVersion, TextHash(query), contextHash)
}

func StickerKey(groupID int64, assetRevision, cooldownEpoch int64, modelVersion, query string) string {
	return fmt.Sprintf("stickers:%d:%d:%d:%s:%s", groupID, assetRevision, cooldownEpoch, modelVersion, TextHash(query))
}

func ContextKey(groupID, memoryRevision, topicRevision, assetRevision, cooldownEpoch int64, modelVersion, query, contextHash string) string {
	return fmt.Sprintf("context:%d:%d:%d:%d:%d:%s:%s:%s", groupID, memoryRevision, topicRevision, assetRevision, cooldownEpoch, modelVersion, TextHash(query), contextHash)
}

// ImageKey caches the normalized data URL, not the raw download response.
func ImageKey(url, mediaVersion string) string {
	return fmt.Sprintf("image:%s:%s", mediaVersion, TextHash(url))
}
