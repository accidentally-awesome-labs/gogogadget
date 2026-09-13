// Package flagsmith implements the managed feature-flag target against
// Flagsmith's two documented HTTP APIs with plain net/http, in the idiom of
// internal/billing/polar/client.go and the v0.26.0 adapter rewrites.
//
// Flagsmith was chosen because it is the only candidate that satisfies all
// seven flags.Service methods through a documented HTTP contract in which the
// PROVIDER performs the decision. Nothing here reimplements vendor targeting
// semantics and nothing here vendors an SDK, so the seam rule in AGENTS.md
// ("handlers never import SDKs") holds for a managed flag provider for the
// first time.
//
// Two APIs, two credentials
// (https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview):
//
//   - The Flags API answers evaluations. It authenticates with the public,
//     non-secret Environment Key in the X-Environment-Key header — the docs
//     say it "can be exposed in client-side code", which is why
//     FLAGSMITH_ENVIRONMENT_KEY is declared secret:false in this module's
//     manifest.
//   - The Management API performs every mutation and every administrative
//     read. It authenticates with a SECRET organisation API token, sent as
//     `Authorization: Api-Key <token>`
//     (https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/admin-api/authentication).
//
// Identity mapping. The seam's unit of targeting is an organisation id:
// Enabled takes (orgID, key) and Override is {OrgID, Enabled}. This adapter
// identifies the ORG to Flagsmith — POST /api/v1/identities/ with
// identifier=orgID — so a per-org override is a Flagsmith IDENTITY override,
// not a segment. Flagsmith's documented precedence is
//
//  1. identity overrides  2. segment overrides  3. environment default
//
// (https://docs.flagsmith.com/flagsmith-concepts/segments#flag-evaluation-precedence),
// which is exactly the seam's fixed order in internal/flags/flags.go:99-102:
// a per-org override wins over everything, else the environment default
// decides. A segment would have been the wrong object: segments are
// trait-rule cohorts, so "org X specifically" would need one segment per org
// (500 segments per project is the documented limit,
// https://docs.flagsmith.com/flagsmith-concepts/segments/segment-rule-operators#limits)
// and Flagsmith would still rank it BELOW an identity override.
package flagsmith

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/flags"
)

// defaultFlagsBaseURL is the Flags API host for SaaS customers, named as such
// by https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/flags-api.
// A self-hosted deployment serves both APIs from one host, so it sets this and
// defaultManagementBaseURL to the same value.
const defaultFlagsBaseURL = "https://edge.api.flagsmith.com"

// defaultManagementBaseURL is the Management API host, the one publishing the
// OpenAPI document at /api/v1/swagger.json
// (https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/management-api).
const defaultManagementBaseURL = "https://api.flagsmith.com"

// requestTimeout bounds one provider call. Enabled runs on request paths, so a
// provider that stops answering must fail fast rather than hold the handler.
const requestTimeout = 10 * time.Second

// decisionTTL is how stale one evaluation may be. Flagsmith performs the
// decision over the network, so without this every flag check on every request
// path would cost a round trip. The bound is fixed by the adapter for the same
// reason internal/flags/module.go fixes its own: every deployment must agree on
// it. Mutations cannot invalidate a per-identity entry (cache.Store has no
// prefix delete), so a changed override becomes visible within decisionTTL.
const decisionTTL = 30 * time.Second

// pageLimit bounds pagination on the Management API's paginated collections.
// The documented per-project ceiling is far below this, and an unbounded
// follow loop on a provider that always returns `next` would never return.
const pageLimit = 50

