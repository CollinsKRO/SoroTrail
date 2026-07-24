package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/khaylebfortune/sorotrail/internal/rpc"
	"github.com/khaylebfortune/sorotrail/internal/store"
)

const testContract = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"

// stubStore returns canned values and records the filter it was queried with.
type stubStore struct {
	store.Store // panic on anything not stubbed below

	events     []store.Event
	nextCursor string
	queryErr   error
	lastFilter store.EventFilter

	event    store.Event
	eventErr error

	exists       bool
	existsErr    error
	existsCalls  int // count of EventExists calls
	lastExistsID string

	ingestion    store.IngestionState
	ingestionErr error

	stats   store.Stats
	pingErr error
}

func (s *stubStore) QueryEvents(_ context.Context, f store.EventFilter) ([]store.Event, string, error) {
	s.lastFilter = f
	return s.events, s.nextCursor, s.queryErr
}

// LedgerRangeCensus, ReplaceEventsInRange, and the audit_state/findings
// methods are unused by API tests but needed to satisfy store.Store now.
func (s *stubStore) ReplaceEventsInRange(context.Context, []store.Event, int64, int64) error {
	return nil
}
func (s *stubStore) LedgerRangeCensus(context.Context, int64, int64, bool) ([]store.LedgerCensus, error) {
	return nil, nil
}
func (s *stubStore) GetAuditState(context.Context) (store.AuditState, error) {
	return store.AuditState{}, store.ErrNotFound
}
func (s *stubStore) SaveAuditState(context.Context, store.AuditState) error {
	return nil
}
func (s *stubStore) SaveAuditStateIfGreater(_ context.Context, ledger int64) (store.AuditState, error) {
	return store.AuditState{VerifiedThroughLedger: ledger}, nil
}
func (s *stubStore) RecordAuditFinding(_ context.Context, f store.AuditFinding) (store.AuditFinding, error) {
	f.ID = 1
	return f, nil
}
func (s *stubStore) UpdateAuditFinding(context.Context, store.AuditFinding) error {
	return nil
}
func (s *stubStore) ListOpenFindingsByRange(context.Context, int64, int64) (store.AuditFinding, error) {
	return store.AuditFinding{}, store.ErrNotFound
}

func (s *stubStore) GetEvent(context.Context, string) (store.Event, error) {
	return s.event, s.eventErr
}

func (s *stubStore) GetContractSpec(context.Context, string) ([]byte, error) {
	return nil, store.ErrNotFound
}
func (s *stubStore) SetContractSpec(context.Context, string, string, []byte) error {
	return nil
}

// EventExists is the cheap 304 path; tests assert the handler uses it
// (instead of GetEvent) when If-None-Match matches.
func (s *stubStore) EventExists(_ context.Context, id string) (bool, error) {
	s.existsCalls++
	s.lastExistsID = id
	return s.exists, s.existsErr
}

// GetIngestionState backs the list-cache frontier lookup. Tests stage
// LastIngestedLedger to drive the boundary decisions (just-below, at,
// and above the frontier).
func (s *stubStore) GetIngestionState(context.Context) (store.IngestionState, error) {
	return s.ingestion, s.ingestionErr
}

func (s *stubStore) Stats(context.Context) (store.Stats, error) { return s.stats, nil }
func (s *stubStore) Ping(context.Context) error                 { return s.pingErr }

// Subscription stubs for the webhook feature.
func (s *stubStore) CreateSubscription(_ context.Context, sub store.Subscription) (store.Subscription, error) {
	sub.ID = 1
	return sub, nil
}
func (s *stubStore) GetSubscription(_ context.Context, id int64) (store.Subscription, error) {
	return store.Subscription{}, store.ErrNotFound
}
func (s *stubStore) ListSubscriptions(context.Context, string, int) ([]store.Subscription, string, error) {
	return nil, "", nil
}
func (s *stubStore) UpdateSubscription(_ context.Context, sub store.Subscription) (store.Subscription, error) {
	return sub, nil
}
func (s *stubStore) DeleteSubscription(context.Context, int64) error { return nil }
func (s *stubStore) ListEnabledSubscriptions(context.Context) ([]store.Subscription, error) {
	return nil, nil
}
func (s *stubStore) IncrementSubscriptionFailures(context.Context, int64, int) (int, bool, error) {
	return 0, false, nil
}
func (s *stubStore) ResetSubscriptionFailures(context.Context, int64) error { return nil }
func (s *stubStore) RecordDeliveryAttempt(_ context.Context, a store.DeliveryAttempt) (store.DeliveryAttempt, error) {
	a.ID = 1
	return a, nil
}
func (s *stubStore) ListDeliveryAttempts(context.Context, int64, string, int) ([]store.DeliveryAttempt, string, error) {
	return nil, "", nil
}

