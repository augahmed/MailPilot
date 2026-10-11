package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/credentials"
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/store/sqlite"
)

// Memory tokens exist ONLY in tests; production always uses the OS keyring.
type memoryStore struct {
	mu      sync.Mutex
	record  credentials.Record
	failure error
	saves   int
}

func (s *memoryStore) Load(ctx context.Context) (credentials.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return credentials.Record{}, s.failure
	}
	if s.record.RefreshToken.Empty() {
		return credentials.Record{}, credentials.ErrNotFound
	}
	return s.record, nil
}
func (s *memoryStore) Save(ctx context.Context, r credentials.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	s.record = r
	s.saves++
	return nil
}
func (s *memoryStore) Delete(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	s.record = credentials.Record{}
	return nil
}

type fakeProvider struct {
	mu         sync.Mutex
	refreshes  int
	exchanges  int
	refreshErr error
	revokeErr  error
	rotated    bool
	block      chan struct{}
}

func (*fakeProvider) Configured() bool { return true }
func (*fakeProvider) AuthorizationURL(redirect, state, verifier string) string {
	return "https://example.invalid/auth?" + url.Values{"redirect_uri": {redirect}, "state": {state}}.Encode()
}
func (p *fakeProvider) Exchange(context.Context, string, string, string) (Grant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exchanges++
	return Grant{credentials.NewSecret("test-access"), credentials.NewSecret("test-refresh"), time.Now().Add(time.Hour)}, nil
}
func (p *fakeProvider) Refresh(ctx context.Context, refresh credentials.Secret) (Grant, error) {
	p.mu.Lock()
	p.refreshes++
	err := p.refreshErr
	rotated := p.rotated
	block := p.block
	p.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return Grant{}, ctx.Err()
		case <-block:
		}
	}
	if err != nil {
		return Grant{}, err
	}
	if rotated {
		refresh = credentials.NewSecret("rotated-refresh")
	}
	return Grant{credentials.NewSecret("renewed-access"), refresh, time.Now().Add(time.Hour)}, nil
}
func (*fakeProvider) Email(context.Context, credentials.Secret) (string, error) {
	return "reader@example.test", nil
}
func (p *fakeProvider) Revoke(context.Context, credentials.Secret) error { return p.revokeErr }
func managerFixture(t *testing.T) (*Manager, *memoryStore, *fakeProvider, *sqlite.Store) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	provider := &fakeProvider{}
	m := NewManager(context.Background(), provider, store, db.Accounts(), "test-keyring-ref")
	t.Cleanup(func() { m.Close(); m.opMu.Lock(); m.opMu.Unlock(); _ = db.Close() })
	return m, store, provider, db
}
func waitStatus(t *testing.T, m *Manager, state string) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := m.Status()
		if s.State == state {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted %s, got %+v", state, m.Status())
	return Status{}
}
func deliver(t *testing.T, begin BeginResult, extra url.Values) int {
	t.Helper()
	u, err := url.Parse(begin.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if extra.Get("state") == "" {
		extra.Set("state", q.Get("state"))
	}
	resp, err := http.Get(q.Get("redirect_uri") + "?" + extra.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
func signIn(t *testing.T, m *Manager) {
	t.Helper()
	begin := m.Begin()
	if begin.Status.State != "connecting" || begin.AuthorizationURL == "" {
		t.Fatalf("invalid begin: %+v", begin.Status)
	}
	if deliver(t, begin, url.Values{"code": {"synthetic-code"}}) != 200 {
		t.Fatal("callback failed")
	}
	waitStatus(t, m, "connected")
}
func TestConnectDisconnectAndPersistedIdentity(t *testing.T) {
	m, store, _, db := managerFixture(t)
	signIn(t, m)
	record, err := store.Load(context.Background())
	if err != nil || record.AccountID == "" {
		t.Fatal("refresh credential not persisted")
	}
	account, err := db.Accounts().Get(context.Background(), domain.AccountID(record.AccountID))
	if err != nil || account.CredentialRef != "test-keyring-ref" {
		t.Fatal("account not linked")
	}
	if m.Begin().AuthorizationURL != "" {
		t.Fatal("connected session replaced")
	}
	m.Disconnect()
	waitStatus(t, m, "disconnected")
	if _, err = store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("token retained")
	}
	account, err = db.Accounts().Get(context.Background(), account.ID)
	if err != nil || account.CredentialRef != "" {
		t.Fatal("reference retained")
	}
}
func TestDeniedConsentAndInvalidState(t *testing.T) {
	m, store, p, _ := managerFixture(t)
	begin := m.Begin()
	if deliver(t, begin, url.Values{"state": {"wrong"}, "code": {"evil"}}) != 400 {
		t.Fatal("invalid state accepted")
	}
	if m.Status().State != "connecting" {
		t.Fatal("invalid callback consumed session")
	}
	deliver(t, begin, url.Values{"error": {"access_denied"}})
	if s := waitStatus(t, m, "error"); s.Code != "consent_denied" {
		t.Fatal(s)
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("credential created after denial")
	}
	if p.exchanges != 0 {
		t.Fatal("code exchange after denial")
	}
}
func TestRefreshRotationAndConcurrentChecks(t *testing.T) {
	m, store, p, _ := managerFixture(t)
	signIn(t, m)
	p.mu.Lock()
	p.rotated = true
	p.mu.Unlock()
	m.mu.Lock()
	m.grant.Expiry = time.Now().Add(-time.Second)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); m.Status() }()
	}
	wg.Wait()
	waitStatus(t, m, "connected")
	record, _ := store.Load(context.Background())
	if record.RefreshToken.Reveal() != "rotated-refresh" {
		t.Fatal("rotation not saved")
	}
	p.mu.Lock()
	count := p.refreshes
	p.mu.Unlock()
	if count != 1 {
		t.Fatalf("refresh count %d", count)
	}
}
func TestInvalidGrantRequiresReconnectWithoutClaimingConnected(t *testing.T) {
	m, _, p, _ := managerFixture(t)
	signIn(t, m)
	p.mu.Lock()
	p.refreshErr = ErrExpired
	p.mu.Unlock()
	m.Check()
	s := waitStatus(t, m, "reauth_required")
	if s.Email != "" {
		t.Fatal("stale connected identity")
	}
}
func TestStoreFailureFailsClosed(t *testing.T) {
	m, store, p, _ := managerFixture(t)
	store.failure = credentials.ErrUnavailable
	begin := m.Begin()
	if begin.AuthorizationURL == "" {
		t.Fatal("session not created")
	}
	s := waitStatus(t, m, "error")
	if s.Code != "credential_store_unavailable" || p.exchanges != 0 {
		t.Fatal("did not fail closed")
	}
}
func TestDisconnectCancelsRefreshAndCannotResurrectCredentials(t *testing.T) {
	m, store, p, _ := managerFixture(t)
	signIn(t, m)
	p.mu.Lock()
	p.block = make(chan struct{})
	p.mu.Unlock()
	m.Check()
	m.Disconnect()
	waitStatus(t, m, "disconnected")
	if _, err := store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("credential resurrected")
	}
}
func TestDisconnectRevocationFailureStillRemovesLocalCredential(t *testing.T) {
	m, store, p, _ := managerFixture(t)
	signIn(t, m)
	p.revokeErr = ErrFailed
	m.Disconnect()
	s := waitStatus(t, m, "disconnected")
	if s.Code != "revocation_failed" {
		t.Fatal(s)
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("retained credential")
	}
}
func TestDisconnectDeletionFailureIsNotSuccess(t *testing.T) {
	m, store, _, _ := managerFixture(t)
	signIn(t, m)
	store.mu.Lock()
	store.failure = credentials.ErrUnavailable
	store.mu.Unlock()
	m.Disconnect()
	s := waitStatus(t, m, "error")
	if s.Code != "credential_store_unavailable" {
		t.Fatal(s)
	}
}
func TestRestoreAfterRestartVerifiesProfileAndRefreshes(t *testing.T) {
	m, store, p, db := managerFixture(t)
	signIn(t, m)
	m.Close()
	next := NewManager(context.Background(), p, store, db.Accounts(), "test-keyring-ref")
	defer next.Close()
	next.Check()
	s := waitStatus(t, next, "connected")
	if s.Email != "reader@example.test" {
		t.Fatal("wrong identity")
	}
}
func TestLoopbackTimeoutAndMalformedCallback(t *testing.T) {
	l, err := newLoopback()
	if err != nil {
		t.Fatal(err)
	}
	defer l.server.Close()
	for _, query := range []string{"state=" + l.state + "&code=a&code=b", "state=" + l.state + "&code=a&error=access_denied", "state=" + l.state + "&code=" + strings.Repeat("x", 9000)} {
		resp, err := http.Get(l.redirect + "?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatal("malformed callback accepted")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err = l.wait(ctx); !errors.Is(err, ErrTimeout) {
		t.Fatal("missing timeout")
	}
	if _, err = http.Get(l.redirect); err == nil {
		t.Fatal("listener still open")
	}
}

func TestCancelledSignInClosesListenerAndDoesNotConnect(t *testing.T) {
	m, store, _, _ := managerFixture(t)
	begin := m.Begin()
	parsed, _ := url.Parse(begin.AuthorizationURL)
	m.Cancel()
	s := waitStatus(t, m, "error")
	if s.Code != "authentication_cancelled" {
		t.Fatal(s)
	}
	if _, err := http.Get(parsed.Query().Get("redirect_uri")); err == nil {
		t.Fatal("cancelled listener still open")
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("cancel created credential")
	}
}
func TestDisconnectWhileBrowserWaiting(t *testing.T) {
	m, store, _, _ := managerFixture(t)
	begin := m.Begin()
	parsed, _ := url.Parse(begin.AuthorizationURL)
	m.Disconnect()
	waitStatus(t, m, "disconnected")
	if _, err := http.Get(parsed.Query().Get("redirect_uri")); err == nil {
		t.Fatal("disconnected listener still open")
	}
	if _, err := store.Load(context.Background()); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("credential retained")
	}
}