// ErrRolloutUnsupported refuses a partial rollout rather than accepting it and
// silently dropping it.
//
// Flagsmith does not model a rollout percentage as a field on a feature: the
// writable fields are the feature's description and the environment feature
// state's `enabled` (see the request bodies below). A gradual rollout in
// Flagsmith is a SEGMENT with a `% Split` condition plus a segment override
// (https://docs.flagsmith.com/flagsmith-concepts/segments/segment-rule-operators,
// https://docs.flagsmith.com/managing-flags/rollout/rollout-by-percentage) —
// a separate object graph this adapter deliberately does not author. So the
// rollout values this adapter round-trips are exactly {100}: List reports 100,
// Upsert accepts 100, and anything else is refused by name with the console
// link, which is strictly more honest than storing a number the provider will
// never consult.
var ErrRolloutUnsupported = errors.New("flagsmith: a partial rollout is a percentage-split segment override in Flagsmith, not a feature field; set it in the provider console and leave rollout at 100 here")

// Service is the flags.Service implementation. Reads that a request path needs
// go through the Flags API; everything administrative goes through the
// Management API.
type Service struct {
	// Flags is the Flags API base URL (evaluation).
	Flags string
	// Management is the Management API base URL (administration).
	Management string
	// EnvironmentKey is the public Environment Key.
	EnvironmentKey string
	// Token is the secret organisation API token.
	Token string
	// ProjectID and EnvironmentID are the numeric Management API coordinates.
	// Features are project-scoped; feature states are environment-scoped.
	ProjectID, EnvironmentID int
	// Console is the provider console URL surfaced by the admin page.
	Console string
	// Cache bounds the cost of evaluation. It is required: see decisionTTL.
	Cache cache.Store
	// HTTP is the shared client.
	HTTP *http.Client
	// Log receives the errors Enabled cannot return.
	Log *slog.Logger
}

var _ flags.Service = (*Service)(nil)
var _ flags.Evaluator = (*Service)(nil)
var _ apphost.HealthChecker = (*Service)(nil)

// New builds a Service. Callers that reach the provider must go through
// NewModule, which refuses missing configuration.
func New(s Service) *Service {
	s.Flags = strings.TrimRight(orDefault(s.Flags, defaultFlagsBaseURL), "/")
	s.Management = strings.TrimRight(orDefault(s.Management, defaultManagementBaseURL), "/")
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: requestTimeout}
	}
	return &s
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

// ---------------------------------------------------------------------------
// Evaluation — the Flags API, the provider performing the decision.
// ---------------------------------------------------------------------------

