package cli

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
)

// kitCertificateNotTrusted and kitCertificateVerificationFailed are the texts
// of the kit's ErrCertificateFingerprintNotTrusted and
// ErrCertificateVerificationFailed, which a trust-on-first-use cache reports
// for a certificate it was told not to trust. Like the two fingerprint texts
// beside WrapPinMismatch, they live in the kit's internal/ssl package, so pmx
// matches them by text.
const (
	kitCertificateNotTrusted         = "certificate fingerprint is not trusted"
	kitCertificateVerificationFailed = "certificate verification failed"
)

// stdlibVerifyFailure is the prefix crypto/tls gives a handshake that failed
// certificate verification. It catches the failure when a layer between the
// handshake and the caller kept the text but dropped the error chain.
const stdlibVerifyFailure = "tls: failed to verify certificate"

// IsCertVerificationError reports whether err is a failure to verify the
// server's TLS certificate: a CA bundle or the system roots that reject it,
// a fingerprint pin it does not match, or a trust-on-first-use cache that
// does not hold it. It inspects the error chain first and then the kit's
// error texts, because the kit's sentinels are internal to it. A connection
// that failed for any other reason, such as a refused dial or a timeout,
// reports false.
func IsCertVerificationError(err error) bool {
	if err == nil {
		return false
	}

	var verifyErr *tls.CertificateVerificationError
	var authorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	if errors.As(err, &verifyErr) || errors.As(err, &authorityErr) || errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidErr) || errors.Is(err, ErrPinMismatch) {
		return true
	}

	text := err.Error()
	for _, marker := range []string{
		kitCannotVerifyFingerprint,
		kitUnknownFingerprint,
		kitCertificateNotTrusted,
		kitCertificateVerificationFailed,
		stdlibVerifyFailure,
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
