package vectorstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run with CHROMA_URL and CHROMA_INTEGRATION_TEST=1 against a disposable
// Chroma collection. It intentionally leaves its uniquely named collection
// intact for inspection after a failure.
func TestChromaIntegration(t *testing.T) {
	if os.Getenv("CHROMA_INTEGRATION_TEST") != "1" {
		t.Skip("set CHROMA_INTEGRATION_TEST=1 to use a live Chroma server")
	}
	name := "kb_it_" + strings.TrimPrefix(strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", ""), "0")
	t.Setenv("CHROMA_COLLECTION", name)
	store, err := NewChromaFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	chroma := store.(*ChromaStore)
	ctx := context.Background()
	defer func() {
		if err := chroma.request(ctx, "DELETE", chroma.rootPath()+"/collections/"+name, nil, nil); err != nil {
			t.Errorf("delete temporary Chroma collection: %v", err)
		}
	}()
	vector := make([]float64, 1024)
	vector[0] = 1
	if err := store.Upsert(ctx, []Chunk{{DocID: "doc-test", DocType: "制度", Filename: "sample.txt", Index: 0, Content: "chroma integration marker", Embedding: vector}}); err != nil {
		t.Fatal(err)
	}
	count, err := store.Count(ctx, []string{"doc-test"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("Count() = %d, want 1", count)
	}
	matches, err := store.Search(ctx, []string{"doc-test"}, vector, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Filename != "sample.txt" || matches[0].Content != "chroma integration marker" {
		t.Fatalf("unexpected search matches: %#v", matches)
	}
	otherDocMatches, err := store.Search(ctx, []string{"doc-other"}, vector, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherDocMatches) != 0 {
		t.Fatalf("document filter leaked results: %#v", otherDocMatches)
	}
	if err := store.DeleteDoc(ctx, "doc-test"); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := store.Count(ctx, []string{"doc-test"})
	if err != nil || afterDelete != 0 {
		t.Fatalf("DeleteDoc left %d chunks (err=%v)", afterDelete, err)
	}
}
