package search

import "sort"

// Fuse combines rankings without comparing scores from different retrieval domains.
func Fuse(limit int, tracks ...[]string) []string {
	scores := map[string]float64{}
	order := map[string]int{}
	for _, track := range tracks {
		seen := map[string]bool{}
		for rank, id := range track {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if _, ok := order[id]; !ok {
				order[id] = len(order)
			}
			scores[id] += 1 / (60 + float64(rank+1))
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return order[ids[i]] < order[ids[j]]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return ids
}
