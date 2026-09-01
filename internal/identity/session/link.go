package session

import (
	"context"
	"errors"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

type LinkRequest struct{ Provider, Subject, UserID, OrgID string }
type Linker struct {
	Pool   *pgxpool.Pool
	Verify identity.Verifier
}

func (l *Linker) Link(ctx context.Context, r LinkRequest) error {
	if l == nil || l.Pool == nil || l.Verify == nil {
		return errors.New("identity: link dependencies are required")
	}
	if r.Provider == "" || r.Subject == "" || (r.UserID == "") == (r.OrgID == "") {
		return errors.New("identity: exactly one user or organization destination is required")
	}
	// A subject is not a session token. Require the adapter's explicit
	// subject-verification port instead of passing it to Verify.
	sv, ok := l.Verify.(identity.SubjectVerifier)
	if !ok {
		return errors.New("identity: adapter does not support subject verification")
	}
	claims, err := sv.VerifySubject(ctx, r.Subject)
	if err != nil {
		return err
	}
	if claims == nil || claims.Provider != r.Provider || claims.UserSubject != r.Subject && claims.OrgSubject != r.Subject {
		return fmt.Errorf("identity: provider subject verification mismatch")
	}
	tx, err := l.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	if r.UserID != "" {
		if _, err = q.GetUserByID(ctx, r.UserID); err != nil {
			return err
		}
		if _, err = q.InsertIdentitySubject(ctx, sqlc.InsertIdentitySubjectParams{Provider: r.Provider, Subject: r.Subject, UserID: r.UserID}); err != nil {
			prior, e := q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: r.Provider, Subject: r.Subject})
			if e != nil || prior.UserID != r.UserID {
				return fmt.Errorf("identity: subject already linked to another user")
			}
		}
		audit.Log(ctx, q, "", r.UserID, "identity.linked", map[string]any{"provider": r.Provider, "subject": r.Subject})
	} else {
		if _, err = q.GetOrgByID(ctx, r.OrgID); err != nil {
			return err
		}
		if _, err = q.InsertIdentityOrganization(ctx, sqlc.InsertIdentityOrganizationParams{Provider: r.Provider, Subject: r.Subject, OrgID: r.OrgID}); err != nil {
			prior, e := q.GetIdentityOrganization(ctx, sqlc.GetIdentityOrganizationParams{Provider: r.Provider, Subject: r.Subject})
			if e != nil || prior.OrgID != r.OrgID {
				return fmt.Errorf("identity: subject already linked to another organization")
			}
		}
		audit.Log(ctx, q, r.OrgID, "", "identity.linked", map[string]any{"provider": r.Provider, "subject": r.Subject})
	}
	return tx.Commit(ctx)
}

// RunLinkCommand is the noninteractive command core used by ggg identity link.
// It accepts exactly --environment, --provider, --subject, and one destination.
func RunLinkCommand(ctx context.Context, linker *Linker, args []string) error {
	var r LinkRequest
	var environment string
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return errors.New("identity link: missing flag value")
		}
		switch args[i] {
		case "--environment":
			environment = args[i+1]
		case "--provider":
			r.Provider = args[i+1]
		case "--subject":
			r.Subject = args[i+1]
		case "--user":
			r.UserID = args[i+1]
		case "--org":
			r.OrgID = args[i+1]
		default:
			return fmt.Errorf("identity link: unknown flag %s", args[i])
		}
		i++
	}
	if environment != "development" && environment != "test" && environment != "production" {
		return errors.New("identity link: environment must be development, test, or production")
	}
	return linker.Link(ctx, r)
}
