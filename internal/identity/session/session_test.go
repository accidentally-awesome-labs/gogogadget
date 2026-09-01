package session

import (
	"context"
	"testing"
)

func TestSessionLoaderRequiresProviderAndDatabasePorts(t *testing.T) {
	var loader SessionLoader
	if _, err := loader.Load(context.Background(), "token"); err == nil {
		t.Fatal("Load with no dependencies succeeded")
	}
}

func TestLinkCommandRejectsIncompleteDestination(t *testing.T) {
	err := RunLinkCommand(context.Background(), nil, []string{
		"--environment", "development", "--provider", "dev", "--subject", "subject",
	})
	if err == nil {
		t.Fatal("link command accepted missing destination")
	}
}
