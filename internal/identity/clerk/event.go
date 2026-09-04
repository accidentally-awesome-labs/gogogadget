package clerk

import (
	"encoding/json"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/identity"
)

// envelope is the Clerk webhook envelope: {"type": "...", "data": {...}}.
type envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// parseEvent maps one Clerk delivery onto the neutral identity event. Every
// Clerk payload shape stays confined to this file; the receiver only ever
// sees identity.Event.
func parseEvent(msgID string, payload []byte) (identity.Event, error) {
	var raw envelope
	if err := json.Unmarshal(payload, &raw); err != nil {
		return identity.Event{}, err
	}
	out := identity.Event{ID: msgID, Provider: Provider, Type: raw.Type}
	switch raw.Type {
	case "user.created", "user.updated":
		id, p, err := parseUserData(raw.Data)
		if err != nil {
			return identity.Event{}, err
		}
		out.User = &identity.UserEvent{Subject: id, Email: p.Email, Name: p.Name, AvatarURL: p.AvatarURL}
	case "user.deleted":
		id, err := parseUserDeletedData(raw.Data)
		if err != nil {
			return identity.Event{}, err
		}
		out.User = &identity.UserEvent{Subject: id}
	case "organization.created", "organization.updated", "organization.deleted":
		id, name, slug, image, err := parseOrgData(raw.Data)
		if err != nil {
			return identity.Event{}, err
		}
		out.Organization = &identity.OrganizationEvent{Subject: id, Name: name, Slug: slug, ImageURL: image}
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		org, user, role, err := parseMembershipData(raw.Data)
		if err != nil {
			return identity.Event{}, err
		}
		out.Membership = &identity.MembershipEvent{OrganizationSubject: org, UserSubject: user, Role: role}
	}
	return out, nil
}

type userData struct {
	ID             string `json:"id"`
	EmailAddresses []struct {
		ID           string `json:"id"`
		EmailAddress string `json:"email_address"`
	} `json:"email_addresses"`
	PrimaryEmailAddressID *string `json:"primary_email_address_id"`
	FirstName             *string `json:"first_name"`
	LastName              *string `json:"last_name"`
	ImageURL              *string `json:"image_url"`
}

// parseUserData extracts the mirror payload from user.created/updated data.
func parseUserData(data json.RawMessage) (id string, profile identity.UserProfile, err error) {
	var d userData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", identity.UserProfile{}, fmt.Errorf("user data: %w", err)
	}
	if d.ID == "" {
		return "", identity.UserProfile{}, fmt.Errorf("user data: missing id")
	}
	email := ""
	if d.PrimaryEmailAddressID != nil {
		for _, e := range d.EmailAddresses {
			if e.ID == *d.PrimaryEmailAddressID {
				email = e.EmailAddress
				break
			}
		}
	}
	if email == "" && len(d.EmailAddresses) > 0 {
		email = d.EmailAddresses[0].EmailAddress
	}
	return d.ID, identity.UserProfile{
		Email:     email,
		Name:      DisplayName(d.FirstName, d.LastName, email),
		AvatarURL: deref(d.ImageURL),
	}, nil
}

// parseUserDeletedData extracts the id from user.deleted data.
func parseUserDeletedData(data json.RawMessage) (string, error) {
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.ID == "" {
		return "", fmt.Errorf("user.deleted data: missing id")
	}
	return d.ID, nil
}

type orgData struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url"`
}

// parseOrgData extracts the mirror payload from organization.* data.
func parseOrgData(data json.RawMessage) (id, name, slug, imageURL string, err error) {
	var d orgData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", "", "", "", fmt.Errorf("organization data: %w", err)
	}
	if d.ID == "" {
		return "", "", "", "", fmt.Errorf("organization data: missing id")
	}
	return d.ID, d.Name, d.Slug, d.ImageURL, nil
}

type membershipData struct {
	Organization struct {
		ID string `json:"id"`
	} `json:"organization"`
	PublicUserData struct {
		UserID string `json:"user_id"`
	} `json:"public_user_data"`
	Role string `json:"role"`
}

// parseMembershipData extracts org id, user id, and role from
// organizationMembership.* data.
func parseMembershipData(data json.RawMessage) (orgID, userID, role string, err error) {
	var d membershipData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", "", "", fmt.Errorf("membership data: %w", err)
	}
	if d.Organization.ID == "" || d.PublicUserData.UserID == "" {
		return "", "", "", fmt.Errorf("membership data: missing organization or user id")
	}
	return d.Organization.ID, d.PublicUserData.UserID, d.Role, nil
}
