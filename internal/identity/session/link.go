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

type LinkRequest struct { Provider, Subject, UserID, OrgID string }
type Linker struct { Pool *pgxpool.Pool; Verify identity.Verifier }
func (l *Linker) Link(ctx context.Context,r LinkRequest) error {
  if l==nil||l.Pool==nil||l.Verify==nil{return errors.New("identity: link dependencies are required")}
  if r.Provider==""||r.Subject==""||(r.UserID=="")== (r.OrgID==""){return errors.New("identity: exactly one user or organization destination is required")}
  // Verify the subject through the selected provider adapter before writing.
  claims,err:=l.Verify.Verify(ctx,r.Subject);if err!=nil{return err};if claims==nil||claims.Provider!=r.Provider{return fmt.Errorf("identity: provider subject verification mismatch")}
  tx,err:=l.Pool.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx);q:=sqlc.New(tx)
  if r.UserID!="" {
    if _,err=q.GetUserByID(ctx,r.UserID);err!=nil{return err}
    if _,err=q.InsertIdentitySubject(ctx,sqlc.InsertIdentitySubjectParams{Provider:r.Provider,Subject:r.Subject,UserID:r.UserID});err!=nil {
      prior,e:=q.GetIdentitySubject(ctx,sqlc.GetIdentitySubjectParams{Provider:r.Provider,Subject:r.Subject});if e!=nil||prior.UserID!=r.UserID{return fmt.Errorf("identity: subject already linked to another user")}
    }
    audit.Log(ctx,q,"",r.UserID,"identity.linked",map[string]any{"provider":r.Provider,"subject":r.Subject})
  } else {
    if _,err=q.GetOrgByID(ctx,r.OrgID);err!=nil{return err}
    if _,err=q.InsertIdentityOrganization(ctx,sqlc.InsertIdentityOrganizationParams{Provider:r.Provider,Subject:r.Subject,OrgID:r.OrgID});err!=nil {
      prior,e:=q.GetIdentityOrganization(ctx,sqlc.GetIdentityOrganizationParams{Provider:r.Provider,Subject:r.Subject});if e!=nil||prior.OrgID!=r.OrgID{return fmt.Errorf("identity: subject already linked to another organization")}
    }
    audit.Log(ctx,q,r.OrgID,"","identity.linked",map[string]any{"provider":r.Provider,"subject":r.Subject})
  }
  return tx.Commit(ctx)
}