type stubRPC struct {
	rpc.Client

	health    rpc.Health
	healthErr error
}

func (s *stubRPC) GetHealth(context.Context) (rpc.Health, error) {
	return s.health, s.healthErr
}

func newTestServer(st *stubStore, rc *stubRPC) *Server {
	if rc == nil {
		rc = &stubRPC{health: rpc.Health{Status: "healthy"}}
	}
	return New(st, rc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func doGet(t *testing.T, s *Server, path string) (*http.Response, []byte) {
	t.Helper()
	srv := httptest.NewServer(s.Router())
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	return resp, body
}

func TestListEvents_ParsesFilters(t *testing.T) {
	st := &stubStore{}
	s := newTestServer(st, nil)

	resp, body := doGet(t, s,
		"/events?contract_id="+testContract+`&type=contract&from_ledger=10&to_ledger=20&limit=5&topic={"symbol":"transfer"}&topic_contains=[{"address":"G..."}]&from_time=2026-07-21T00:00:00Z&to_time=2026-07-22T00:00:00Z`)

	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Equal(t, testContract, st.lastFilter.ContractID)
	assert.Equal(t, "contract", st.lastFilter.Type)
	assert.Equal(t, int64(10), st.lastFilter.FromLedger)
	assert.Equal(t, int64(20), st.lastFilter.ToLedger)
	assert.Equal(t, 5, st.lastFilter.Limit)
	assert.JSONEq(t, `{"symbol":"transfer"}`, string(st.lastFilter.Topic))
	assert.JSONEq(t, `[{"address":"G..."}]`, string(st.lastFilter.TopicContains))
	assert.Equal(t, "2026-07-21T00:00:00Z", st.lastFilter.FromTime.Format(time.RFC3339))
	assert.Equal(t, "2026-07-22T00:00:00Z", st.lastFilter.ToTime.Format(time.RFC3339))
}

func TestListEvents_BareTopicBecomesJSONString(t *testing.T) {
	st := &stubStore{}
	s := newTestServer(st, nil)

	resp, _ := doGet(t, s, "/events?topic=transfer")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `"transfer"`, string(st.lastFilter.Topic))
}

func TestListEvents_PositionalTopicFiltersParse(t *testing.T) {
	st := &stubStore{}
	s := newTestServer(st, nil)

	resp, _ := doGet(t, s,
		"/events?topic0={\"symbol\":\"transfer\"}&topic1=GABC&topic2={\"x\":123}")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"symbol":"transfer"}`, string(st.lastFilter.Topic0))
	assert.JSONEq(t, `"GABC"`, string(st.lastFilter.Topic1))
	assert.JSONEq(t, `{"x":123}`, string(st.lastFilter.Topic2))
}

func TestListEvents_TopicAndPositionalFiltersConflict(t *testing.T) {
	resp, body := doGet(t, newTestServer(&stubStore{}, nil), "/events?topic=transfer&topic0=GABC")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var e map[string]string
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Contains(t, e["error"], "cannot be combined")
}

func TestListEvents_BadParams(t *testing.T) {
	for _, path := range []string{
		"/events?type=bogus",
		"/events?contract_id=nope",
		"/events?from_ledger=abc",
		"/events?from_ledger=20&to_ledger=10",
		"/events?from_time=not-a-time",
		"/events?from_time=2026-07-21T00:00:00",
		"/events?from_time=2026-07-21T00:00:00.123Z",
		"/events?from_time=2026-07-22T00:00:00Z&to_time=2026-07-21T00:00:00Z",
		"/events?limit=0",
		"/events?limit=99999",
		"/events?topic_contains=not-valid-json",
	} {
		t.Run(path, func(t *testing.T) {
			resp, body := doGet(t, newTestServer(&stubStore{}, nil), path)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			var e map[string]string
			require.NoError(t, json.Unmarshal(body, &e))
			assert.NotEmpty(t, e["error"])
		})
	}
}

func TestListEvents_ReturnsNextCursor(t *testing.T) {
	st := &stubStore{
		events:     []store.Event{{ID: "e1"}, {ID: "e2"}},
		nextCursor: "e2",
	}
	resp, body := doGet(t, newTestServer(st, nil), "/events")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Events     []store.Event `json:"events"`
		NextCursor string        `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Len(t, out.Events, 2)
	// nextCursor should be base64-encoded "e2"
	assert.Equal(t, "ZTI=", out.NextCursor)
}

