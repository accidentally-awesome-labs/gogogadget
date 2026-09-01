// Package notifications is the provider-neutral notification capability.
package notifications

import "context"

type Notifier interface {
	Send(context.Context, string, string, string, string, string, string) error
	SendOrg(context.Context, string, string, string, string, string) error
}
type Message struct{ OrgID, UserID, Kind, Title, Body, URL string }
type FuncNotifier struct {
	SendFn    func(context.Context, Message) error
	SendOrgFn func(context.Context, Message) error
}

func (n FuncNotifier) Send(c context.Context, o, u, k, t, b, url string) error {
	if n.SendFn == nil {
		return nil
	}
	return n.SendFn(c, Message{o, u, k, t, b, url})
}
func (n FuncNotifier) SendOrg(c context.Context, o, k, t, b, url string) error {
	if n.SendOrgFn == nil {
		return nil
	}
	return n.SendOrgFn(c, Message{OrgID: o, Kind: k, Title: t, Body: b, URL: url})
}
