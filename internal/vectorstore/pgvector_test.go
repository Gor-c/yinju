package vectorstore

import "testing"

func TestVectorLiteral(t *testing.T) {
	got := vectorLiteral([]float64{1, -0.5, 0.000001})
	want := "[1,-0.5,0.000001]"
	if got != want {
		t.Fatalf("vectorLiteral() = %q, want %q", got, want)
	}
	if got := vectorLiteral(nil); got != "[]" {
		t.Fatalf("vectorLiteral(nil) = %q", got)
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	if _, err := New("milvus", nil); err == nil {
		t.Fatal("expected unknown provider error")
	}
	if _, err := New("pgvector", nil); err == nil {
		t.Fatal("expected pgvector without database to fail")
	}
}