func TestListEvents_EncodedCursorRoundTrip(t *testing.T) {
	// Verify that a cursor passed to /events is decoded before reaching
	// the store, and a cursor returned from the store is encoded in the
	// response.
	st := &stubStore{
		events:     []store.Event{{ID: "result"}},
		nextCursor: "", // exhausted
	}
	resp, body := doGet(t, newTestServer(st, nil), "/events?cursor=ZTI=") // base64("e2")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "e2", st.lastFilter.Cursor, "cursor must be decoded before hitting the store")

	// Also test invalid cursor returns 400
	resp2, _ := doGet(t, newTestServer(&stubStore{}, nil), "/events?cursor=!!!not-base64!!!")
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode, "invalid cursor must return 400")
	_ = resp
	_ = body
}

func TestListEvents_PaginationBoundaries(t *testing.T) {
	t.Run("empty result set", func(t *testing.T) {
		st := &stubStore{events: nil, nextCursor: ""}
		resp, body := doGet(t, newTestServer(st, nil), "/events")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		assert.NotContains(t, out, "next_cursor", "empty set must omit next_cursor")
	})

	t.Run("exactly one page has no next_cursor", func(t *testing.T) {
		st := &stubStore{events: []store.Event{{ID: "e1"}}, nextCursor: ""}
		resp, body := doGet(t, newTestServer(st, nil), "/events?limit=1")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		assert.NotContains(t, out, "next_cursor", "full page that ends the set must omit next_cursor")
	})

	t.Run("cursor past end returns empty page", func(t *testing.T) {
		st := &stubStore{events: nil, nextCursor: ""}
		resp, body := doGet(t, newTestServer(st, nil), "/events?cursor=YWJjZGVmZw==") // base64("abcdefg")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out eventsResponse
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Empty(t, out.Events)
		assert.Empty(t, out.NextCursor)
	})

	t.Run("invalid cursor returns 400", func(t *testing.T) {
		resp, body := doGet(t, newTestServer(&stubStore{}, nil), "/events?cursor=!!!invalid!!!")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var e map[string]string
		require.NoError(t, json.Unmarshal(body, &e))
		assert.Contains(t, e["error"], "cursor")
	})
}

func TestListEvents_IncludeXDR(t *testing.T) {
	event := store.Event{
		ID:          "e1",
		RawTopicXDR: []string{"topic-xdr"},
		RawValueXDR: "value-xdr",
	}
	st := &stubStore{events: []store.Event{event}}
	s := newTestServer(st, nil)

	resp, body := doGet(t, s, "/events")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, string(body), "topics_xdr")
	assert.NotContains(t, string(body), "value_xdr")

	resp, body = doGet(t, s, "/events?include_xdr=true")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Events []struct {
			TopicsXDR []string `json:"topics_xdr"`
			ValueXDR  *string  `json:"value_xdr"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.Events, 1)
	assert.Equal(t, []string{"topic-xdr"}, out.Events[0].TopicsXDR)
	require.NotNil(t, out.Events[0].ValueXDR)
	assert.Equal(t, "value-xdr", *out.Events[0].ValueXDR)
}

func TestGetEvent_NotFound(t *testing.T) {
	st := &stubStore{eventErr: store.ErrNotFound}
	resp, _ := doGet(t, newTestServer(st, nil), "/events/0000000000-0000000000")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetEvent_IncludeXDR(t *testing.T) {
	st := &stubStore{event: store.Event{
		ID:          "0000000000-0000000001",
		RawTopicXDR: []string{"topic-xdr"},
		RawValueXDR: "value-xdr",
	}}
	s := newTestServer(st, nil)

	resp, body := doGet(t, s, "/events/0000000000-0000000001")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, string(body), "topics_xdr")
	assert.NotContains(t, string(body), "value_xdr")

	resp, body = doGet(t, s, "/events/0000000000-0000000001?include_xdr=true")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		TopicsXDR []string `json:"topics_xdr"`
		ValueXDR  *string  `json:"value_xdr"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, []string{"topic-xdr"}, out.TopicsXDR)
	require.NotNil(t, out.ValueXDR)
	assert.Equal(t, "value-xdr", *out.ValueXDR)
}