type blockedStore struct {
	*memoryStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockedStore) Load(ctx context.Context) (credentials.Record, error) {
	close(s.entered)
	<-s.release // emulate a native prompt that cannot be interrupted
	return s.memoryStore.Load(ctx)
}
func TestCancellationClosesListenerWhileNativeStoreIsBlocked(t *testing.T) {
	m, store, _, _ := managerFixture(t)
	blocked := &blockedStore{memoryStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	m.store = blocked
	begin := m.Begin()
	u, _ := url.Parse(begin.AuthorizationURL)
	<-blocked.entered
	m.Cancel()
	defer close(blocked.release)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(u.Query().Get("redirect_uri"))
		if err != nil {
			return
		}
		response.Body.Close()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("callback survived cancellation while keyring was blocked")
}

func TestClosedManagerCannotRestartAuthentication(t *testing.T) {
	m, _, p, _ := managerFixture(t)
	m.Close()
	if got := m.Begin(); got.AuthorizationURL != "" || got.Status.State != "disconnected" {
		t.Fatal("closed manager started sign-in")
	}
	if m.Check().State != "disconnected" || m.Disconnect().State != "disconnected" {
		t.Fatal("closed manager started work")
	}
	m.mu.Lock()
	busy := m.busy
	m.mu.Unlock()
	if busy {
		t.Fatal("closed manager has a pending worker")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refreshes != 0 || p.exchanges != 0 {
		t.Fatal("closed manager contacted provider")
	}
}

func TestCallbackRejectsErrorWithAnyCodeParameter(t *testing.T) {
	l, err := newLoopback()
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	for _, extra := range []string{"&code=", "&code=&code=synthetic"} {
		response, err := http.Get(l.redirect + "?state=" + l.state + "&error=access_denied" + extra)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatal("ambiguous callback accepted")
		}
	}
	if l.used.Load() {
		t.Fatal("invalid callback consumed the session")
	}
}

func TestSyncTokenBoundaryAndDisconnectCancellation(t *testing.T) {
	m, _, p, _ := managerFixture(t)
	signIn(t, m)
	account, session, err := m.Session()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.AccessToken(context.Background(), "another-account"); !errors.Is(err, ErrExpired) {
		t.Fatal("cross-account token returned")
	}
	m.mu.Lock()
	m.grant.Expiry = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := m.AccessToken(context.Background(), account)
			if err != nil || token.Empty() {
				t.Error("token refresh failed")
			}
		}()
	}
	wg.Wait()
	p.mu.Lock()
	refreshes := p.refreshes
	p.mu.Unlock()
	if refreshes != 1 {
		t.Fatalf("refresh stampede: %d", refreshes)
	}
	m.Disconnect()
	if session.Err() == nil {
		t.Fatal("disconnect left sync session active")
	}
	if _, _, err = m.Session(); !errors.Is(err, ErrExpired) {
		t.Fatal("disconnected session returned")
	}
	if _, err = m.AccessToken(context.Background(), account); !errors.Is(err, ErrExpired) {
		t.Fatal("disconnected token returned")
	}
}

