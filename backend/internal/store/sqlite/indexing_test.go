package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func indexStoreFixture(t *testing.T) (*Store, repository.IndexRun, repository.ChunkSnapshot, []repository.EmailChunk) {
	t.Helper()
	s := testStore(t)
	a := account(t, s, "local-index")
	if _, err := s.db.Exec(`UPDATE accounts SET provider='gmail' WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	_, input := chunkFixture(t, s, a, "source")
	p := repository.IndexProfile{Model: input.Model, PreprocessingVersion: input.PreprocessingVersion, IndexVersion: input.IndexVersion, Fingerprint: hashText("recipe")}
	run, err := s.Indexing().Begin(context.Background(), a, p, false)
	if err != nil {
		t.Fatal(err)
	}
	state, chunks := replaceChunks(t, s, input)
	_ = state
	return s, run, input, chunks
}
func TestIndexJobFencesPauseCancelRestartAndModelChange(t *testing.T) {
	for _, transition := range []string{"paused", "cancelled", "restart", "model"} {
		t.Run(transition, func(t *testing.T) {
			s, run, input, chunks := indexStoreFixture(t)
			ctx := context.Background()
			state, err := s.Chunks().State(ctx, run.AccountID, input.EmailID)
			if err != nil {
				t.Fatal(err)
			}
			batch := repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}}}
			switch transition {
			case "restart":
				err = s.Indexing().Recover(ctx)
			case "model":
				p := run.Profile
				p.Model.Version = "new-version"
				_, err = s.Indexing().Begin(ctx, run.AccountID, p, false)
			default:
				err = s.Indexing().Transition(ctx, run, transition, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Indexing().SaveIndexed(ctx, run, batch); err == nil {
				t.Fatal("revoked run published vectors")
			}
			if _, err = s.Indexing().ReplaceIndexed(ctx, run, input); err == nil {
				t.Fatal("revoked run replaced chunks")
			}
			if count(t, s, "chunk_embeddings") != 2 {
				t.Fatal("revoked run changed cached rows")
			}
			var ready int
			if err = s.db.QueryRow(`SELECT COUNT(*) FROM chunk_embeddings WHERE status='ready'`).Scan(&ready); err != nil || ready != 0 {
				t.Fatal("revoked vector saved")
			}
		})
	}
}
func TestIndexJobPurgeAndTombstoneCannotRestoreData(t *testing.T) {
	for _, tombstone := range []bool{false, true} {
		t.Run(map[bool]string{false: "purge", true: "tombstone"}[tombstone], func(t *testing.T) {
			s, run, input, chunks := indexStoreFixture(t)
			ctx := context.Background()
			state, _ := s.Chunks().State(ctx, run.AccountID, input.EmailID)
			source, err := s.Emails().Get(ctx, run.AccountID, input.EmailID)
			if err != nil {
				t.Fatal(err)
			}
			if tombstone {
				if _, err := s.db.Exec(`UPDATE emails SET remote_deleted=1 WHERE account_id=? AND id=?`, run.AccountID, input.EmailID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec(`DELETE FROM emails WHERE account_id=? AND id=?`, run.AccountID, input.EmailID); err != nil {
					t.Fatal(err)
				}
			}
			job, err := s.Indexing().Run(ctx, run.AccountID)
			if err != nil || job.Status != "paused" || job.Generation == run.Generation {
				t.Fatal("purge did not revoke job")
			}
			if _, err = s.Indexing().ReplaceIndexed(ctx, run, input); err == nil {
				t.Fatal("purged chunks recreated")
			}
			if _, err = s.Indexing().SaveIndexed(ctx, run, repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}}}); err == nil {
				t.Fatal("purged vectors restored")
			}
			if count(t, s, "email_chunks") != 0 || count(t, s, "chunk_embeddings") != 0 || count(t, s, "index_state") != 0 {
				t.Fatal("derived data resurrected")
			}
			// Reimport the exact same source/ID: the revoked job still cannot restore it.
			if _, err = s.db.Exec(`UPDATE emails SET remote_deleted=0 WHERE account_id=? AND id=?`, run.AccountID, input.EmailID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Emails().Upsert(ctx, source.Email); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Indexing().ReplaceIndexed(ctx, run, input); err == nil {
				t.Fatal("purged job revived after identical reimport")
			}
			if count(t, s, "email_chunks") != 0 {
				t.Fatal("late job restored reimported chunks")
			}

		})
	}
}
func TestIndexJobIsolationAtomicBatchesAndCompletion(t *testing.T) {
	s, run, input, chunks := indexStoreFixture(t)
	ctx := context.Background()
	other := account(t, s, "other-index")
	_, otherInput := chunkFixture(t, s, other, "other-source")
	if _, err := s.Indexing().ReplaceIndexed(ctx, run, otherInput); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("cross-account replacement", err)
	}
	state, _ := s.Chunks().State(ctx, run.AccountID, input.EmailID)
	batch := repository.EmbeddingBatch{State: state, Embeddings: []repository.ChunkEmbedding{{ChunkID: chunks[0].ID, Status: "ready", Vector: []float32{1, 0}}, {ChunkID: "unrelated", Status: "ready", Vector: []float32{1, 0}}}}
	if _, err := s.Indexing().SaveIndexed(ctx, run, batch); err == nil {
		t.Fatal("mixed batch accepted")
	}
	vector, err := s.Chunks().Embedding(ctx, run.AccountID, input.EmailID, chunks[0].ID, run.Profile.Model)
	if err != nil || vector.Status != "pending" {
		t.Fatal("partial transaction leaked", err)
	}
	if err = s.Indexing().Transition(ctx, run, "complete", ""); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("incomplete index published", err)
	}
	readyChunks(t, s, state, chunks)
	if err = s.Indexing().Transition(ctx, run, "complete", ""); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Indexing().Progress(ctx, run.AccountID, run.Profile)
	if err != nil || counts.Eligible != 1 || counts.Complete != 1 || counts.Embeddings != 2 {
		t.Fatal("account counts", counts, err)
	}
	p := run.Profile
	p.Fingerprint = hashText("changed without version bump")
	if _, err = s.Indexing().Begin(ctx, run.AccountID, p, false); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("same version reinterpreted fingerprint", err)
	}
	incompatible, err := s.Indexing().Progress(ctx, run.AccountID, p)
	if err != nil || incompatible.Indexed != 0 || incompatible.Pending != 1 || incompatible.Embeddings != 0 {
		t.Fatal("fingerprint-incompatible cache counted as ready", incompatible, err)
	}
	nextProfile := run.Profile
	nextProfile.Model.Version = "second-version"
	if _, err = s.Indexing().Begin(ctx, run.AccountID, nextProfile, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Indexing().Begin(ctx, run.AccountID, p, false); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("A/B/A model identity changed", err)
	}
}
func TestIndexJobUpgradeSixteenPreservesVectorsAndJournals(t *testing.T) {
	ctx := context.Background()
	old, dir := historicalStore(t, 16)
	a := account(t, old, "index-upgrade")
	plan := draftPlan(t, old, a)
	importHistoricalDraft(t, old, plan, "uncertain")
	_, input := chunkFixture(t, old, a, "ready-source")
	state, chunks := replaceChunks(t, old, input)
	state = readyChunks(t, old, state, chunks)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.SchemaVersion() != 18 || count(t, s, "local_index_jobs") != 0 || count(t, s, "chunk_embeddings") != 2 {
		t.Fatal("upgrade changed existing data")
	}
	after, err := s.Chunks().State(ctx, a, input.EmailID)
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatal("old vector state changed", err)
	}
	journal, err := s.Replies().Get(ctx, a, plan.ID)
	if err != nil || journal.Plan.Digest != plan.Digest || journal.State != "uncertain" {
		t.Fatal("historical recovery changed", err)
	}
	if _, err = s.Indexing().Begin(ctx, domain.AccountID("missing"), repository.IndexProfile{Model: input.Model, PreprocessingVersion: input.PreprocessingVersion, IndexVersion: input.IndexVersion, Fingerprint: hashText("x")}, false); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("missing account accepted", err)
	}
}

func TestIndexLegacySourceHashIsUnavailableWithoutRewritingMail(t *testing.T) {
	s, run, input, _ := indexStoreFixture(t)
	ctx := context.Background()
	for _, table := range []string{"index_state", "email_chunks"} {
		if _, err := s.db.Exec(`DELETE FROM `+table+` WHERE account_id=?`, run.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE emails SET content_hash='historical-source-hash' WHERE account_id=? AND id=?`, run.AccountID, input.EmailID); err != nil {
		t.Fatal(err)
	}
	next, err := s.Indexing().Next(ctx, run)
	if err != nil || len(next) != 0 {
		t.Fatal("legacy source was scheduled", err)
	}
	progress, err := s.Indexing().Progress(ctx, run.AccountID, run.Profile)
	if err != nil || progress.Eligible != 1 || progress.Unavailable != 1 || progress.Indexed != 0 {
		t.Fatal("legacy source falsely covered", progress, err)
	}
	if err = s.Indexing().Transition(ctx, run, "complete", ""); err != nil {
		t.Fatal(err)
	}
	job, err := s.Indexing().Run(ctx, run.AccountID)
	if err != nil || job.Status != "partial" {
		t.Fatal("legacy coverage claimed complete")
	}
	stored, err := s.Emails().Get(ctx, run.AccountID, input.EmailID)
	if err != nil || stored.ContentHash != "historical-source-hash" || stored.Email.BodyText == "" {
		t.Fatal("historical source changed", err)
	}
}