func TestContractEvents_ForcesContractFilter(t *testing.T) {
	st := &stubStore{}
	resp, _ := doGet(t, newTestServer(st, nil), "/contracts/"+testContract+"/events")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testContract, st.lastFilter.ContractID)

	resp, _ = doGet(t, newTestServer(st, nil), "/contracts/junk/events")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestListEvents_TopicContainsValidation(t *testing.T) {
	t.Run("valid JSON array", func(t *testing.T) {
		st := &stubStore{}
		s := newTestServer(st, nil)
		resp, _ := doGet(t, s, `/events?topic_contains=[{"address":"G..."}]`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `[{"address":"G..."}]`, string(st.lastFilter.TopicContains))
	})

	t.Run("valid JSON object", func(t *testing.T) {
		st := &stubStore{}
		s := newTestServer(st, nil)
		resp, _ := doGet(t, s, `/events?topic_contains={"address":"G..."}`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `{"address":"G..."}`, string(st.lastFilter.TopicContains))
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		st := &stubStore{}
		s := newTestServer(st, nil)
		resp, body := doGet(t, s, `/events?topic_contains=not-json`)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var e map[string]string
		require.NoError(t, json.Unmarshal(body, &e))
		assert.Contains(t, e["error"], "valid JSON")
	})

	t.Run("empty topic_contains is a no-op", func(t *testing.T) {
		st := &stubStore{}
		s := newTestServer(st, nil)
		resp, _ := doGet(t, s, `/events?topic_contains=`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, st.lastFilter.TopicContains)
	})

	t.Run("combined with contract_id and ledger", func(t *testing.T) {
		st := &stubStore{}
		s := newTestServer(st, nil)
		resp, _ := doGet(t, s, `/events?contract_id=`+testContract+`&from_ledger=100&topic_contains=[{"symbol":"transfer"}]`)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, testContract, st.lastFilter.ContractID)
		assert.Equal(t, int64(100), st.lastFilter.FromLedger)
		assert.JSONEq(t, `[{"symbol":"transfer"}]`, string(st.lastFilter.TopicContains))
	})
}

func TestHealth(t *testing.T) {
	t.Run("all healthy", func(t *testing.T) {
		resp, _ := doGet(t, newTestServer(&stubStore{}, nil), "/health")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
	t.Run("db down", func(t *testing.T) {
		st := &stubStore{pingErr: errors.New("connection refused")}
		resp, body := doGet(t, newTestServer(st, nil), "/health")
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		assert.Contains(t, string(body), "connection refused")
	})
	t.Run("rpc down", func(t *testing.T) {
		rc := &stubRPC{healthErr: errors.New("rpc unreachable")}
		resp, _ := doGet(t, newTestServer(&stubStore{}, rc), "/health")
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})
}

func TestStats(t *testing.T) {
	t.Run("includes store and freshness fields", func(t *testing.T) {
		st := &stubStore{stats: store.Stats{
			TotalEvents:        42,
			LastIngestedLedger: 999,
			OldestStoredLedger: 100,
		}}
		rc := &stubRPC{health: rpc.Health{Status: "healthy", LatestLedger: 1_020}}
		resp, body := doGet(t, newTestServer(st, rc), "/stats")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var got store.Stats
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, int64(42), got.TotalEvents)
		assert.Equal(t, int64(999), got.LastIngestedLedger)
		assert.Equal(t, int64(100), got.OldestStoredLedger)
		require.NotNil(t, got.ChainHeadLedger)
		assert.Equal(t, int64(1_020), *got.ChainHeadLedger)
		require.NotNil(t, got.IngestLagLedgers)
		assert.Equal(t, int64(21), *got.IngestLagLedgers)
	})

	t.Run("keeps stored stats when RPC is down", func(t *testing.T) {
		st := &stubStore{stats: store.Stats{
			TotalEvents:        42,
			LastIngestedLedger: 999,
			OldestStoredLedger: 100,
		}}
		rc := &stubRPC{healthErr: errors.New("rpc unreachable")}
		resp, body := doGet(t, newTestServer(st, rc), "/stats")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var got store.Stats
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, int64(42), got.TotalEvents)
		assert.Equal(t, int64(999), got.LastIngestedLedger)
		assert.Equal(t, int64(100), got.OldestStoredLedger)
		assert.Nil(t, got.ChainHeadLedger)
		assert.Nil(t, got.IngestLagLedgers)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(body, &raw))
		assert.Contains(t, raw, "chain_head_ledger")
		assert.Nil(t, raw["chain_head_ledger"])
		assert.Contains(t, raw, "ingest_lag_ledgers")
		assert.Nil(t, raw["ingest_lag_ledgers"])
	})
}