func TestRejectedCredentialsInvalidateExistingAccountSession(t *testing.T) {
	for _, cause := range []string{"revoked", "consent_denied", "missing_scope", "credential_store_unavailable", "missing_credentials"} {
		t.Run(cause, func(t *testing.T) {
			m, store, p, _ := managerFixture(t)
			signIn(t, m)
			_, session, err := m.Session()
			if err != nil {
				t.Fatal(err)
			}
			expected := "reauth_required"
			switch cause {
			case "revoked":
				p.mu.Lock()
				p.refreshErr = ErrExpired
				p.mu.Unlock()
			case "consent_denied", "missing_scope":
				p.mu.Lock()
				if cause == "consent_denied" {
					p.refreshErr = ErrDenied
				} else {
					p.refreshErr = ErrScope
				}
				p.mu.Unlock()
				expected = "error"
			case "credential_store_unavailable":
				store.mu.Lock()
				store.failure = credentials.ErrUnavailable
				store.mu.Unlock()
				expected = "error"
			case "missing_credentials":
				if err := store.Delete(context.Background()); err != nil {
					t.Fatal(err)
				}
				expected = "disconnected"
			}
			m.Check()
			waitStatus(t, m, expected)
			if session.Err() != context.Canceled {
				t.Fatal("failed check retained existing session")
			}
			if _, _, err := m.Session(); !errors.Is(err, ErrExpired) {
				t.Fatal("failed check still authorizes cached account")
			}
			p.mu.Lock()
			p.refreshErr = nil
			p.mu.Unlock()
			store.mu.Lock()
			store.failure = nil
			store.mu.Unlock()
			if cause != "missing_credentials" {
				m.Check()
				waitStatus(t, m, "connected")
				_, renewed, err := m.Session()
				if err != nil || renewed == session || renewed.Err() != nil {
					t.Fatal("successful retry did not create a fresh session")
				}
			}
		})
	}
}
func TestFailedAccessTokenRefreshInvalidatesSession(t *testing.T) {
	m, _, p, _ := managerFixture(t)
	signIn(t, m)
	account, session, err := m.Session()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.grant.Expiry = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	p.mu.Lock()
	p.refreshErr = ErrExpired
	p.mu.Unlock()
	if _, err := m.AccessToken(context.Background(), account); !errors.Is(err, ErrExpired) {
		t.Fatal("revoked refresh accepted")
	}
	if session.Err() != context.Canceled {
		t.Fatal("failed token refresh retained old session")
	}
	if _, _, err := m.Session(); !errors.Is(err, ErrExpired) {
		t.Fatal("failed token refresh still authorizes account")
	}
}

func TestTransientRefreshFailurePreservesOfflineCacheSession(t *testing.T) {
	m, _, p, _ := managerFixture(t)
	signIn(t, m)
	account, session, err := m.Session()
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.refreshErr = ErrFailed
	p.mu.Unlock()
	m.Check()
	waitStatus(t, m, "error")
	id, current, err := m.Session()
	if err != nil || id != account || current != session || session.Err() != nil {
		t.Fatal("transient provider failure revoked local cache authorization")
	}
	if _, err := m.AccessToken(context.Background(), account); !errors.Is(err, ErrExpired) {
		t.Fatal("failed refresh authorized provider traffic")
	}
}
