package vectorstore

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New selects the configured vector provider. A provider-specific adapter can
// be added here without changing the indexing or retrieval handlers.
func New(provider string, db *pgxpool.Pool) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "pgvector":
		if db == nil {
			return nil, fmt.Errorf("pgvector requires a PostgreSQL connection")
		}
		return NewPGVector(db), nil
	case "chroma":
		return NewChromaFromEnv()
	default:
		return nil, fmt.Errorf("unknown vector provider %q", provider)
	}
}

// embeddingDim 返回配置的向量维度，需与索引模型输出一致。
func embeddingDim() int {
	if v, err := strconv.Atoi(os.Getenv("EMBED_DIMENSIONS")); err == nil && v > 0 {
		return v
	}
	return 1024
}
