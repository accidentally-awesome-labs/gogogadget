package identity

import (
	"context"
	"encoding/json"
	"net/http"
)

type ClerkWebhook struct{ Secret string }

func (w ClerkWebhook) Verify(_ context.Context, payload []byte, headers http.Header) (Event, error) {
	if err := VerifyClerkWebhook(w.Secret, payload, headers); err != nil {
		return Event{}, err
	}
	var raw ClerkEvent
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, err
	}
	out := Event{ID: headers.Get("svix-id"), Provider: "clerk", Type: raw.Type}
	switch raw.Type {
	case "user.created", "user.updated":
		id, p, err := ParseUserData(raw.Data)
		if err != nil {
			return Event{}, err
		}
		out.User = &UserEvent{Subject: id, Email: p.Email, Name: p.Name, AvatarURL: p.AvatarURL}
	case "user.deleted":
		id, err := ParseUserDeletedData(raw.Data)
		if err != nil {
			return Event{}, err
		}
		out.User = &UserEvent{Subject: id}
	case "organization.created", "organization.updated", "organization.deleted":
		id, name, slug, image, err := ParseOrgData(raw.Data)
		if err != nil {
			return Event{}, err
		}
		out.Organization = &OrganizationEvent{Subject: id, Name: name, Slug: slug, ImageURL: image}
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		org, user, role, err := ParseMembershipData(raw.Data)
		if err != nil {
			return Event{}, err
		}
		out.Membership = &MembershipEvent{OrganizationSubject: org, UserSubject: user, Role: role}
	}
	return out, nil
}
