package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"

	pveaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/access"
	pbsaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/access"
	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"
)

// ticketResult is the product-neutral outcome of a ticket (or OIDC) login:
// the fields every auth sub-command needs regardless of which product's
// response shape produced them.
type ticketResult struct {
	Username string
	Ticket   string // empty when the server withheld it (HttpOnly mode)
	CSRF     string
}

// authClient abstracts the per-product access service surface the auth
// sub-commands (login, refresh, logout, and OIDC) need.
// Implemented by pveAuthClient, pbsAuthClient, and pdmAuthClient below, each
// wrapping the matching apiclient.*Client and translating that product's
// request/response shapes to and from the neutral types here.
type authClient interface {
	CreateTicket(ctx context.Context, user, realm, password string, opts ticketOptions) (*ticketResult, error)
	OpenidAuthUrl(ctx context.Context, realm, redirectURL string) (string, error)
	OpenidLogin(ctx context.Context, code, state, redirectURL string) (*ticketResult, error)
	Logout() error
}

// newAuthClientForContext builds the product-appropriate client for ctx and
// wraps it in the matching authClient adapter. Exactly one of
// (user+realm+password), (ticket+csrf), or token should be non-empty,
// mirroring the old clientForContext contract; contextOptions decides which
// of those BuildOptions embeds.
//
// contextName is the resolved context name (see resolveContextName), used
// only to derive the per-context TOFU fingerprint cache path (see
// contextOptions).
func newAuthClientForContext(
	cmd *cobra.Command,
	ctx *config.Context,
	contextName, user, realm, password, token, ticket, csrf string,
) (authClient, error) {
	flagInsecure := cli.GetDeps(cmd).Insecure
	if flagInsecure || ctx.TLS.Insecure {
		cli.WarnInsecureTLS(cmd.ErrOrStderr())
	}

	rlm := realm
	if rlm == "" {
		rlm = ctx.Realm
	}
	opts := contextOptions(cmd, ctx, flagInsecure, contextName, user, rlm, token, password, ticket, csrf)

	switch ctx.Product {
	case config.ProductPVE, "":
		ac, err := apiclient.NewAPIClient(opts)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", ctx.Host, err)
		}
		return &pveAuthClient{ac: ac}, nil
	case config.ProductPBS:
		ac, err := apiclient.NewPBSClient(opts)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", ctx.Host, err)
		}
		return &pbsAuthClient{ac: ac}, nil
	case config.ProductPDM:
		ac, err := apiclient.NewPDMClient(opts)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", ctx.Host, err)
		}
		return &pdmAuthClient{ac: ac}, nil
	default:
		return nil, fmt.Errorf("unsupported product %q", ctx.Product)
	}
}

// qualifiedUsername returns "user@realm" if realm is non-empty and user does
// not already contain "@", otherwise returns user unchanged. It mirrors
// apiclient.qualifiedUser (unexported there): PBS and PDM's CreateTicketParams
// have no Realm field, so their adapters fold the realm into the username
// instead of passing it as a separate parameter the way the PVE adapter does.
func qualifiedUsername(user, realm string) string {
	if realm == "" {
		return user
	}
	for _, c := range user {
		if c == '@' {
			return user
		}
	}
	return user + "@" + realm
}

// ---------------------------------------------------------------------------
// PVE adapter
// ---------------------------------------------------------------------------

// pveAuthClient adapts an *apiclient.APIClient (PVE) to authClient.
type pveAuthClient struct {
	ac *apiclient.APIClient
}

