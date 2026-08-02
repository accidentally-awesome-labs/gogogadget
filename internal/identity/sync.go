package identity

import (
	"encoding/json"
	"fmt"
)

// ClerkEvent is the webhook envelope: {"type": "...", "data": {...}}.
type ClerkEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type clerkUserData struct {
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

// ParseUserData extracts the mirror payload from user.created/updated data.
func ParseUserData(data json.RawMessage) (id string, profile UserProfile, err error) {
	var d clerkUserData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", UserProfile{}, fmt.Errorf("user data: %w", err)
	}
	if d.ID == "" {
		return "", UserProfile{}, fmt.Errorf("user data: missing id")
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
	return d.ID, UserProfile{
		Email:     email,
		Name:      DisplayName(d.FirstName, d.LastName, email),
		AvatarURL: deref(d.ImageURL),
	}, nil
}

// ParseUserDeletedData extracts the id from user.deleted data.
func ParseUserDeletedData(data json.RawMessage) (string, error) {
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.ID == "" {
		return "", fmt.Errorf("user.deleted data: missing id")
	}
	return d.ID, nil
}

type clerkOrgData struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url"`
}

// ParseOrgData extracts the mirror payload from organization.* data.
func ParseOrgData(data json.RawMessage) (id, name, slug, imageURL string, err error) {
	var d clerkOrgData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", "", "", "", fmt.Errorf("organization data: %w", err)
	}
	if d.ID == "" {
		return "", "", "", "", fmt.Errorf("organization data: missing id")
	}
	return d.ID, d.Name, d.Slug, d.ImageURL, nil
}

type clerkMembershipData struct {
	Organization struct {
		ID string `json:"id"`
	} `json:"organization"`
	PublicUserData struct {
		UserID string `json:"user_id"`
	} `json:"public_user_data"`
	Role string `json:"role"`
}

// ParseMembershipData extracts org id, user id, and role from
// organizationMembership.* data.
func ParseMembershipData(data json.RawMessage) (orgID, userID, role string, err error) {
	var d clerkMembershipData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", "", "", fmt.Errorf("membership data: %w", err)
	}
	if d.Organization.ID == "" || d.PublicUserData.UserID == "" {
		return "", "", "", fmt.Errorf("membership data: missing organization or user id")
	}
	return d.Organization.ID, d.PublicUserData.UserID, d.Role, nil
}
