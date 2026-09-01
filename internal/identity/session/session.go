// Package identitysession maps verified provider subjects to opaque domain IDs.
// It is deliberately the only session layer used by HTTP middleware.
package session

import (
  "context"
  "crypto/rand"
  "errors"
  "fmt"

  "github.com/gogogadget/gogogadget/internal/identity"
  "github.com/gogogadget/gogogadget/internal/db/sqlc"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

type Session struct { Claims identity.Claims; User sqlc.User; Org *sqlc.Org }
type Loader interface { Load(context.Context,string) (Session,error) }
type SessionLoader struct { Pool *pgxpool.Pool; Verify identity.Verifier; Fetch identity.UserFetcher }

func (l *SessionLoader) Load(ctx context.Context, token string) (Session,error) {
  if l==nil || l.Pool==nil || l.Verify==nil || l.Fetch==nil { return Session{}, errors.New("identity-session: dependencies are required") }
  pc,err:=l.Verify.Verify(ctx,token); if err!=nil{return Session{},err}
  if pc==nil || pc.Provider=="" || pc.UserSubject=="" { return Session{},errors.New("identity-session: verifier returned incomplete claims") }
  tx,err:=l.Pool.Begin(ctx); if err!=nil{return Session{},err}; defer tx.Rollback(ctx)
  q:=sqlc.New(tx)
  mapped,err:=q.GetIdentitySubject(ctx,sqlc.GetIdentitySubjectParams{Provider:pc.Provider,Subject:pc.UserSubject})
  var user sqlc.User
  if err==nil { user,err=q.GetUserByID(ctx,mapped.UserID) } else if !errors.Is(err,pgx.ErrNoRows) { return Session{},err } else {
    profile,fetchErr:=l.Fetch.Fetch(ctx,pc.UserSubject); if fetchErr!=nil{return Session{},fetchErr}
    // Mutable profile data never links to an existing user implicitly.
    if _,emailErr:=q.GetUserByEmail(ctx,profile.Email); emailErr==nil { return Session{},identity.ErrLinkRequired } else if !errors.Is(emailErr,pgx.ErrNoRows) { return Session{},emailErr }
    userID,genErr:=opaqueID("usr_"); if genErr!=nil{return Session{},genErr}
    user,err=q.UpsertUser(ctx,sqlc.UpsertUserParams{UserID:userID,Email:profile.Email,Name:profile.Name,AvatarUrl:profile.AvatarURL}); if err!=nil{return Session{},err}
    inserted,insertErr:=q.InsertIdentitySubject(ctx,sqlc.InsertIdentitySubjectParams{Provider:pc.Provider,Subject:pc.UserSubject,UserID:user.UserID})
    if insertErr!=nil { if !errors.Is(insertErr,pgx.ErrNoRows) { return Session{},insertErr }; mapped,err=q.GetIdentitySubject(ctx,sqlc.GetIdentitySubjectParams{Provider:pc.Provider,Subject:pc.UserSubject}); if err!=nil{return Session{},err}; user,err=q.GetUserByID(ctx,mapped.UserID); if err!=nil{return Session{},err} } else { user.UserID=inserted.UserID }
  }
  claims:=identity.Claims{UserID:user.UserID,OrgRole:pc.OrgRole,OrgSlug:pc.OrgSlug}
  var org *sqlc.Org
  if pc.OrgSubject!="" {
    om,orgErr:=q.GetIdentityOrganization(ctx,sqlc.GetIdentityOrganizationParams{Provider:pc.Provider,Subject:pc.OrgSubject})
    if orgErr==nil { o,loadErr:=q.GetOrgByID(ctx,om.OrgID); if loadErr!=nil{return Session{},loadErr}; org=&o } else if !errors.Is(orgErr,pgx.ErrNoRows) { return Session{},orgErr } else {
      orgID,genErr:=opaqueID("org_"); if genErr!=nil{return Session{},genErr}
      o,upErr:=q.UpsertOrg(ctx,sqlc.UpsertOrgParams{OrgID:orgID,Name:pc.OrgSlug,Slug:pc.OrgSlug,ImageUrl:""}); if upErr!=nil{return Session{},upErr}
      if _,upErr=q.InsertIdentityOrganization(ctx,sqlc.InsertIdentityOrganizationParams{Provider:pc.Provider,Subject:pc.OrgSubject,OrgID:o.OrgID}); upErr!=nil{return Session{},upErr}
      if upErr=q.UpsertMembership(ctx,sqlc.UpsertMembershipParams{OrgID:o.OrgID,UserID:user.UserID,Role:pc.OrgRole}); upErr!=nil{return Session{},upErr}; org=&o
    }
    claims.OrgID=org.OrgID
  }
  if err=tx.Commit(ctx); err!=nil{return Session{},err}; return Session{Claims:claims,User:user,Org:org},nil
}
func opaqueID(prefix string)(string,error){ var b [16]byte; if _,err:=rand.Read(b[:]);err!=nil{return "",fmt.Errorf("identity-session: generate id: %w",err)}; return prefix+fmt.Sprintf("%x",b[:]),nil }
