package identity

import (
  "context"
  "github.com/clerk/clerk-sdk-go/v2"
  "github.com/clerk/clerk-sdk-go/v2/user"
)

type Deleter interface { DeleteUser(context.Context, string) error }
type DevDeleter struct{}
func (DevDeleter) DeleteUser(context.Context,string) error { return nil }
type clerkDeleter struct { client *user.Client }
func NewClerkDeleter(secretKey string) Deleter { return &clerkDeleter{client:user.NewClient(&clerk.ClientConfig{BackendConfig:clerk.BackendConfig{Key:&secretKey}})} }
func (d *clerkDeleter) DeleteUser(ctx context.Context,userSubject string) error { _,err:=d.client.Delete(ctx,userSubject); return err }
