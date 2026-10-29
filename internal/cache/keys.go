package cache

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

func TextHash(text string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

func EmbeddingKey(modelVersion, text string) string {
	return fmt.Sprintf("embed:v3:%s:%s", modelVersion, TextHash(text))
}

func NotesIndexKey(groupID, memoryRevision int64) string {
	return fmt.Sprintf("notes-index:%d:%d", groupID, memoryRevision)
}

func RecallKey(groupID int64, actorVersion uint64, memoryRevision int64, modelVersion, query string) string {
	return fmt.Sprintf("recall:%d:%d:%d:%s:%s", groupID, actorVersion, memoryRevision, modelVersion, TextHash(query))
}

func TopicSnapshotKey(groupID int64, actorVersion uint64, topicRevision int64) string {
	return fmt.Sprintf("topic-snapshot:%d:%d:%d", groupID, actorVersion, topicRevision)
}

func StickerKey(groupID int64, assetRevision, cooldownEpoch int64, modelVersion, query string) string {
	return fmt.Sprintf("stickers:%d:%d:%d:%s:%s", groupID, assetRevision, cooldownEpoch, modelVersion, TextHash(query))
}

func ContextKey(groupID int64, actorVersion uint64, memoryRevision, topicRevision, assetRevision int64) string {
	return fmt.Sprintf("context:%d:%d:%d:%d:%d", groupID, actorVersion, memoryRevision, topicRevision, assetRevision)
}

// ImageKey caches the normalized data URL, not the raw download response.
func ImageKey(url, mediaVersion string) string {
	return fmt.Sprintf("image:%s:%s", mediaVersion, TextHash(url))
}
