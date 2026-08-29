package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// question with the answers.
func TestCheckboxGroupAssociatesItsQuestion(t *testing.T) {
	html := renderComponent(t, CheckboxGroup(CheckboxGroupOpts{
		Name: "channels", Legend: "Notification channels",
		Options: []Option{{Value: "email", Label: "Email", Selected: true}, {Value: "sms", Label: "SMS"}},
	}))
	assert.Contains(t, html, "<fieldset")
	assert.Contains(t, html, "<legend")
	assert.Contains(t, html, "Notification channels")
	assert.Equal(t, 2, strings.Count(html, `name="channels"`),
		"every option submits under the group name")
	assert.Contains(t, html, `checked`)
}

// A form's action row puts the primary action where reading ends. The Align
// enum's own default is start, so an unset value must be handled explicitly or
