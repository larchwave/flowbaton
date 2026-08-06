package model

// EffectiveAppID is what a flow's commands are about.
//
// specs/01-core-engine.md:17 makes `url` the web equivalent of `appId`: "if
// present becomes effective appId". So this is the one place the rule lives —
// the session, the evaluation context and the element lookup all read it,
// because a flow whose target depends on which of them asked is a flow that
// taps in one app and asserts in another.
func (config Config) EffectiveAppID() string {
	if config.URL != "" {
		return config.URL
	}
	return config.AppID
}
