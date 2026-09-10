package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	memorydomain "github.com/phlin/go-agent/internal/domain/memory"
)

// GetRelevantMemories 实现检索功能（集成到 service）
func (s *service) GetRelevantMemories(ctx context.Context, req *memorydomain.RetrievalRequest) ([]*memorydomain.MemoryWithSource, error) {
	if req.Limit <= 0 {
		req.Limit = 10
	}

	// 如果有 retrieval service，使用它
	// 当前简化实现：直接从 store 查询
	results, err := s.searchMemoriesFromStore(ctx, req)
	if err != nil {
		return nil, err
	}

	return results, nil
}

// searchMemoriesFromStore 从 store 直接查询记忆
func (s *service) searchMemoriesFromStore(ctx context.Context, req *memorydomain.RetrievalRequest) ([]*memorydomain.MemoryWithSource, error) {
	// 1. 查询所有相关主体的记忆
	allMemories := make([]*memorydomain.Memory, 0)

	// 如果指定了目标 ID，查询这些用户的记忆
	if len(req.TargetIDs) > 0 {
		for _, targetID := range req.TargetIDs {
			statuses := []memorydomain.MemoryStatus{memorydomain.MemoryStatusActive}
			if !req.ExcludeExpired {
				statuses = append(statuses, memorydomain.MemoryStatusPending)
			}

			memories, err := s.store.ListBySubject(ctx, req.Scope, memorydomain.SubjectKindUser, targetID, statuses)
			if err != nil {
				return nil, fmt.Errorf("failed to list memories for %s: %w", targetID, err)
			}
			allMemories = append(allMemories, memories...)
		}
	}

	// 2. 查询群级记忆
	groupMemories, err := s.store.ListBySubject(ctx, req.Scope, memorydomain.SubjectKindGroup, req.Scope, []memorydomain.MemoryStatus{memorydomain.MemoryStatusActive})
	if err != nil {
		return nil, fmt.Errorf("failed to list group memories: %w", err)
	}
	allMemories = append(allMemories, groupMemories...)

	// 3. 过滤和排序
	filtered := s.filterMemories(allMemories, req)
	scored := s.scoreMemories(filtered, req.Query)

	// 4. 限制数量
	if len(scored) > req.Limit {
		scored = scored[:req.Limit]
	}

	// 5. 转换为 MemoryWithSource
	results := make([]*memorydomain.MemoryWithSource, 0, len(scored))
	for _, mem := range scored {
		evidence, err := s.store.GetEvidence(ctx, mem.MemoryID)
		if err != nil {
			continue // 跳过无法获取证据的记忆
		}

		eventIDs := make([]string, 0, len(evidence))
		for _, ev := range evidence {
			eventIDs = append(eventIDs, ev.EventID)
		}

		sourceSummary := fmt.Sprintf("%d pieces of evidence", len(evidence))
		if mem.FirstObservedAt.Equal(mem.LastObservedAt) {
			sourceSummary += fmt.Sprintf(" from %s", mem.FirstObservedAt.Format("2006-01-02"))
		} else {
			sourceSummary += fmt.Sprintf(" from %s to %s",
				mem.FirstObservedAt.Format("2006-01-02"),
				mem.LastObservedAt.Format("2006-01-02"))
		}

		results = append(results, &memorydomain.MemoryWithSource{
			Memory:           *mem,
			EvidenceCount:    len(evidence),
			EvidenceEventIDs: eventIDs,
			SourceSummary:    sourceSummary,
		})
	}

	return results, nil
}

// filterMemories 过滤记忆
func (s *service) filterMemories(memories []*memorydomain.Memory, req *memorydomain.RetrievalRequest) []*memorydomain.Memory {
	filtered := make([]*memorydomain.Memory, 0, len(memories))

	for _, mem := range memories {
		// 检查状态
		if !mem.IsActive() {
			continue
		}

		// 类型过滤
		if len(req.IncludeTypes) > 0 {
			found := false
			for _, t := range req.IncludeTypes {
				if mem.Type == t {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		filtered = append(filtered, mem)
	}

	return filtered
}

// scoreMemories 为记忆打分并排序（简单文本相似度）
func (s *service) scoreMemories(memories []*memorydomain.Memory, query string) []*memorydomain.Memory {
	type scoredMemory struct {
		memory *memorydomain.Memory
		score  float64
	}

	query = strings.ToLower(query)
	queryWords := strings.Fields(query)

	scored := make([]scoredMemory, 0, len(memories))
	for _, mem := range memories {
		score := 0.0
		content := strings.ToLower(mem.Content)

		// 简单的词频匹配
		for _, word := range queryWords {
			if strings.Contains(content, word) {
				score += 1.0
			}
		}

		// 加入时间因素（越新越好）
		daysSinceObserved := float64(mem.LastObservedAt.Unix()) / 86400.0
		recencyBoost := 1.0 / (1.0 + daysSinceObserved/365.0)
		score += recencyBoost * 0.1

		scored = append(scored, scoredMemory{memory: mem, score: score})
	}

	// 按分数排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	result := make([]*memorydomain.Memory, len(scored))
	for i, s := range scored {
		result[i] = s.memory
	}

	return result
}
