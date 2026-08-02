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
