package sqlite

import (
	"context"
	"fmt"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/search"
	"strings"
	"testing"
)

type profileEncoder struct{}

func (profileEncoder) ProviderName() string { return "local-scan-profile-384" }
func (profileEncoder) Dimension() int       { return 384 }
func (profileEncoder) EmbedEmails(context.Context, domain.AccountID, []domain.EmailID) ([][]float32, error) {
	return nil, embedding.ErrUnavailable
}
func (profileEncoder) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	values := make([][]float32, len(texts))
	for i := range values {
		values[i] = make([]float32, 384)
		for j := range values[i] {
			values[i][j] = float32(j+1) / 384
		}
	}
	return values, nil
}
func BenchmarkPassageExactScan1000(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	a, err := s.Accounts().Upsert(ctx, domain.Account{ID: "profile", Provider: "fixture", ProviderAccountID: "profile"})
	if err != nil {
		b.Fatal(err)
	}
	encoder := &search.PassageEmbeddings{Base: profileEncoder{}, Emails: s.Emails(), Vectors: s.PassageVectors()}
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("mail-%04d", i)
		e := retrievalEmail(a, id, "Synthetic operations report", strings.Repeat("Administrative notes about the schedule and planning. ", 20)+fmt.Sprintf("Final action item %d.", i), "profile@example.test", 10)
		if _, err := s.Emails().Upsert(ctx, e); err != nil {
			b.Fatal(err)
		}
		if _, err := encoder.EmbedEmails(ctx, a, []domain.EmailID{e.ID}); err != nil {
			b.Fatal(err)
		}
	}
	service := search.New(s.SearchIndex(), encoder, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := service.Query(ctx, a, "schedule", 3, search.Filters{})
		if err != nil || r.Scanned != 1000 || len(r.Hits) != 3 {
			b.Fatal("scan failed", err)
		}
	}
}
