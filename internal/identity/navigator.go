package identity

// PortalNavigator builds provider-hosted account URLs without exposing the
// provider client to generic web handlers.
type PortalNavigator struct{ BaseURL string }

func (n PortalNavigator) LoginURL(returnTo string) string {
	return n.BaseURL + "/sign-in?redirect_url=" + returnTo
}
func (n PortalNavigator) SignupURL(returnTo string) string {
	return n.BaseURL + "/sign-up?redirect_url=" + returnTo
}
func (n PortalNavigator) AccountURL() string { return n.BaseURL + "/sign-out" }
