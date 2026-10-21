package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

type unavailableQuery struct{ searchEmbedder }

func (unavailableQuery) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, embedding.ErrUnavailable
}

func keywordFixture(t *testing.T, s *Store, a domain.AccountID, pid, text string) (*retrieval.VectorSearch, domain.Email) {
	t.Helper()
	e, input := chunkFixture(t, s, a, pid)
	input.Chunks = []repository.ChunkInput{{Index: 0, SectionType: "body", Text: text, TokenCount: 5, EndOffset: len(text)}}
	_, _ = replaceChunks(t, s, input)
	search := retrieval.New(s.Vectors(), unavailableQuery{searchEmbedder{[]float32{.25, .75}}})
	search.Profile.PreprocessingVersion = input.PreprocessingVersion
	search.Profile.IndexVersion = input.IndexVersion
	return search, e
}

func TestKeywordHybridFallbackFiltersAndSourceFences(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	a, b := account(t, s, "keyword-a"), account(t, s, "keyword-b")
	search, e := keywordFixture(t, s, a, "one", "Northstar Labs confirmation N8K2ZP. Contact support@northstar.test.")
	_, _ = keywordFixture(t, s, b, "private", "Northstar Labs confirmation N8K2ZP.")
	_, _ = keywordFixture(t, s, a, "distractor", "Northstar Labs confirmation N8K2ZP-extra. Different code.")
	cat, err := s.Categories().Save(ctx, a, domain.EmailCategory{Name: "Travel", Description: "Travel reservations", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Categories().Assign(ctx, a, e.ID, cat); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"keyword", "hybrid"} {
		for _, q := range []string{"N8K2ZP", "support@northstar.test", `"Northstar Labs"`} {
			got, err := search.SearchMode(ctx, a, q, 10, repository.SearchFilter{}, mode)
			if err != nil || len(got.Emails) == 0 || got.Emails[0].EmailID != string(e.ID) || !got.Emails[0].ExactMatch || got.Mode != "keyword" {
				t.Fatalf("%s %s: %+v %v", mode, q, got, err)
			}
			if mode == "hybrid" && got.Warning != "semantic_unavailable" {
				t.Fatal("fallback not disclosed")
			}
			for _, hit := range got.Emails {
				if hit.Evidence[0].ScoreKind != "keyword" {
					t.Fatal("keyword presented as cosine")
				}
			}
		}
		for _, f := range []repository.SearchFilter{{Sender: "absent%_"}, {After: e.ReceivedAt.UnixMilli() + 1}, {Before: e.ReceivedAt.UnixMilli()}, {CategoryID: "foreign"}} {
			got, err := search.SearchMode(ctx, a, "N8K2ZP", 10, f, mode)
			if err != nil || len(got.Emails) != 0 {
				t.Fatal("filter leaked", f, got, err)
			}
		}
	}
	filtered := repository.SearchFilter{Sender: e.Sender.Address, After: e.ReceivedAt.UnixMilli(), CategoryID: string(cat)}
	got, err := search.SearchMode(ctx, a, "N8K2ZP", 10, filtered, "keyword")
	if err != nil || len(got.Emails) != 1 {
		t.Fatal(got, err)
	}
	if err = s.Categories().Unassign(ctx, a, e.ID, cat); err != nil {
		t.Fatal(err)
	}
	requireError(t, search.CheckSources(ctx, a, filtered, got.Sources), repository.ErrConflict)
	coverage, err := search.Keywords.KeywordCoverage(ctx, a, search.Profile)
	if err != nil || coverage != 2 {
		t.Fatal(coverage, err)
	}
	e.BodyText += " changed"
	saveEmail(t, s, e)
	requireError(t, search.CheckSources(ctx, a, repository.SearchFilter{}, got.Sources), repository.ErrConflict)
	coverage, err = search.Keywords.KeywordCoverage(ctx, a, search.Profile)
	if err != nil || coverage != 1 {
		t.Fatal("stale coverage", coverage, err)
	}
	if _, err = s.db.Exec(`UPDATE emails SET remote_deleted=1 WHERE account_id=?`, a); err != nil {
		t.Fatal(err)
	}
	got, err = search.SearchMode(ctx, a, "N8K2ZP", 10, repository.SearchFilter{}, "keyword")
	if err != nil || len(got.Emails) != 0 {
		t.Fatal("deleted evidence", got, err)
	}
	var remaining int
	if err = s.db.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH 'N8K2ZP'`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatal("FTS tombstone cleanup", remaining, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = search.SearchMode(cancelled, a, "test", 10, repository.SearchFilter{}, "keyword")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestKeywordFTSQuerySafetyPartialAndSnippet(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "fts-safety")
	ctx := context.Background()
	text := strings.Repeat("ordinary prose ", 70) + " confirmation N8K2ZP."
	search, e := keywordFixture(t, s, a, "one", text)
	if _, err := s.db.Exec(`UPDATE index_state SET content_status='partial' WHERE email_id=?`, e.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"N8K2ZP", `N8K2ZP OR NOT " : *`, "***", `"`, "ordinary"} {
		got, err := search.SearchMode(ctx, a, q, 10, repository.SearchFilter{}, "keyword")
		if err != nil {
			t.Fatal("FTS syntax escaped incorrectly", q, err)
		}
		if q == "N8K2ZP" && (len(got.Emails) != 1 || !got.Emails[0].PartialContent || !strings.Contains(got.Emails[0].Evidence[0].Excerpt, q)) {
			t.Fatal("lost partial/keyword passage", got)
		}
	}
	// Triggers update existing passages and remove all FTS postings on purge.
	if _, err := s.db.Exec(`DELETE FROM emails WHERE account_id=?`, a); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH 'N8K2ZP'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestKeywordUpgradeNineteenRebuildsSharedPassages(t *testing.T) {
	old, dir := historicalStore(t, 19)
	a := account(t, old, "upgrade-fts")
	search, _ := keywordFixture(t, old, a, "one", "Migration identifier J4R9QP.")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	search.Vectors = s.Vectors()
	search.Keywords = s.Vectors().(repository.KeywordRepository)
	got, err := search.SearchMode(context.Background(), a, "J4R9QP", 10, repository.SearchFilter{}, "keyword")
	if err != nil || len(got.Emails) != 1 {
		t.Fatal("upgrade FTS backfill", got, err)
	}
}
