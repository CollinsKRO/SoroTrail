package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/khaylebfortune/sorotrail/internal/store"
)

// --- Response types ---

type subscriptionsResponse struct {
	Subscriptions []store.Subscription `json:"subscriptions"`
	NextCursor    string               `json:"next_cursor,omitempty"`
}

type deliveriesResponse struct {
	Deliveries []store.DeliveryAttempt `json:"deliveries"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

// parseLimitParam returns 0 when the raw string is empty (the caller should
// use ResolvePageLimit on the result), or the parsed integer. It returns an
// error when the value is not a valid non-negative integer, so callers can
// return 400 to the client instead of silently falling back to the default.
func parseLimitParam(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("limit must be an integer, got %q", raw)
	}
	return n, nil
}

// --- Subscription CRUD handlers ---

// createSubscriptionRequest is the JSON body for POST /subscriptions.
type createSubscriptionRequest struct {
	URL     string                   `json:"url"`
	Filters store.SubscriptionFilter `json:"filters"`
	Secret  string                   `json:"secret"`
	Enabled *bool                    `json:"enabled,omitempty"` // defaults to true
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req createSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, errors.New("url is required"))
		return
	}
	if req.Secret == "" {
		writeError(w, http.StatusBadRequest, errors.New("secret is required"))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	sub := store.Subscription{
		URL:     req.URL,
		Filters: req.Filters,
		Secret:  req.Secret,
		Enabled: enabled,
	}
	created, err := s.store.CreateSubscription(r.Context(), sub)
	if err != nil {
		s.log.Error("creating subscription", "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("creating subscription failed"))
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := parseSubscriptionID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sub, err := s.store.GetSubscription(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, fmt.Errorf("subscription %d not found", id))
		return
	}
	if err != nil {
		s.log.Error("getting subscription", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("getting subscription failed"))
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	rawCursor := r.URL.Query().Get("cursor")
	cursor := ""
	if rawCursor != "" {
		var err error
		cursor, err = DecodeCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	rawLimit, limitErr := parseLimitParam(r.URL.Query().Get("limit"))
	if limitErr != nil {
		writeError(w, http.StatusBadRequest, limitErr)
		return
	}
	limit := ResolvePageLimit(rawLimit)

	subs, nextRaw, err := s.store.ListSubscriptions(r.Context(), cursor, limit)
	if err != nil {
		s.log.Error("listing subscriptions", "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("listing subscriptions failed"))
		return
	}
	next := ""
	if nextRaw != "" {
		next = EncodeCursor(nextRaw)
	}
	writeJSON(w, http.StatusOK, subscriptionsResponse{Subscriptions: subs, NextCursor: next})
}

type updateSubscriptionRequest struct {
	URL     *string                   `json:"url,omitempty"`
	Filters *store.SubscriptionFilter `json:"filters,omitempty"`
	Secret  *string                   `json:"secret,omitempty"`
	Enabled *bool                     `json:"enabled,omitempty"`
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := parseSubscriptionID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	existing, err := s.store.GetSubscription(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, fmt.Errorf("subscription %d not found", id))
		return
	}
	if err != nil {
		s.log.Error("getting subscription for update", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("getting subscription failed"))
		return
	}

	var req updateSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.Filters != nil {
		existing.Filters = *req.Filters
	}
	if req.Secret != nil {
		existing.Secret = *req.Secret
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if existing.URL == "" {
		writeError(w, http.StatusBadRequest, errors.New("url must not be empty"))
		return
	}

	updated, err := s.store.UpdateSubscription(r.Context(), existing)
	if err != nil {
		s.log.Error("updating subscription", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("updating subscription failed"))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := parseSubscriptionID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.DeleteSubscription(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, fmt.Errorf("subscription %d not found", id))
			return
		}
		s.log.Error("deleting subscription", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("deleting subscription failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Delivery attempts ---

func (s *Server) handleListDeliveries(w http.ResponseWriter, r *http.Request) {
	id, err := parseSubscriptionID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Verify the subscription exists.
	if _, err := s.store.GetSubscription(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, fmt.Errorf("subscription %d not found", id))
		return
	}

	rawCursor := r.URL.Query().Get("cursor")
	cursor := ""
	if rawCursor != "" {
		var err error
		cursor, err = DecodeCursor(rawCursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	rawLimit, limitErr := parseLimitParam(r.URL.Query().Get("limit"))
	if limitErr != nil {
		writeError(w, http.StatusBadRequest, limitErr)
		return
	}
	limit := ResolvePageLimit(rawLimit)

	attempts, nextRaw, err := s.store.ListDeliveryAttempts(r.Context(), id, cursor, limit)
	if err != nil {
		s.log.Error("listing delivery attempts", "subscription_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, errors.New("listing delivery attempts failed"))
		return
	}
	next := ""
	if nextRaw != "" {
		next = EncodeCursor(nextRaw)
	}
	writeJSON(w, http.StatusOK, deliveriesResponse{Deliveries: attempts, NextCursor: next})
}

func parseSubscriptionID(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("subscription id must be a positive integer, got %q", raw)
	}
	return id, nil
}
