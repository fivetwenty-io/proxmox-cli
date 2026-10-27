package cli_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

func TestIsCertVerificationError(t *testing.T) {
	wrapped := func(err error) error {
		return fmt.Errorf("wait task UPID:pve1: %w",
			&url.Error{Op: "Get", URL: "https://pve1:8006/api2/json/version", Err: err})
	}

	for name, err := range map[string]error{
		"stdlib verification error": wrapped(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}),
		"unknown authority":         wrapped(x509.UnknownAuthorityError{}),
		"hostname mismatch":         wrapped(x509.HostnameError{Host: "10.0.0.1"}),
		"expired":                   wrapped(x509.CertificateInvalidError{Reason: x509.Expired}),
		"pmx pin":                   wrapped(fmt.Errorf("tls fingerprint pin: %w", cli.ErrPinMismatch)),
		"kit pin":                   wrapped(errors.New("cannot verify certificate fingerprint: AB:CD")),
		"kit tofu unknown": wrapped(errors.New(
			"unknown certificate fingerprint (manual verification required): AB:CD")),
		"kit tofu rejected":   wrapped(errors.New("certificate fingerprint is not trusted: AB:CD")),
		"kit callback failed": wrapped(errors.New("certificate verification failed for fingerprint AB:CD")),
		"flattened chain":     errors.New("Get: tls: failed to verify certificate: x509: unknown authority"),
	} {
		t.Run(name, func(t *testing.T) {
			require.True(t, cli.IsCertVerificationError(err))
		})
	}

	for name, err := range map[string]error{
		"nil":      nil,
		"refused":  wrapped(errors.New("dial tcp 10.0.0.1:8006: connect: connection refused")),
		"timeout":  wrapped(context.DeadlineExceeded),
		"api 500":  errors.New("API error 500: task status unavailable"),
		"canceled": context.Canceled,
	} {
		t.Run("not "+name, func(t *testing.T) {
			require.False(t, cli.IsCertVerificationError(err))
		})
	}
}