// sdkFlag is one element of both Flags API responses. Shape pinned from the
// published OpenAPI document for GET /api/v1/flags/ and POST
// /api/v1/identities/: {feature:{id,name,type}, enabled, feature_state_value}.
type sdkFlag struct {
	Feature struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"feature"`
	Enabled bool `json:"enabled"`
}

// Enabled asks Flagsmith whether the feature is on for this organisation and
// returns Flagsmith's answer verbatim. It never recomputes targeting locally.
//
// With an organisation id it uses the identity workflow, which the docs
// describe as one call that "lazily creates an identity if it doesn't already
// exist" and "receives the flags for that identity, including any segment or
// identity-specific overrides":
//
//	POST {flags}/api/v1/identities/
//	X-Environment-Key: <environment key>
//	Content-Type: application/json
//	{"identifier":"<orgID>"}
//
// (https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/flags-api/code-examples).
// With no organisation — a global check — it reads the environment defaults
// instead: GET {flags}/api/v1/flags/ with the same header, from the same page.
//
// The seam gives Enabled no error return, so a provider failure is logged and
// answers false: a flag whose state cannot be established is off. An unknown
// key is false for the same reason the Postgres adapter makes it false
// (internal/flags/flags.go:99).
func (s *Service) Enabled(ctx context.Context, orgID, key string) bool {
	decisions, err := s.decisions(ctx, orgID)
	if err != nil {
		s.logError(ctx, "feature flag evaluation failed", err, "org_id", orgID, "flag", key)
		return false
	}
	return decisions[key]
}

// decisions returns every flag decision Flagsmith made for this subject,
// through the cache. One provider call answers every key, so the whole set is
// what gets cached.
func (s *Service) decisions(ctx context.Context, orgID string) (map[string]bool, error) {
	if s == nil {
		return nil, fmt.Errorf("flagsmith: service is required")
	}
	cacheKey := "flagsmith/decisions/" + orgID
	if s.Cache != nil {
		if raw, found, err := s.Cache.Get(ctx, cacheKey); err == nil && found {
			var cached map[string]bool
			if json.Unmarshal(raw, &cached) == nil {
				return cached, nil
			}
		} else if err != nil {
			// A cache read failure is a reported miss, never a flag failure.
			s.logError(ctx, "feature flag cache read failed", err, "org_id", orgID)
		}
	}
	list, err := s.evaluate(ctx, orgID)
	if err != nil {
		return nil, err
	}
	decisions := make(map[string]bool, len(list))
	for _, item := range list {
		decisions[item.Feature.Name] = item.Enabled
	}
	if s.Cache != nil {
		if raw, err := json.Marshal(decisions); err == nil {
			if err := s.Cache.Set(ctx, cacheKey, raw, decisionTTL); err != nil {
				s.logError(ctx, "feature flag cache write failed", err, "org_id", orgID)
			}
		}
	}
	return decisions, nil
}

// evaluate performs the one Flags API call that answers every key for one
// subject.
func (s *Service) evaluate(ctx context.Context, orgID string) ([]sdkFlag, error) {
	if strings.TrimSpace(s.EnvironmentKey) == "" {
		return nil, fmt.Errorf("flagsmith: FLAGSMITH_ENVIRONMENT_KEY is required")
	}
	if orgID == "" {
		var out []sdkFlag
		if err := s.flagsAPI(ctx, http.MethodGet, "/api/v1/flags/", nil, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	body := map[string]any{"identifier": orgID}
	var out struct {
		Identifier string    `json:"identifier"`
		Flags      []sdkFlag `json:"flags"`
	}
	if err := s.flagsAPI(ctx, http.MethodPost, "/api/v1/identities/", body, &out); err != nil {
		return nil, err
	}
	return out.Flags, nil
}

// ---------------------------------------------------------------------------
// Administration — the Management API.
// ---------------------------------------------------------------------------

// managementFeature is the subset of the Management API's feature
// representation this adapter reads. `environment_feature_state` is present
// only when the request passes ?environment=<id>, which is what makes List one
// call instead of one per flag; the OpenAPI description of
// GET /api/v1/projects/{project_pk}/features/ states exactly that.
type managementFeature struct {
	ID                      int    `json:"id"`
	Name                    string `json:"name"`
	Description             string `json:"description"`
	EnvironmentFeatureState *struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	} `json:"environment_feature_state"`
}

// List returns the admin table view.
//
//	GET {management}/api/v1/projects/{project}/features/?environment=<envID>
//	Authorization: Api-Key <token>
//
// Rollout is reported as 100 for every flag: see ErrRolloutUnsupported.
func (s *Service) List(ctx context.Context) ([]flags.Flag, error) {
	features, err := s.features(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]flags.Flag, 0, len(features))
	for _, feature := range features {
		enabled := false
		if feature.EnvironmentFeatureState != nil {
			enabled = feature.EnvironmentFeatureState.Enabled
		}
		out = append(out, flags.Flag{
			Key:         feature.Name,
			Description: feature.Description,
			Enabled:     enabled,
			Rollout:     100,
		})
	}
	return out, nil
}

// Upsert creates the feature when it is absent and reconciles it when it is
// present. Three documented calls, each the narrowest one that does the job:
//
//	POST  {management}/api/v1/projects/{project}/features/
//	      {"name","description","default_enabled","initial_value"}    (201)
//	PUT   {management}/api/v1/projects/{project}/features/{id}/
//	      {"description"}                                             (200)
//	PUT   {management}/api/v1/environments/{envKey}/featurestates/{id}/
//	      {"feature","enabled"}                                       (200)
//
// The split is forced by the provider's own schemas, not chosen: on create
// (ListFeature) name, description, default_enabled and initial_value are all
// writable, but on update (UpdateFeature) name, default_enabled and
// initial_value are read-only — so an existing feature's on/off state is only
// reachable through its environment feature state.
func (s *Service) Upsert(ctx context.Context, flag flags.Flag) error {
	if err := s.ready(); err != nil {
		return err
	}
	if strings.TrimSpace(flag.Key) == "" {
		return fmt.Errorf("flagsmith: flag key is required")
	}
	if flag.Rollout != 100 {
		return fmt.Errorf("%w (key %q, rollout %d, console %s)", ErrRolloutUnsupported, flag.Key, flag.Rollout, s.consoleOrDocs())
	}
	existing, err := s.feature(ctx, flag.Key)
	if err != nil {
		return err
	}
	if existing == nil {
		return s.managementAPI(ctx, http.MethodPost,
			fmt.Sprintf("/api/v1/projects/%d/features/", s.ProjectID), nil,
			map[string]any{
				"name":            flag.Key,
				"description":     flag.Description,
				"default_enabled": flag.Enabled,
				"initial_value":   "",
			}, nil)
	}
	if existing.Description != flag.Description {
		if err := s.managementAPI(ctx, http.MethodPut,
			fmt.Sprintf("/api/v1/projects/%d/features/%d/", s.ProjectID, existing.ID), nil,
			map[string]any{"description": flag.Description}, nil); err != nil {
			return err
		}
	}
	state := existing.EnvironmentFeatureState
	if state == nil {
		return fmt.Errorf("flagsmith: feature %q has no feature state in environment %d; the environment id and environment key must name the same environment", flag.Key, s.EnvironmentID)
	}
	if state.Enabled == flag.Enabled {
		return nil
	}
	return s.managementAPI(ctx, http.MethodPut,
		fmt.Sprintf("/api/v1/environments/%s/featurestates/%d/", url.PathEscape(s.EnvironmentKey), state.ID), nil,
		map[string]any{"feature": existing.ID, "enabled": flag.Enabled}, nil)
}

// Delete removes the feature from the project, which is what deleting a flag
// means here — Flagsmith has no per-environment feature deletion.
//
//	DELETE {management}/api/v1/projects/{project}/features/{id}/   (204)
//
// A key that is already absent is not an error: Delete is idempotent, matching
// the Postgres adapter's DELETE ... WHERE key = $1.
func (s *Service) Delete(ctx context.Context, key string) error {
	if err := s.ready(); err != nil {
		return err
	}
	existing, err := s.feature(ctx, key)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	return s.managementAPI(ctx, http.MethodDelete,
		fmt.Sprintf("/api/v1/projects/%d/features/%d/", s.ProjectID, existing.ID), nil, nil, nil)
}

// identityFeatureState is one feature state that belongs to an identity. The
// `identity` member is an object carrying the identifier, which is the seam's
// org id.
type identityFeatureState struct {
	ID       int  `json:"id"`
	Enabled  bool `json:"enabled"`
	Feature  int  `json:"feature"`
	Identity *struct {
		ID         int    `json:"id"`
		Identifier string `json:"identifier"`
	} `json:"identity"`
}

// ListOverrides returns every per-organisation override on one flag.
//
//	GET {management}/api/v1/environments/{envKey}/featurestates/
//	    ?feature_name=<key>&anyIdentity=1
//
// `anyIdentity` is the provider's own filter for this exact question — the
// published OpenAPI document documents it as "Pass any value to get results
// that have an identity override. Do not pass for default behaviour." — so the
// environment default row is excluded by the provider, not by this code.
func (s *Service) ListOverrides(ctx context.Context, key string) ([]flags.Override, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("flagsmith: flag key is required")
	}
	out := make([]flags.Override, 0)
	err := s.paginate(ctx, fmt.Sprintf("/api/v1/environments/%s/featurestates/", url.PathEscape(s.EnvironmentKey)),
		url.Values{"feature_name": {key}, "anyIdentity": {"1"}},
		func(raw json.RawMessage) error {
			var states []identityFeatureState
			if err := json.Unmarshal(raw, &states); err != nil {
				return fmt.Errorf("flagsmith: decode identity feature states: %w", err)
			}
			for _, state := range states {
				if state.Identity == nil || state.Identity.Identifier == "" {
					continue
				}
				out = append(out, flags.Override{OrgID: state.Identity.Identifier, Enabled: state.Enabled})
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetOverride pins one flag for one organisation, as a Flagsmith identity
// override — the top of the provider's precedence order.
//
//	GET  {management}/api/v1/environments/{envKey}/identities/?q="<orgID>"
//	POST {management}/api/v1/environments/{envKey}/identities/
//	     {"identifier":"<orgID>"}                                      (201)
//	GET  {management}/api/v1/environments/{envKey}/identities/{pk}/featurestates/
//	     ?feature=<featureID>
//	POST {management}/api/v1/environments/{envKey}/identities/{pk}/featurestates/
//	     {"feature":<id>,"enabled":<bool>}                             (201)
//	PUT  {management}/api/v1/environments/{envKey}/identities/{pk}/featurestates/{id}/
//	     {"feature":<id>,"enabled":<bool>}                             (200)
//
// The `q` filter on the identities collection is NOT in the published OpenAPI
// document; it is in the deployed implementation, where a double-quoted query
// is an exact identifier match rather than a fuzzy one
// (api/environments/identities/views.py, IdentityViewSet.get_queryset:
// `if search_query.startswith('"') ... identifier__exact`). This adapter
// therefore also re-checks the identifier in Go, so a provider that ever
// degraded the quoted form to fuzzy would still not override the wrong org.
func (s *Service) SetOverride(ctx context.Context, key, orgID string, enabled bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	feature, err := s.requireFeature(ctx, key)
	if err != nil {
		return err
	}
	if strings.TrimSpace(orgID) == "" {
		return fmt.Errorf("flagsmith: organisation id names the identity and is required")
	}
	identityID, err := s.ensureIdentity(ctx, orgID)
	if err != nil {
		return err
	}
	current, err := s.identityState(ctx, identityID, feature.ID)
	if err != nil {
		return err
	}
	body := map[string]any{"feature": feature.ID, "enabled": enabled}
	base := fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/", url.PathEscape(s.EnvironmentKey), identityID)
	if current == nil {
		return s.managementAPI(ctx, http.MethodPost, base, nil, body, nil)
	}
	return s.managementAPI(ctx, http.MethodPut, fmt.Sprintf("%s%d/", base, current.ID), nil, body, nil)
}

// DeleteOverride drops one organisation's override so the org falls back to
// whatever Flagsmith decides for everyone else.
//
//	DELETE {management}/api/v1/environments/{envKey}/identities/{pk}/featurestates/{id}/  (204)
//
// An absent identity or an absent override is not an error, matching the
// Postgres adapter's unconditional DELETE.
func (s *Service) DeleteOverride(ctx context.Context, key, orgID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	feature, err := s.feature(ctx, key)
	if err != nil {
		return err
	}
	if feature == nil {
		return nil
	}
	identityID, err := s.findIdentity(ctx, orgID)
	if err != nil {
		return err
	}
	if identityID == 0 {
		return nil
	}
	current, err := s.identityState(ctx, identityID, feature.ID)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	return s.managementAPI(ctx, http.MethodDelete,
		fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/%d/", url.PathEscape(s.EnvironmentKey), identityID, current.ID),
		nil, nil, nil)
}

// ---------------------------------------------------------------------------
// Management API helpers.
// ---------------------------------------------------------------------------

// features lists the project's features in the context of the configured
// environment. search narrows the provider-side query; an empty search lists
// everything.
func (s *Service) features(ctx context.Context, search string) ([]managementFeature, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	query := url.Values{"environment": {strconv.Itoa(s.EnvironmentID)}}
	if search != "" {
		query.Set("search", search)
	}
	out := make([]managementFeature, 0)
	err := s.paginate(ctx, fmt.Sprintf("/api/v1/projects/%d/features/", s.ProjectID), query,
		func(raw json.RawMessage) error {
			var page []managementFeature
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("flagsmith: decode features: %w", err)
			}
			out = append(out, page...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// feature resolves one feature by exact name. `search` is a provider-side
// substring filter, so the exact match is made here.
func (s *Service) feature(ctx context.Context, key string) (*managementFeature, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("flagsmith: flag key is required")
	}
	candidates, err := s.features(ctx, key)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if candidates[i].Name == key {
			return &candidates[i], nil
		}
	}
	return nil, nil
}

func (s *Service) requireFeature(ctx context.Context, key string) (*managementFeature, error) {
	feature, err := s.feature(ctx, key)
	if err != nil {
		return nil, err
	}
	if feature == nil {
		return nil, fmt.Errorf("flagsmith: no feature named %q in project %d", key, s.ProjectID)
	}
	return feature, nil
}

// findIdentity resolves an organisation id to its Flagsmith identity primary
// key, returning 0 when the organisation has never been identified.
func (s *Service) findIdentity(ctx context.Context, orgID string) (int, error) {
	if strings.TrimSpace(orgID) == "" {
		return 0, fmt.Errorf("flagsmith: organisation id names the identity and is required")
	}
	found := 0
	err := s.paginate(ctx, fmt.Sprintf("/api/v1/environments/%s/identities/", url.PathEscape(s.EnvironmentKey)),
		url.Values{"q": {`"` + orgID + `"`}},
		func(raw json.RawMessage) error {
			var page []struct {
				ID         int    `json:"id"`
				Identifier string `json:"identifier"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("flagsmith: decode identities: %w", err)
			}
			for _, item := range page {
				if item.Identifier == orgID {
					found = item.ID
				}
			}
			return nil
		})
	if err != nil {
		return 0, err
	}
	return found, nil
}