func TestIndexCandidatesHaveNoVisiblePageOrTenThousandMessageCap(t *testing.T) {
	s, run, _, _ := indexStoreFixture(t)
	ctx := context.Background()
	p := run.Profile
	// All early rows are already processed (no usable text); only row 10,001 needs work.
	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (VALUES(0) UNION ALL SELECT v+1 FROM n WHERE v<10000)
 INSERT INTO emails(id,account_id,provider_message_id,provider_thread_id,subject,body_text,received_at,headers_json,attachments_json,content_hash,created_at,updated_at,content_revision,body_state)
 SELECT printf('scale-%05d',v),?,printf('scale-provider-%05d',v),'','','',0,'[]','[]',?,0,0,'','complete' FROM n`, run.AccountID, hashText("synthetic-empty-source")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO index_state(account_id,email_id,source_revision,preprocessing_version,index_version,model_id,model_version,dimension,generation,body_state,status,content_status)
 SELECT account_id,id,content_hash,?,?,?,?,?,'synthetic','complete','unavailable','unavailable' FROM emails WHERE account_id=? AND id LIKE 'scale-%' AND id<>'scale-10000'`, p.PreprocessingVersion, p.IndexVersion, p.Model.ID, p.Model.Version, p.Model.Dimension, run.AccountID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Indexing().Next(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rows {
		if c.EmailID == "scale-10000" {
			found = true
		}
	}
	if !found {
		t.Fatal("eligible message after 10,000 cached rows was skipped")
	}
	counts, err := s.Indexing().Progress(ctx, run.AccountID, p)
	if err != nil || counts.Eligible != 10002 || counts.Unavailable != 10000 {
		t.Fatal("cache counts capped", counts, err)
	}
}
