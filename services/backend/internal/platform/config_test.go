package platform

import "testing"

func TestIdentityConfigurationFailsClosed(t *testing.T) {
	c := Config{Environment: "local", AuthMode: "oidc", WebOrigin: "http://localhost:3100", OIDCIssuer: "http://localhost:8180/realms/jansetu", OIDCClientID: "jansetu-web"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, alter := range map[string]func(*Config){
		"production":             func(c *Config) { c.Environment = "production" },
		"unknown mode":           func(c *Config) { c.AuthMode = "other" },
		"insecure remote issuer": func(c *Config) { c.OIDCIssuer = "http://identity.example.test" },
		"issuer query":           func(c *Config) { c.OIDCIssuer += "?tenant=other" },
		"wildcard origin":        func(c *Config) { c.WebOrigin = "*" },
		"origin trailing slash":  func(c *Config) { c.WebOrigin += "/" },
		"origin path":            func(c *Config) { c.WebOrigin += "/anything" },
		"missing client":         func(c *Config) { c.OIDCClientID = "" },
		"HTTPS rewrite": func(c *Config) {
			c.OIDCIssuer = "https://identity.example.test"
			c.OIDCBackchannel = "http://identity:8080"
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := c
			alter(&copy)
			if copy.Validate() == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	c.WebOrigin = "https://jansetu.example.test"
	if !c.SecureCookies() {
		t.Fatal("HTTPS cookies are not secure")
	}
}
