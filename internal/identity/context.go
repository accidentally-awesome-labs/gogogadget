package identity

import (
	"context"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

// Context keys (exact): the request-scoped identity bag set by Load and LoadPlan.
type ctxKey string

const (
	ctxUser   ctxKey = "user"   // local users mirror row
	ctxClaims ctxKey = "claims" // session claims
	ctxOrg    ctxKey = "org"    // local orgs row for the active org
	ctxPlan   ctxKey = "plan"   // billing.Plan, set by LoadPlan after RequireOrg
	ctxSub    ctxKey = "sub"    // *sqlc.Subscription (nil = free), set by LoadPlan
)

func WithUser(ctx context.Context, u *sqlc.User) context.Context {
	return context.WithValue(ctx, ctxUser, u)
}
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxClaims, c)
}
func WithOrg(ctx context.Context, o *sqlc.Org) context.Context {
	return context.WithValue(ctx, ctxOrg, o)
}
func WithPlan(ctx context.Context, p billing.Plan) context.Context {
	return context.WithValue(ctx, ctxPlan, p)
}
func WithSub(ctx context.Context, s *sqlc.Subscription) context.Context {
	return context.WithValue(ctx, ctxSub, s)
}

func UserFrom(ctx context.Context) *sqlc.User {
	u, _ := ctx.Value(ctxUser).(*sqlc.User)
	return u
}

func ClaimsFrom(ctx context.Context) *Claims {
	c, _ := ctx.Value(ctxClaims).(*Claims)
	return c
}

func OrgFrom(ctx context.Context) *sqlc.Org {
	o, _ := ctx.Value(ctxOrg).(*sqlc.Org)
	return o
}

func PlanFrom(ctx context.Context) billing.Plan {
	p, ok := ctx.Value(ctxPlan).(billing.Plan)
	if !ok {
		return billing.PlanByKey("free")
	}
	return p
}

// SubFrom returns the org's subscription row, or nil on the free plan.
func SubFrom(ctx context.Context) *sqlc.Subscription {
	s, _ := ctx.Value(ctxSub).(*sqlc.Subscription)
	return s
}

// Impersonator carries the active admin-impersonation session (ctx value set
// by sessionLoad's override, AFTER the real JWT verify).
type Impersonator struct {
	AdminUserID string
	SessionID   string
}

type ctxImpersonatorKey struct{}

// WithImpersonator marks the request as impersonated.
func WithImpersonator(ctx context.Context, imp Impersonator) context.Context {
	return context.WithValue(ctx, ctxImpersonatorKey{}, imp)
}

// ImpersonatorFrom returns the active impersonation session, or nil.
func ImpersonatorFrom(ctx context.Context) *Impersonator {
	imp, ok := ctx.Value(ctxImpersonatorKey{}).(Impersonator)
	if !ok {
		return nil
	}
	return &imp
}

// Staff roles on users.admin_role. Empty means "not staff".
const (
	RoleSupport = "support" // read-only access to /admin
	RoleAdmin   = "admin"   // full platform access, including impersonation
)

// IsStaff reports whether the user may READ the admin area.
func IsStaff(u *sqlc.User) bool {
	return u != nil && (u.AdminRole == RoleSupport || u.AdminRole == RoleAdmin)
}

// IsAdmin reports whether the user may CHANGE platform state: impersonate,
// disable accounts, mutate flags and schedules, requeue jobs, publish
// announcements, grant roles.
func IsAdmin(u *sqlc.User) bool {
	return u != nil && u.AdminRole == RoleAdmin
}
