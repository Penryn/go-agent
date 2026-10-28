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

func RecallKey(groupID int64, actorVersion, memoryRevision uint64, query string) string {
	return fmt.Sprintf("recall:%d:%d:%d:%s", groupID, actorVersion, memoryRevision, TextHash(query))
}

func TopicSnapshotKey(groupID int64, actorVersion uint64) string {
	return fmt.Sprintf("topic-snapshot:%d:%d", groupID, actorVersion)
}

func StickerKey(groupID, assetRevision int64, cooldownEpoch int64, query string) string {
	return fmt.Sprintf("stickers:%d:%d:%d:%s", groupID, assetRevision, cooldownEpoch, TextHash(query))
}
