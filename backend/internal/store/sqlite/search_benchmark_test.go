package sqlite

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"mailpilot.local/core/internal/retrieval"
)

// BenchmarkVectorSearch50K uses 10,000 synthetic cached messages and 50,000
// SQLite float32/384 vectors. Query inference is a fixed vector: this isolates
// exact retrieval/storage cost. Native inference latency is measured separately.
func BenchmarkVectorSearch50K(b *testing.B) { benchmarkSearch50K(b, "semantic") }

func BenchmarkSearchModes50K(b *testing.B) {
	for _, mode := range []string{"keyword", "semantic", "hybrid"} {
		b.Run(mode, func(b *testing.B) { benchmarkSearch50K(b, mode) })
	}
}
func benchmarkSearch50K(b *testing.B, mode string) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	a := domain.AccountID("benchmark")
	if _, err = s.Accounts().Upsert(ctx, domain.Account{ID: a, Provider: "gmail", ProviderAccountID: "benchmark"}); err != nil {
		b.Fatal(err)
	}
	e := domain.Email{ID: "seed", AccountID: a, ProviderID: "seed", Subject: "Synthetic performance fixture", BodyText: "Five distinct evidence passages.", BodyState: "complete", ReceivedAt: time.Unix(1700000000, 0), Sender: domain.EmailAddress{Address: "bench@example.test"}}
	if _, err = s.Emails().Upsert(ctx, e); err != nil {
		b.Fatal(err)
	}
	v := make([]float32, 384)
	for i := range v {
		v[i] = float32(i%7 + 1)
	}
	search := retrieval.New(s.Vectors(), searchEmbedder{v})
	search.Profile.PreprocessingVersion = "benchmark-v1"
	search.Profile.IndexVersion = "chunks-v1"
	p := search.Profile
	blob, err := encodeVector(v, 384)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		b.Helper()
		if _, e := tx.ExecContext(ctx, q, args...); e != nil {
			b.Fatal(e)
		}
	}
	seq := `WITH RECURSIVE seq(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM seq WHERE n<9999) `
	exec(seq + `INSERT INTO emails(id,account_id,provider_message_id,provider_thread_id,subject,body_text,body_html,received_at,headers_json,attachments_json,content_hash,created_at,updated_at,content_revision,body_state)
 SELECT printf('email-%05d',n),account_id,printf('provider-%05d',n),'',subject,body_text,NULL,received_at,headers_json,attachments_json,content_hash,created_at,updated_at,content_revision,body_state FROM seq CROSS JOIN emails WHERE id='seed'`)
	exec(`INSERT INTO email_addresses(email_id,role,position,name,address) SELECT id,'sender',0,'Synthetic','bench@example.test' FROM emails WHERE account_id=? AND id<>'seed'`, a)
	for i := 0; i < 5; i++ {
		text := fmt.Sprintf("Synthetic distinct evidence passage %d.", i)
		exec(`INSERT INTO email_chunks(id,account_id,email_id,chunk_index,section_type,text,token_count,source_revision,start_offset,end_offset,content_hash,preprocessing_version) SELECT id||?,account_id,id,?,'body',?,8,content_hash,?,?,?,? FROM emails WHERE account_id=? AND id<>'seed'`, fmt.Sprintf("-chunk%d", i), i, text, i*100, i*100+len(text), hashText(text), p.PreprocessingVersion, a)
	}
	exec(`INSERT INTO chunk_embeddings(account_id,chunk_id,model_id,model_version,dimension,vector_data,status) SELECT account_id,id,?,?,384,?,'ready' FROM email_chunks WHERE account_id=?`, p.Model.ID, p.Model.Version, blob, a)
	exec(`INSERT INTO index_state(account_id,email_id,source_revision,preprocessing_version,index_version,model_id,model_version,dimension,generation,body_state,status,last_indexed_at,content_status) SELECT account_id,id,content_hash,?,?,?,?,384,'generation','complete','ready',1,'complete' FROM emails WHERE account_id=? AND id<>'seed'`, p.PreprocessingVersion, p.IndexVersion, p.Model.ID, p.Model.Version, a)
	exec(`INSERT INTO local_index_models VALUES(?,?,?,?,?)`, a, p.Model.ID, p.Model.Version, 384, p.Fingerprint)
	exec(`DELETE FROM emails WHERE id='seed'`)
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	q, args := vectorQuery(a, p, repository.SearchFilter{})
	plan, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT c.id,v.vector_data`+q+` ORDER BY c.email_id,c.chunk_index,c.id`, args...)
	if err != nil {
		b.Fatal(err)
	}
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
			b.Fatal(err)
		}
		b.Log(detail)
	}
	if err = plan.Close(); err != nil {
		b.Fatal(err)
	}
	var size int64
	var pages, pageSize int64
	if err = s.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		b.Fatal(err)
	}
	if err = s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		b.Fatal(err)
	}
	size = pages * pageSize
	samples := make([]float64, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		r, e := search.SearchMode(ctx, a, "synthetic benchmark query", 10, repository.SearchFilter{}, mode)
		wantScanned := 50000
		if mode == "hybrid" {
			wantScanned = 100000
		}
		if e != nil || r.Scanned != wantScanned || len(r.Emails) != 10 {
			b.Fatalf("invalid benchmark: chunks %d emails %d %v", r.Scanned, len(r.Emails), e)
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
	}
	b.StopTimer()
	sort.Float64s(samples)
	b.ReportMetric(samples[len(samples)/2], "p50-ms")
	b.ReportMetric(samples[(len(samples)*95-1)/100], "p95-ms")
	b.ReportMetric(50000, "chunks")
	b.ReportMetric(float64(size), "db-bytes")
}
