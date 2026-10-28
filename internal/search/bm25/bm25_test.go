package bm25

import "testing"

func TestRankFindsChinesePhraseAndExactEnglishTerm(t *testing.T) {
	results := Rank("旧梗", []Document{
		{ID: "old", Text: "这个群爱聊旧梗"},
		{ID: "new", Text: "这个群爱聊新话题"},
	}, 2)
	if len(results) != 1 || results[0].ID != "old" || results[0].Score <= 0 {
		t.Fatalf("unexpected Chinese BM25 results: %+v", results)
	}

	results = Rank("release", []Document{
		{ID: "release", Text: "release checklist"},
		{ID: "other", Text: "deployment notes"},
	}, 2)
	if len(results) != 1 || results[0].ID != "release" {
		t.Fatalf("unexpected English BM25 results: %+v", results)
	}
}

func TestIndexIsReusableAcrossQueries(t *testing.T) {
	index := NewIndex([]Document{
		{ID: "old", Text: "这个群爱聊旧梗"},
		{ID: "new", Text: "这个群爱聊新话题"},
		{ID: "release", Text: "release checklist"},
	})
	for _, test := range []struct {
		query string
		want  string
	}{
		{query: "旧梗", want: "old"},
		{query: "新话题", want: "new"},
		{query: "release", want: "release"},
	} {
		results := index.Rank(test.query, 1)
		if len(results) != 1 || results[0].ID != test.want {
			t.Fatalf("query %q returned %+v", test.query, results)
		}
	}
}