// ensureIdentity resolves the organisation's identity, creating it when the
// organisation has never been evaluated. The Management API creates identities
// explicitly; the Flags API creates them lazily as a side effect of
// evaluation, and an override must be settable before the org's first request.
func (s *Service) ensureIdentity(ctx context.Context, orgID string) (int, error) {
	existing, err := s.findIdentity(ctx, orgID)
	if err != nil {
		return 0, err
	}
	if existing != 0 {
		return existing, nil
	}
	var created struct {
		ID         int    `json:"id"`
		Identifier string `json:"identifier"`
	}
	if err := s.managementAPI(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/environments/%s/identities/", url.PathEscape(s.EnvironmentKey)), nil,
		map[string]any{"identifier": orgID}, &created); err != nil {
		return 0, err
	}
	if created.ID == 0 {
		return 0, fmt.Errorf("flagsmith: creating identity %q returned no id", orgID)
	}
	return created.ID, nil
}

// identityState returns the identity's existing override on one feature, or
// nil when it has none.
func (s *Service) identityState(ctx context.Context, identityID, featureID int) (*identityFeatureState, error) {
	var found *identityFeatureState
	err := s.paginate(ctx,
		fmt.Sprintf("/api/v1/environments/%s/identities/%d/featurestates/", url.PathEscape(s.EnvironmentKey), identityID),
		url.Values{"feature": {strconv.Itoa(featureID)}},
		func(raw json.RawMessage) error {
			var page []identityFeatureState
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("flagsmith: decode identity feature states: %w", err)
			}
			for i := range page {
				if page[i].Feature == featureID {
					state := page[i]
					found = &state
				}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// paginate walks a Management API collection. Every paginated collection in
// the document answers {count,next,previous,results}; `next` is an absolute
// URL, so it is followed as given rather than reconstructed.
func (s *Service) paginate(ctx context.Context, path string, query url.Values, consume func(json.RawMessage) error) error {
	next := ""
	for range pageLimit {
		var body struct {
			Next    string          `json:"next"`
			Results json.RawMessage `json:"results"`
		}
		var err error
		if next == "" {
			err = s.managementAPI(ctx, http.MethodGet, path, query, nil, &body)
		} else {
			err = s.managementRequest(ctx, http.MethodGet, next, nil, nil, &body)
		}
		if err != nil {
			return err
		}
		if len(body.Results) > 0 {
			if err := consume(body.Results); err != nil {
				return err
			}
		}
		if body.Next == "" {
			return nil
		}
		next = body.Next
	}
	return fmt.Errorf("flagsmith: %s returned more than %d pages", path, pageLimit)
}

// ---------------------------------------------------------------------------
// Transport.
// ---------------------------------------------------------------------------

// flagsAPI performs one Flags API call. The Environment Key is the whole
// credential and travels in X-Environment-Key.
func (s *Service) flagsAPI(ctx context.Context, method, path string, body any, out any) error {
	return s.do(ctx, method, s.Flags+path, nil, body, out, map[string]string{
		"X-Environment-Key": s.EnvironmentKey,
	})
}

// managementAPI performs one Management API call against a relative path.
func (s *Service) managementAPI(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	return s.managementRequest(ctx, method, s.Management+path, query, body, out)
}

// managementRequest performs one Management API call against an absolute URL,
// which is what pagination's `next` gives us.
func (s *Service) managementRequest(ctx context.Context, method, endpoint string, query url.Values, body any, out any) error {
	return s.do(ctx, method, endpoint, query, body, out, map[string]string{
		"Authorization": "Api-Key " + s.Token,
	})
}

func (s *Service) do(ctx context.Context, method, endpoint string, query url.Values, body any, out any, headers map[string]string) error {
	if s == nil || s.HTTP == nil {
		return fmt.Errorf("flagsmith: HTTP client is required")
	}
	if len(query) > 0 {
		separator := "?"
		if strings.Contains(endpoint, "?") {
			separator = "&"
		}
		endpoint += separator + query.Encode()
	}
	var reader io.Reader
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("flagsmith: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if encoded != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		// The path is named, the credential never is.
		return fmt.Errorf("flagsmith: %s %s: %d: %s", method, redactQuery(endpoint), resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	if out == nil {
		return nil
	}
	// 204 carries no body; a caller asking for output from a delete gets the
	// zero value rather than an EOF decode error.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("flagsmith: decode %s %s: %w", method, redactQuery(endpoint), err)
	}
	return nil
}

// redactQuery keeps an error message useful without quoting an organisation
// id out of a search query into a log line.
func redactQuery(endpoint string) string {
	if index := strings.IndexByte(endpoint, '?'); index >= 0 {
		return endpoint[:index]
	}
	return endpoint
}

// ready states the configuration every Management API call needs. The joined
// refusal lives in NewModule; this is the guard for a hand-built Service.
func (s *Service) ready() error {
	if s == nil {
		return fmt.Errorf("flagsmith: service is required")
	}
	var errs []error
	if strings.TrimSpace(s.Token) == "" {
		errs = append(errs, fmt.Errorf("flagsmith: FLAGSMITH_MANAGEMENT_TOKEN is required"))
	}
	if strings.TrimSpace(s.EnvironmentKey) == "" {
		errs = append(errs, fmt.Errorf("flagsmith: FLAGSMITH_ENVIRONMENT_KEY is required"))
	}
	if s.ProjectID <= 0 {
		errs = append(errs, fmt.Errorf("flagsmith: FLAGSMITH_PROJECT_ID is required"))
	}
	if s.EnvironmentID <= 0 {
		errs = append(errs, fmt.Errorf("flagsmith: FLAGSMITH_ENVIRONMENT_ID is required"))
	}
	return errors.Join(errs...)
}

func (s *Service) consoleOrDocs() string {
	if s != nil && strings.TrimSpace(s.Console) != "" {
		return s.Console
	}
	return "https://docs.flagsmith.com/managing-flags/rollout/rollout-by-percentage"
}

func (s *Service) logError(ctx context.Context, message string, err error, args ...any) {
	if s == nil || s.Log == nil {
		return
	}
	s.Log.ErrorContext(ctx, message, append([]any{"error", err}, args...)...)
}

// Health reports whether this adapter is wired to talk to Flagsmith, in the
// idiom of its seam siblings (internal/flags/postgres/postgres.go:83,
// internal/search/typesense/typesense.go:141): configuration and transport,
// not a live provider call. Health runs concurrently under a short deadline on
// every readiness check, so it must not spend a provider round trip.
func (s *Service) Health(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	if s.HTTP == nil {
		return fmt.Errorf("flagsmith: HTTP client is required")
	}
	if s.Cache == nil {
		return fmt.Errorf("flagsmith: cache store is required")
	}
	return ctx.Err()
}

// ---------------------------------------------------------------------------
// Module wiring.
// ---------------------------------------------------------------------------

// Deps is the generated bootstrap's constructor shape. Cache arrives as the
// declared cache.store capability, matching feature-flags-postgres; every
// other value is read from this adapter's own declared environment.
type Deps struct {
	Cache cache.Store
}

type Module struct{ Value *Service }

// NewModule builds the adapter or refuses the boot.
//
// Selecting this adapter is an explicit decision to evaluate flags through
// Flagsmith, so missing credentials are a refusal and never a silent fallback
// to the local Postgres adapter. Every missing key is named in ONE joined
// error, so an operator configuring production learns all of them from one
// failed boot instead of one per restart.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Cache == nil {
		return nil, fmt.Errorf("flagsmith: cache store is required")
	}
	if h == nil {
		return nil, fmt.Errorf("flagsmith: host is required")
	}
	service := Service{
		Flags:          h.Env("FLAGSMITH_FLAGS_API_URL"),
		Management:     h.Env("FLAGSMITH_API_URL"),
		EnvironmentKey: strings.TrimSpace(h.Env("FLAGSMITH_ENVIRONMENT_KEY")),
		Token:          strings.TrimSpace(h.Env("FLAGSMITH_MANAGEMENT_TOKEN")),
		Console:        strings.TrimSpace(h.Env("FLAGSMITH_CONSOLE")),
		Cache:          d.Cache,
		Log:            h.Log(),
	}
	var errs []error
	if service.EnvironmentKey == "" {
		errs = append(errs, fmt.Errorf("FLAGSMITH_ENVIRONMENT_KEY is required (public Environment Key, Flags API)"))
	}
	if service.Token == "" {
		errs = append(errs, fmt.Errorf("FLAGSMITH_MANAGEMENT_TOKEN is required (secret organisation API token, Management API)"))
	}
	var err error
	if service.ProjectID, err = positiveEnv(h, "FLAGSMITH_PROJECT_ID"); err != nil {
		errs = append(errs, err)
	}
	if service.EnvironmentID, err = positiveEnv(h, "FLAGSMITH_ENVIRONMENT_ID"); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("flagsmith: %w", errors.Join(errs...))
	}
	return &Module{Value: New(service)}, nil
}

// positiveEnv reads one required numeric Management API coordinate. A blank
// and a malformed value are distinguished, because they are different operator
// mistakes.
func positiveEnv(h apphost.Host, key string) (int, error) {
	raw := strings.TrimSpace(h.Env(key))
	if raw == "" {
		return 0, fmt.Errorf("%s is required (numeric Management API id)", key)
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return value, nil
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("flagsmith: service is required")
	}
	return m.Value.Health(ctx)
}
