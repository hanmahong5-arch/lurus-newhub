package repo

// user_access_token.go — SetAccessToken, a pure move out of user.go to pay for the
// source-size ratchet; behaviour unchanged.

func (user *User) SetAccessToken(token string) {
	user.AccessToken = &token
}