func (c *pveAuthClient) CreateTicket(
	ctx context.Context, user, realm, password string, opts ticketOptions,
) (*ticketResult, error) {
	params := &pveaccess.CreateTicketParams{
		Username: user,
		Password: password,
	}
	if realm != "" {
		r := realm
		params.Realm = &r
	}
	if opts.otp != "" {
		params.Otp = &opts.otp
	}
	if opts.tfaChallenge != "" {
		params.TfaChallenge = &opts.tfaChallenge
	}
	if opts.verifyPath != "" {
		params.Path = &opts.verifyPath
	}
	if opts.verifyPrivs != "" {
		params.Privs = &opts.verifyPrivs
	}

	resp, err := c.ac.Access.CreateTicket(ctx, params)
	if err != nil {
		return nil, err
	}
	return ticketResultFromPVE(resp), nil
}

func (c *pveAuthClient) OpenidAuthUrl(ctx context.Context, realm, redirectURL string) (string, error) {
	resp, err := c.ac.Access.CreateOpenidAuthUrl(ctx, &pveaccess.CreateOpenidAuthUrlParams{
		Realm:       realm,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("server returned no data")
	}
	var authURL string
	if err := json.Unmarshal(*resp, &authURL); err != nil {
		return "", fmt.Errorf("parse OIDC auth URL response: %w", err)
	}
	return authURL, nil
}

func (c *pveAuthClient) OpenidLogin(ctx context.Context, code, state, redirectURL string) (*ticketResult, error) {
	resp, err := c.ac.Access.CreateOpenidLogin(ctx, &pveaccess.CreateOpenidLoginParams{
		Code:        code,
		State:       state,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("server returned no data")
	}
	var ticketResp pveaccess.CreateTicketResponse
	if err := json.Unmarshal(*resp, &ticketResp); err != nil {
		return nil, fmt.Errorf("parse OIDC login response: %w", err)
	}
	return ticketResultFromPVE(&ticketResp), nil
}

func (c *pveAuthClient) Logout() error {
	return c.ac.Raw.Logout()
}

// ticketResultFromPVE converts a PVE CreateTicketResponse to the
// product-neutral ticketResult shape.
func ticketResultFromPVE(resp *pveaccess.CreateTicketResponse) *ticketResult {
	tr := &ticketResult{Username: resp.Username}
	if resp.Ticket != nil {
		tr.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		tr.CSRF = *resp.CSRFPreventionToken
	}
	return tr
}

// ---------------------------------------------------------------------------
// PBS adapter
// ---------------------------------------------------------------------------

// pbsAuthClient adapts an *apiclient.PBSClient to authClient.
type pbsAuthClient struct {
	ac *apiclient.PBSClient
}

func (c *pbsAuthClient) CreateTicket(
	ctx context.Context, user, realm, password string, opts ticketOptions,
) (*ticketResult, error) {
	if opts.otp != "" {
		return nil, fmt.Errorf(
			"--otp is not supported for %s contexts; PBS/PDM use --tfa-challenge for second-factor login",
			"Proxmox Backup Server",
		)
	}

	params := &pbsaccess.CreateTicketParams{
		Username: qualifiedUsername(user, realm),
		Password: &password,
	}
	if opts.tfaChallenge != "" {
		params.TfaChallenge = &opts.tfaChallenge
	}
	if opts.verifyPath != "" {
		params.Path = &opts.verifyPath
	}
	if opts.verifyPrivs != "" {
		params.Privs = &opts.verifyPrivs
	}

	resp, err := c.ac.Access.CreateTicket(ctx, params)
	if err != nil {
		return nil, err
	}
	return ticketResultFromPBSTicket(resp), nil
}

func (c *pbsAuthClient) OpenidAuthUrl(ctx context.Context, realm, redirectURL string) (string, error) {
	resp, err := c.ac.Access.CreateOpenidAuthUrl(ctx, &pbsaccess.CreateOpenidAuthUrlParams{
		Realm:       realm,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("server returned no data")
	}
	var authURL string
	if err := json.Unmarshal(*resp, &authURL); err != nil {
		return "", fmt.Errorf("parse OIDC auth URL response: %w", err)
	}
	return authURL, nil
}

func (c *pbsAuthClient) OpenidLogin(ctx context.Context, code, state, redirectURL string) (*ticketResult, error) {
	resp, err := c.ac.Access.CreateOpenidLogin(ctx, &pbsaccess.CreateOpenidLoginParams{
		Code:        code,
		State:       state,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("server returned no data")
	}
	tr := &ticketResult{Username: resp.Username, CSRF: resp.CSRFPreventionToken}
	if resp.Ticket != nil {
		tr.Ticket = *resp.Ticket
	}
	return tr, nil
}

func (c *pbsAuthClient) Logout() error {
	return c.ac.Raw.Logout()
}

// ticketResultFromPBSTicket converts a PBS CreateTicketResponse (from
// POST /access/ticket) to the product-neutral ticketResult shape.
func ticketResultFromPBSTicket(resp *pbsaccess.CreateTicketResponse) *ticketResult {
	tr := &ticketResult{Username: resp.Username}
	if resp.Ticket != nil {
		tr.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		tr.CSRF = *resp.CSRFPreventionToken
	}
	return tr
}

// ---------------------------------------------------------------------------
// PDM adapter
// ---------------------------------------------------------------------------

// pdmAuthClient adapts an *apiclient.PDMClient to authClient.
type pdmAuthClient struct {
	ac *apiclient.PDMClient
}

func (c *pdmAuthClient) CreateTicket(
	ctx context.Context, user, realm, password string, opts ticketOptions,
) (*ticketResult, error) {
	if opts.otp != "" {
		return nil, fmt.Errorf(
			"--otp is not supported for %s contexts; PBS/PDM use --tfa-challenge for second-factor login",
			"Proxmox Datacenter Manager",
		)
	}

	params := &pdmaccess.CreateTicketParams{
		Username: qualifiedUsername(user, realm),
		Password: &password,
	}
	if opts.tfaChallenge != "" {
		params.TfaChallenge = &opts.tfaChallenge
	}
	if opts.verifyPath != "" {
		params.Path = &opts.verifyPath
	}
	if opts.verifyPrivs != "" {
		params.Privs = &opts.verifyPrivs
	}

	resp, err := c.ac.Access.CreateTicket(ctx, params)
	if err != nil {
		return nil, err
	}
	return ticketResultFromPDMTicket(resp), nil
}

func (c *pdmAuthClient) OpenidAuthUrl(ctx context.Context, realm, redirectURL string) (string, error) {
	resp, err := c.ac.Access.CreateOpenidAuthUrl(ctx, &pdmaccess.CreateOpenidAuthUrlParams{
		Realm:       realm,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("server returned no data")
	}
	var authURL string
	if err := json.Unmarshal(*resp, &authURL); err != nil {
		return "", fmt.Errorf("parse OIDC auth URL response: %w", err)
	}
	return authURL, nil
}

func (c *pdmAuthClient) OpenidLogin(ctx context.Context, code, state, redirectURL string) (*ticketResult, error) {
	resp, err := c.ac.Access.CreateOpenidLogin(ctx, &pdmaccess.CreateOpenidLoginParams{
		Code:        code,
		State:       state,
		RedirectUrl: redirectURL,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("server returned no data")
	}
	tr := &ticketResult{Username: resp.Username}
	if resp.Ticket != nil {
		tr.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		tr.CSRF = *resp.CSRFPreventionToken
	}
	return tr, nil
}

func (c *pdmAuthClient) Logout() error {
	return c.ac.Raw.Logout()
}

// ticketResultFromPDMTicket converts a PDM CreateTicketResponse (from
// POST /access/ticket) to the product-neutral ticketResult shape.
func ticketResultFromPDMTicket(resp *pdmaccess.CreateTicketResponse) *ticketResult {
	tr := &ticketResult{Username: resp.Username}
	if resp.Ticket != nil {
		tr.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		tr.CSRF = *resp.CSRFPreventionToken
	}
	return tr
}
