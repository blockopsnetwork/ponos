package main

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

type GitHubAuthenticator struct {
	pemKey    []byte
	appID     int64
	installID int64
}

func NewGitHubAuthenticator(pemKey []byte, appID, installID int64) *GitHubAuthenticator {
	return &GitHubAuthenticator{
		pemKey:    pemKey,
		appID:     appID,
		installID: installID,
	}
}

func (auth *GitHubAuthenticator) GenerateJWT() (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(auth.pemKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse RSA private key: %v", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": auth.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

func (auth *GitHubAuthenticator) CreateClient(ctx context.Context) (*github.Client, error) {
	jwtToken, err := auth.GenerateJWT()
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT: %v", err)
	}

	appClient := github.NewClient(nil).WithAuthToken(jwtToken)
	
	installToken, _, err := appClient.Apps.CreateInstallationToken(
		ctx, auth.installID, &github.InstallationTokenOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create installation token: %v", err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: installToken.GetToken()})
	tc := oauth2.NewClient(ctx, ts)
	
	return github.NewClient(tc), nil
}